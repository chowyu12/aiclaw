package providers

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

// Strategies describe the public service contract, not the model's original vendor.
// Unknown compatible gateways retain Chat Completions and can be overridden explicitly.
func serviceProfile(p *model.Provider) string {
	if p.Type != model.ProviderOpenAICompat {
		return string(p.Type)
	}
	u, err := url.Parse(p.BaseURL)
	if err != nil {
		return "openai-compatible"
	}
	host := strings.ToLower(u.Hostname())
	switch {
	case host == "openrouter.ai":
		return "openrouter"
	case host == "api.openai.com":
		return "openai"
	case strings.HasSuffix(host, ".openai.azure.com"):
		return "azure"
	case host == "api.anthropic.com":
		return "claude"
	case host == "generativelanguage.googleapis.com" && !strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/openai"):
		return "gemini"
	default:
		return "openai-compatible"
	}
}

// Versioned, exact IDs only. Deployment aliases and unrecognised models remain unknown.
// Sources verified 2026-11-02. These rules apply only to OpenAI/Azure direct service profiles.
var responsesModels = map[string]bool{
	"gpt-6-astra": true, "gpt-6-astra-2026-09-03": true,
	"gpt-6.1-sol": true, "gpt-6.1-sol-2026-09-29": true,
	"gpt-6.1-terra": true, "gpt-6.1-terra-2026-10-26": true,
	"gpt-6-sol": true, "gpt-6-sol-2026-09-22": true,
	"gpt-6-luna": true, "gpt-6-luna-2026-09-22": true,
	"gpt-5.6-sol": true, "gpt-5.6-terra": true, "gpt-5.6-luna": true,
}

func modelOverrides(p *model.Provider) map[string]model.APIProtocol {
	result := map[string]model.APIProtocol{}
	_ = json.Unmarshal(p.ModelOverrides, &result)
	return result
}

func validateOverrides(items map[string]model.APIProtocol) (model.JSON, error) {
	clean := map[string]model.APIProtocol{}
	for name, api := range items {
		if strings.TrimSpace(name) == "" || name != strings.TrimSpace(name) || api == "" || !api.Valid() {
			return nil, fmt.Errorf("invalid model protocol override: %s", name)
		}
		clean[name] = api
	}
	raw, err := json.Marshal(clean)
	return model.JSON(raw), err
}

type routeResolver struct {
	provider  *model.Provider
	profile   string
	metadata  map[string]model.ModelCapabilities
	overrides map[string]model.APIProtocol
	visions   map[string]bool
	audios    map[string]bool
	names     []string
}

func compileRoutes(p *model.Provider) *routeResolver {
	r := &routeResolver{provider: p, profile: serviceProfile(p), overrides: modelOverrides(p), visions: map[string]bool{}, audios: map[string]bool{}}
	var metadata catalogMetadata
	if json.Unmarshal(p.ModelMetadata, &metadata) == nil && metadata.Profile == r.profile && metadata.BaseURL == p.BaseURL {
		r.metadata = metadata.Models
	}
	for _, entry := range decodeModels(p.Models) {
		parsed := protocol.ParseModelMark(entry)
		r.names = append(r.names, parsed.Name)
		for _, role := range parsed.Roles {
			if role == protocol.RoleSTT {
				r.audios[parsed.Name] = true
			}
			if role == protocol.RoleVision {
				r.visions[parsed.Name] = true
			}
		}
	}
	return r
}

func resolveRoute(p *model.Provider, name string) protocol.ModelRouteView {
	return compileRoutes(p).resolve(name)
}

func (r *routeResolver) resolve(name string) protocol.ModelRouteView {
	p, profile := r.provider, r.profile
	route := protocol.ModelRouteView{APIProtocol: model.APIChatCompletions, Source: "service:" + profile}
	switch profile {
	case "claude":
		route.APIProtocol = model.APIAnthropic
	case "gemini":
		route.APIProtocol = model.APIGemini
	case "openai", "azure":
		if responsesModels[name] {
			route.APIProtocol = model.APIResponses
			route.Source = "builtin:2026-11-02:" + profile
			route.SourceURL = "https://developers.openai.com/api/docs/guides/latest-model"
			if profile == "azure" {
				route.SourceURL = "https://learn.microsoft.com/azure/foundry/openai/how-to/reasoning"
			}
			route.Capabilities.Tools = "supported"
			route.Capabilities.Reasoning = "supported"
			if name == "gpt-6-astra" || name == "gpt-6-astra-2026-09-03" || name == "gpt-6.1-sol" || name == "gpt-6.1-sol-2026-09-29" {
				route.Capabilities.ReasoningEfforts = "low,medium,high"
			}
		}
	}
	if (profile == "openai" || profile == "azure") && (name == "gpt-audio" || name == "gpt-audio-mini" || name == "gpt-4o-audio-preview") {
		route.Capabilities.AudioInput = "supported"
	}
	if profile == "gemini" && r.audios[name] {
		route.Capabilities.AudioInput = "supported"
	}
	// A gateway's own metadata takes precedence over built-in capability descriptions.
	if caps, ok := r.metadata[name]; ok {
		route.Capabilities = caps
	}
	if r.visions[name] && route.Capabilities.Vision == "" {
		route.Capabilities.Vision = "supported"
	}
	if p.RoutingMode == "manual" {
		route.APIProtocol = p.APIProtocol.Normalized()
		route.Source = "service-override"
		route.SourceURL = ""
	}
	if api, ok := r.overrides[name]; ok {
		route.APIProtocol = api
		route.Source = "model-override"
		route.SourceURL = ""
	}
	return route
}

type catalogMetadata struct {
	Profile string                             `json:"profile"`
	BaseURL string                             `json:"baseUrl"`
	Models  map[string]model.ModelCapabilities `json:"models"`
}

func modelRoutes(p *model.Provider) map[string]protocol.ModelRouteView {
	result := map[string]protocol.ModelRouteView{}
	resolver := compileRoutes(p)
	for _, name := range resolver.names {
		result[name] = resolver.resolve(name)
	}
	return result
}
