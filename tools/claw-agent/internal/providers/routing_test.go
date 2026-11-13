package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/appdb"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

func TestRoutingUsesServiceContractBeforeModelVendor(t *testing.T) {
	for _, tc := range []struct {
		kind      model.ProviderType
		url, name string
		want      model.APIProtocol
		profile   string
	}{
		{model.ProviderOpenRouter, "https://openrouter.ai/api/v1", "openai/gpt-6-astra", model.APIChatCompletions, "openrouter"},
		{model.ProviderOpenAICompat, "https://resource.openai.azure.com/openai/v1", "gpt-6-astra", model.APIResponses, "azure"},
		{model.ProviderOpenAICompat, "https://resource.openai.azure.com/openai/v1", "claude-opus", model.APIChatCompletions, "azure"},
		{model.ProviderOpenAICompat, "https://resource.openai.azure.com.evil.invalid/v1", "gpt-6-astra", model.APIChatCompletions, "openai-compatible"},
		{model.ProviderOpenAICompat, "https://custom.invalid/v1", "gpt-6-astra", model.APIChatCompletions, "openai-compatible"},
		{model.ProviderOpenAI, "https://api.openai.com/v1", "deployment-alias", model.APIChatCompletions, "openai"},
		{model.ProviderClaude, "", "fixture", model.APIAnthropic, "claude"},
		{model.ProviderGemini, "", "fixture", model.APIGemini, "gemini"},
	} {
		p := &model.Provider{Type: tc.kind, BaseURL: tc.url}
		route := resolveRoute(p, tc.name)
		if route.APIProtocol != tc.want || serviceProfile(p) != tc.profile {
			t.Fatalf("%s %s => %+v", tc.url, tc.name, route)
		}
	}
	p := &model.Provider{Type: model.ProviderAzure, APIProtocol: model.APIAnthropic, RoutingMode: "manual", ModelOverrides: model.JSON(`{"gpt-6-astra":"gemini"}`)}
	if resolveRoute(p, "gpt-6-astra").APIProtocol != model.APIGemini || resolveRoute(p, "other").APIProtocol != model.APIAnthropic {
		t.Fatal("override precedence lost")
	}
	p.RoutingMode = "auto"
	p.ModelOverrides = nil
	route := resolveRoute(p, "gpt-6-astra")
	if route.SourceURL == "" || route.Capabilities.ReasoningEfforts != "low,medium,high" {
		t.Fatal("known rule lacks provenance/constraints", route)
	}
	if resolveRoute(p, "unknown").Capabilities != (model.ModelCapabilities{}) {
		t.Fatal("unknown model capabilities were invented")
	}
}

func TestOpenRouterMetadataStaysScopedAndPreservesManualModels(t *testing.T) {
	ctx := context.Background()
	body := `{"data":[{"id":"openai/gpt-6-astra","supported_parameters":["tools","reasoning"],"architecture":{"input_modalities":["text","image","audio"]}},{"id":"text-only","supported_parameters":[],"architecture":{"input_modalities":["text"]}},{"id":"unknown"}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
	defer srv.Close()
	db, err := appdb.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := New(db)
	p, err := store.Create(ctx, protocol.ProviderCreateParams{Name: "gateway", Type: "openrouter", BaseURL: srv.URL, APIKey: "fixture", Models: []string{"manual"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.FetchModels(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	cfg := protocol.ModelConfig{ProviderID: p.ID, Model: "openai/gpt-6-astra"}
	_, err = store.Resolve(ctx, &cfg)
	if err != nil || cfg.APIProtocol != model.APIChatCompletions || cfg.Capabilities.Tools != "supported" || cfg.Capabilities.Vision != "supported" || cfg.Capabilities.AudioInput != "supported" {
		t.Fatal(cfg, err)
	}
	cfg.Model = "text-only"
	_, _ = store.Resolve(ctx, &cfg)
	if cfg.Capabilities.Tools != "unsupported" || cfg.Capabilities.Vision != "unsupported" || cfg.Capabilities.AudioInput != "unsupported" {
		t.Fatal("explicit absence not preserved", cfg)
	}
	cfg.Model = "unknown"
	_, _ = store.Resolve(ctx, &cfg)
	if cfg.Capabilities != (model.ModelCapabilities{}) {
		t.Fatal("missing fields became false", cfg)
	}
	record, _ := db.GetProvider(ctx, p.ID)
	var names []string
	_ = json.Unmarshal(record.Models, &names)
	if len(names) != 1 || names[0] != "manual" {
		t.Fatal("metadata fetch replaced user's model list")
	}
	next := srv.URL + "/new"
	_, err = store.Update(ctx, protocol.ProviderUpdateParams{ID: p.ID, BaseURL: &next})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Model = "text-only"
	_, _ = store.Resolve(ctx, &cfg)
	if cfg.Capabilities.Tools != "" {
		t.Fatal("metadata from old endpoint was reused")
	}
}
