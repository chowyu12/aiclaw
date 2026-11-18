package providers

import (
	"encoding/json"
	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"testing"
	"time"
)

func TestCapabilityAuthorityIsPerFieldAndScoped(t *testing.T) {
	p := &model.Provider{Type: model.ProviderOpenAI, BaseURL: "https://api.openai.com/v1", APIKey: "fixture", UpdatedAt: time.Now()}
	metadata := catalogMetadata{Profile: "openai", BaseURL: p.BaseURL, Identity: catalogIdentity(p), FetchedAt: 123, SupplementalAt: 122,
		Models:       map[string]model.ModelCapabilities{"gpt-6.2-sol": {Vision: "unsupported"}},
		Supplemental: map[string]model.ModelCapabilities{"gpt-6.2-sol": {Tools: "unsupported", Vision: "supported", ContextWindow: 1}}}
	p.ModelMetadata, _ = json.Marshal(metadata)
	route := resolveRoute(p, "gpt-6.2-sol")
	if route.APIProtocol != model.APIResponses || route.Capabilities.ContextWindow != 922000 || route.Capabilities.Tools != "supported" || route.Capabilities.Vision != "unsupported" {
		t.Fatalf("precedence: %+v", route)
	}
	if route.Evidence["vision"].Source != "endpoint" || route.Evidence["vision"].UpdatedAt != 123 || route.Evidence["tools"].Source != "builtin:2026-11-18:openai" {
		t.Fatal(route.Evidence)
	}
	p.CapabilityOverrides = model.JSON(`{"gpt-6.2-sol":{"vision":"supported","reasoningEfforts":"high,ultra"}}`)
	route = resolveRoute(p, "gpt-6.2-sol")
	if route.Capabilities.Vision != "supported" || route.Evidence["vision"].Source != "user" || route.Capabilities.ReasoningEfforts != "high,ultra" {
		t.Fatal(route)
	}
	p.CapabilityOverrides = nil
	p.APIKey = "different-fixture"
	if got := resolveRoute(p, "gpt-6.2-sol"); got.Capabilities.Vision != "supported" {
		t.Fatal("credential-scoped cache leaked", got)
	}
	p.Type = model.ProviderOpenRouter
	if got := resolveRoute(p, "gpt-6.2-sol"); got.APIProtocol != model.APIChatCompletions || got.Capabilities != (model.ModelCapabilities{}) {
		t.Fatal("underlying vendor contract leaked", got)
	}
}

func TestCapabilityOverrideValidationAndNormalization(t *testing.T) {
	for _, cap := range []model.ModelCapabilities{{Tools: "yes"}, {ContextWindow: -1}, {APIProtocol: "arbitrary"}, {ReasoningEfforts: "extreme"}} {
		if _, err := validateCapabilityOverrides(map[string]model.ModelCapabilities{"m": cap}); err == nil {
			t.Fatal("accepted invalid", cap)
		}
	}
	cap := model.ModelCapabilities{Tools: "yes", Vision: "unsupported", ReasoningEfforts: " ultra,invalid,ultra,high ", ContextWindow: -1}.Normalized()
	if cap.Tools != "" || cap.Vision != "unsupported" || cap.ContextWindow != 0 || cap.ReasoningEfforts != "ultra,high" {
		t.Fatal(cap)
	}
}

func TestContextWindowOverrideUpdatesExistingSnapshotAndRestoresHostLimit(t *testing.T) {
	p := &model.Provider{Type: model.ProviderOpenAI, Enabled: true, APIKey: "fixture"}
	cfg := protocol.ModelConfig{Model: "gpt-6.2-sol", ContextWindow: 131072}
	for _, tc := range []struct {
		override string
		want     int
	}{
		{`{"gpt-6.2-sol":{"contextWindow":8192}}`, 8192},
		{`{"gpt-6.2-sol":{"contextWindow":4096}}`, 4096},
		{`{}`, 131072},
	} {
		p.CapabilityOverrides = model.JSON(tc.override)
		if _, err := resolveProvider(p, &cfg); err != nil || cfg.ContextWindow != tc.want {
			t.Fatalf("override %s: window=%d, err=%v", tc.override, cfg.ContextWindow, err)
		}
		// Resume from the persisted model snapshot on the next iteration.
		raw, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		cfg = protocol.ModelConfig{}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatal(err)
		}
	}
}
