package model

import (
	"strings"
	"time"
)

type ProviderType string

type APIProtocol string

const (
	APIChatCompletions APIProtocol = "chat-completions"
	APIResponses       APIProtocol = "responses"
	APIAnthropic       APIProtocol = "anthropic-messages"
	APIGemini          APIProtocol = "gemini"
)

func (p APIProtocol) Normalized() APIProtocol {
	if p == "" {
		return APIChatCompletions
	}
	return p
}

func (p APIProtocol) Valid() bool {
	switch p.Normalized() {
	case APIChatCompletions, APIResponses, APIAnthropic, APIGemini:
		return true
	default:
		return false
	}
}

const (
	ProviderOpenAI       ProviderType = "openai"
	ProviderAzure        ProviderType = "azure"
	ProviderQwen         ProviderType = "qwen"
	ProviderKimi         ProviderType = "kimi"
	ProviderOpenRouter   ProviderType = "openrouter"
	ProviderOpenAICompat ProviderType = "openai-compatible"
	ProviderClaude       ProviderType = "claude"
	ProviderGemini       ProviderType = "gemini"
)

// ModelCapabilities uses explicit tri-state values. Empty means unknown, not unsupported.
type ModelCapabilities struct {
	APIProtocol      APIProtocol `json:"apiProtocol,omitempty"`
	ContextWindow    int         `json:"contextWindow,omitempty"`
	AudioInput       string      `json:"audioInput,omitempty"`
	Tools            string      `json:"tools,omitempty"`
	Vision           string      `json:"vision,omitempty"`
	Reasoning        string      `json:"reasoning,omitempty"`
	ReasoningEfforts string      `json:"reasoningEfforts,omitempty"`
}

// Normalized drops unrecognised catalog fields rather than inventing support.
func (c ModelCapabilities) Normalized() ModelCapabilities {
	for _, flag := range []*string{&c.Tools, &c.Vision, &c.AudioInput, &c.Reasoning} {
		if *flag != "supported" && *flag != "unsupported" {
			*flag = ""
		}
	}
	if c.APIProtocol != "" && !c.APIProtocol.Valid() {
		c.APIProtocol = ""
	}
	if c.ContextWindow < 0 || c.ContextWindow > 100_000_000 {
		c.ContextWindow = 0
	}
	efforts := []string{}
	seen := map[string]bool{}
	for _, value := range strings.Split(c.ReasoningEfforts, ",") {
		value = strings.TrimSpace(value)
		switch value {
		case "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra":
			if !seen[value] {
				efforts = append(efforts, value)
				seen[value] = true
			}
		}
	}
	c.ReasoningEfforts = strings.Join(efforts, ",")
	if c.Reasoning == "unsupported" {
		c.ReasoningEfforts = ""
	}
	return c
}

type Provider struct {
	// Monotonic configuration version. Catalog cache writes do not advance it.
	Revision            int64        `json:"-" gorm:"not null;default:1"`
	RoutingMode         string       `json:"routing_mode" gorm:"size:20;not null;default:auto"`
	ModelOverrides      JSON         `json:"model_overrides" gorm:"type:text"`
	ModelMetadata       JSON         `json:"model_metadata" gorm:"type:text"`
	CapabilityOverrides JSON         `json:"capability_overrides" gorm:"type:text"`
	ID                  int64        `json:"id" gorm:"primaryKey;autoIncrement"`
	Name                string       `json:"name" gorm:"size:100;not null"`
	Type                ProviderType `json:"type" gorm:"column:type;size:50;not null"`
	APIProtocol         APIProtocol  `json:"api_protocol" gorm:"size:40;not null;default:chat-completions"`
	BaseURL             string       `json:"base_url" gorm:"size:500;not null"`
	APIKey              string       `json:"api_key,omitempty" gorm:"size:500"`
	Models              JSON         `json:"models,omitzero" gorm:"type:text"`
	Enabled             bool         `json:"enabled" gorm:"not null;default:true"`
	CreatedAt           time.Time    `json:"created_at"`
	UpdatedAt           time.Time    `json:"updated_at"`
}

type CreateProviderReq struct {
	Name        string       `json:"name"`
	Type        ProviderType `json:"type"`
	APIProtocol APIProtocol  `json:"api_protocol"`
	BaseURL     string       `json:"base_url"`
	APIKey      string       `json:"api_key"`
	Models      JSON         `json:"models,omitzero"`
	Enabled     *bool        `json:"enabled,omitzero"`
}

type UpdateProviderReq struct {
	CapabilityOverrides JSON
	RoutingMode         *string
	ModelOverrides      JSON
	ModelMetadata       JSON
	Name                *string       `json:"name,omitzero"`
	Type                *ProviderType `json:"type,omitzero"`
	APIProtocol         *APIProtocol  `json:"api_protocol,omitzero"`
	BaseURL             *string       `json:"base_url,omitzero"`
	APIKey              *string       `json:"api_key,omitzero"`
	Models              JSON          `json:"models,omitzero"`
	Enabled             *bool         `json:"enabled,omitzero"`
}
