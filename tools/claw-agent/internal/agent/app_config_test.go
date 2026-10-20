package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

type configurationEmitter struct {
	recordingEmitter
	requests []protocol.AppConfigRequest
}

func (e *configurationEmitter) RequestAppConfig(ctx context.Context, request protocol.AppConfigRequest) (protocol.AppConfigResult, error) {
	e.requests = append(e.requests, request)
	return protocol.AppConfigResult{Text: "User saved configuration using the desktop card."}, nil
}
func configurationSession(t *testing.T, model *fakeModel, enabled bool, parent string) *Session {
	t.Helper()
	endpoint := httptest.NewServer(http.HandlerFunc(model.handler))
	t.Cleanup(endpoint.Close)
	s, err := New(context.Background(), "desktop", protocol.SessionStartParams{
		Model: protocol.ModelConfig{BaseURL: endpoint.URL, Model: "fake"}, Workdir: t.TempDir(),
		ApprovalPolicy: protocol.ApprovalBypass, EnableAppConfig: enabled, ParentID: parent,
	}, StaticKey("sk-test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}
func TestConfigurationToolsAreOptInAndAbsentForChildren(t *testing.T) {
	for _, tc := range []struct {
		enabled bool
		parent  string
		want    bool
	}{{false, "", false}, {true, "", true}, {true, "parent", false}} {
		s := configurationSession(t, &fakeModel{}, tc.enabled, tc.parent)
		for _, name := range []string{"app_config_list", "app_configure"} {
			_, ok := s.registry.Get(name)
			if ok != tc.want {
				t.Fatalf("%s enabled=%v parent=%s got=%v", name, tc.enabled, tc.parent, ok)
			}
		}
	}
}
func TestConfigurationUsesHostCardEvenUnderBypass(t *testing.T) {
	model := &fakeModel{script: []string{
		sseToolCalls([3]string{"c1", "app_configure", `{"sessionId":"forged","action":"list","category":"search","operation":"save","values":{"provider":"tavily"}}`}), sseText("Done"),
	}}
	s := configurationSession(t, model, true, "")
	e := &configurationEmitter{}
	s.RunTurn(context.Background(), "turn", "Configure search", nil, nil, e)
	if len(e.requests) != 1 || e.requests[0].Action != "configure" || e.requests[0].SessionID != "desktop" {
		t.Fatalf("bad request: %+v", e.requests)
	}
	if !e.find(protocol.NotifyItemCompleted, "User saved configuration") {
		t.Fatal("host result missing")
	}
	if len(e.approvals) != 0 {
		t.Fatal("writes are authorized by the host card, not session bypass")
	}
}
func TestConfigurationFailsWithoutInteractiveHost(t *testing.T) {
	model := &fakeModel{script: []string{sseToolCalls([3]string{"c1", "app_config_list", `{"category":"email"}`}), sseText("Unavailable")}}
	s := configurationSession(t, model, true, "")
	e := &recordingEmitter{}
	s.RunTurn(context.Background(), "turn", "Configure email", nil, nil, e)
	if !e.find(protocol.NotifyItemCompleted, "interactive desktop host") {
		t.Fatal("missing safe host requirement")
	}
}
func TestConfigurationRefreshFlagIsPreserved(t *testing.T) {
	s := configurationSession(t, &fakeModel{}, true, "")
	if !refreshOf(s.Config()).EnableAppConfig {
		t.Fatal("configuration capability missing from refresh")
	}
}
