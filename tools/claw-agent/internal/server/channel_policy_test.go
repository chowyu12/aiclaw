package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/agent"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

func policyServer(t *testing.T, upstream string) (*Server, *model.ChannelBinding) {
	t.Helper()
	s, err := New(Options{DataHome: filepath.Join(t.TempDir(), "sessions"), AppDB: filepath.Join(t.TempDir(), "app.db"), Logf: t.Logf}, &receiptWriter{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.closeAll)
	provider, err := s.providers.Create(context.Background(), protocol.ProviderCreateParams{Name: "local fixture", Type: "openai-compatible", BaseURL: upstream, APIKey: "sk-test", Models: []string{"fake"}})
	if err != nil {
		t.Fatal(err)
	}
	binding := &model.ChannelBinding{PluginUUID: "test-plugin", ChannelID: "wecom", ConnectionID: "test-connection", ExternalKey: "external-chat", Allowed: true, ProviderID: provider.ID, ModelName: "fake"}
	if err := s.appDB.SaveChannelBinding(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	return s, binding
}

func TestChannelModeDefaultsAndChangesOnNextMessage(t *testing.T) {
	ctx := context.Background()
	s, binding := policyServer(t, "http://127.0.0.1:1/v1")
	gateway := &channelGateway{server: s}
	binding.AllowedTools = model.JSON(`["write_file","run_command"]`)
	session, err := gateway.sessionFor(ctx, binding)
	if err != nil {
		t.Fatal(err)
	}
	if session.Config().ApprovalPolicy != protocol.ApprovalNever {
		t.Fatal("legacy selection elevated permissions")
	}
	if !slices.Contains(session.Tools(), "write_file") {
		t.Fatal("mode should control approval, not a checkbox whitelist")
	}
	if err := s.appDB.SaveChannelBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	id := session.ID
	for _, mode := range []protocol.ApprovalPolicy{protocol.ApprovalBypass, protocol.ApprovalNever} {
		binding.ApprovalPolicy = string(mode)
		if err := s.appDB.SaveChannelBinding(ctx, binding); err != nil {
			t.Fatal(err)
		}
		session, err = gateway.sessionFor(ctx, binding)
		if err != nil {
			t.Fatal(err)
		}
		if session.ID != id || session.Config().ApprovalPolicy != mode {
			t.Fatalf("mode not applied to existing chat: %+v", session.Config())
		}
		if session.Config().EnableAppConfig || session.Config().EnableEmail || session.Config().EnableSchedule {
			t.Fatal("mode enabled owner-only capabilities")
		}
	}
}

func TestChannelAuthorizationPersistsAndRejectsInvalidModes(t *testing.T) {
	ctx := context.Background()
	s, b := policyServer(t, "http://127.0.0.1:1/v1")
	key := protocol.ChannelBindingKey{PluginUUID: b.PluginUUID, ChannelID: b.ChannelID, ConnectionID: b.ConnectionID, ExternalKey: b.ExternalKey}
	params := protocol.ChannelAuthorizeParams{ChannelBindingKey: key, ProviderID: b.ProviderID, Model: b.ModelName, ApprovalPolicy: protocol.ApprovalBypass}
	if err := s.plugins.Authorize(ctx, params); err != nil {
		t.Fatal(err)
	}
	saved, _ := s.appDB.GetChannelBinding(ctx, b.PluginUUID, b.ChannelID, b.ConnectionID, b.ExternalKey)
	if saved.ApprovalPolicy != "bypass" {
		t.Fatal(saved.ApprovalPolicy)
	}
	params.ApprovalPolicy = protocol.ApprovalAlways
	if err := s.plugins.Authorize(ctx, params); err == nil {
		t.Fatal("interactive mode accepted for external chat")
	}
	params.ApprovalPolicy = ""
	if err := s.plugins.Authorize(ctx, params); err != nil {
		t.Fatal(err)
	}
	saved, _ = s.appDB.GetChannelBinding(ctx, b.PluginUUID, b.ChannelID, b.ConnectionID, b.ExternalKey)
	if saved.ApprovalPolicy != "never" {
		t.Fatal("omitted mode must fail closed")
	}
}

func TestDesktopRefreshCannotOverrideSavedChannelMode(t *testing.T) {
	ctx := context.Background()
	s, b := policyServer(t, "http://127.0.0.1:1/v1")
	gateway := &channelGateway{server: s}
	session, err := gateway.sessionFor(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	b.ApprovalPolicy = "bypass"
	if err := s.appDB.SaveChannelBinding(ctx, b); err != nil {
		t.Fatal(err)
	}
	for _, desktopPolicy := range []protocol.ApprovalPolicy{protocol.ApprovalAlways, protocol.ApprovalNever} {
		got, err := s.channelSessionRefresh(ctx, session.ID, &protocol.SessionRefresh{ApprovalPolicy: desktopPolicy, EnableAppConfig: true, EnableEmail: true, MCPServers: map[string]protocol.MCPServerConfig{"third_party": {URL: "https://example.com"}}})
		if err != nil || got.ApprovalPolicy != protocol.ApprovalBypass || got.EnableAppConfig || got.EnableEmail || len(got.MCPServers) != 0 {
			t.Fatalf("bad refresh: %+v %v", got, err)
		}
	}
	b.Allowed = false
	if err := s.appDB.SaveChannelBinding(ctx, b); err != nil {
		t.Fatal(err)
	}
	got, err := s.channelSessionRefresh(ctx, session.ID, nil)
	if err != nil || got.ApprovalPolicy != protocol.ApprovalNever {
		t.Fatal("revoked chat retained bypass")
	}
}

func TestChannelModesEnforceFileWriteApproval(t *testing.T) {
	for _, mode := range []string{"never", "bypass"} {
		t.Run(mode, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "result.txt")
			args, _ := json.Marshal(map[string]string{"path": output, "content": "approved output"})
			fake := &scriptedModel{root: func(step int, _ wireRequest) string {
				if step == 0 {
					return sseToolCall("write", "write_file", string(args))
				}
				return sseText("done")
			}}
			upstream := httptest.NewServer(http.HandlerFunc(fake.handler))
			defer upstream.Close()
			s, b := policyServer(t, upstream.URL)
			b.ApprovalPolicy = mode
			gateway := &channelGateway{server: s}
			session, err := gateway.sessionFor(context.Background(), b)
			if err != nil {
				t.Fatal(err)
			}
			observer := &channelEmitter{server: s, kinds: map[string]protocol.ItemKind{}}
			session.RunTurn(context.Background(), "mode-check", "write the requested output", nil, nil, observer)
			_, err = os.Stat(output)
			if mode == "never" && !os.IsNotExist(err) {
				t.Fatal("unattended wrote a file requiring approval")
			}
			if mode == "bypass" && err != nil {
				t.Fatal("full access did not permit requested write", err)
			}
		})
	}
}

func TestChannelSearchUsesOnlyBuiltInTrustedServer(t *testing.T) {
	ctx := context.Background()
	s, _ := policyServer(t, "http://127.0.0.1:1/v1")
	servers, err := s.channelSearchServers(ctx)
	if err != nil || len(servers) != 0 {
		t.Fatal(servers, err)
	}
	_, err = s.search.Create(ctx, protocol.SearchEngineCreateParams{Provider: "tavily", APIKey: "fixture-secret", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	servers, err = s.channelSearchServers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	search, ok := servers["web_search"]
	if !ok || !search.Trusted || search.Args[0] != "mcp-search" || search.Command == "" || len(servers) != 1 {
		t.Fatalf("unexpected search config: %+v", servers)
	}
	encoded, _ := json.Marshal(servers)
	if strings.Contains(string(encoded), "fixture-secret") {
		t.Fatal("credential embedded in MCP config")
	}
}

func TestLegacyChannelWhitelistMigratesWithoutElevatingMode(t *testing.T) {
	ctx := context.Background()
	s, b := policyServer(t, "http://127.0.0.1:1/v1")
	legacy, err := agent.New(ctx, "c_legacy", protocol.SessionStartParams{Model: protocol.ModelConfig{ProviderID: b.ProviderID, Model: b.ModelName}, ApprovalPolicy: protocol.ApprovalNever, EnabledBuiltins: []string{"read_file"}}, s.keyFor)
	if err != nil {
		t.Fatal(err)
	}
	s.guard(legacy)
	s.sessions[legacy.ID] = legacy
	if err := legacy.Save(ctx, s.db); err != nil {
		t.Fatal(err)
	}
	b.ThreadUUID = legacy.ID
	if err := s.appDB.SaveChannelBinding(ctx, b); err != nil {
		t.Fatal(err)
	}
	gateway := &channelGateway{server: s}
	migrated, err := gateway.sessionFor(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if migrated.Config().ApprovalPolicy != protocol.ApprovalNever || !slices.Contains(migrated.Tools(), "write_file") {
		t.Fatal("legacy whitelist did not migrate safely")
	}
	same, err := gateway.sessionFor(ctx, b)
	if err != nil || same != migrated {
		t.Fatal("unchanged channel should reuse its session")
	}
}
