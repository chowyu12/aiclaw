package model

import "time"

type ProviderType string

const (
	ProviderOpenAI     ProviderType = "openai"
	ProviderQwen       ProviderType = "qwen"
	ProviderKimi       ProviderType = "kimi"
	ProviderOpenRouter ProviderType = "openrouter"
	ProviderOpenAICompat ProviderType = "openai-compatible"
	ProviderClaude     ProviderType = "claude"
	ProviderGemini     ProviderType = "gemini"
)

type Provider struct {
	ID        int64        `json:"id" gorm:"primaryKey;autoIncrement"`
	Name      string       `json:"name" gorm:"size:100;not null"`
	Type      ProviderType `json:"type" gorm:"column:type;size:50;not null"`
	BaseURL   string       `json:"base_url" gorm:"size:500;not null"`
	APIKey    string       `json:"api_key,omitempty" gorm:"size:500"`
	Models    JSON         `json:"models,omitzero" gorm:"type:text"`
	Enabled   bool         `json:"enabled" gorm:"not null;default:true"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
}

type CreateProviderReq struct {
	Name    string       `json:"name"`
	Type    ProviderType `json:"type"`
	BaseURL string       `json:"base_url"`
	APIKey  string       `json:"api_key"`
	Models  JSON         `json:"models,omitzero"`
	Enabled *bool        `json:"enabled,omitzero"`
}

type UpdateProviderReq struct {
	Name    *string       `json:"name,omitzero"`
	Type    *ProviderType `json:"type,omitzero"`
	BaseURL *string       `json:"base_url,omitzero"`
	APIKey  *string       `json:"api_key,omitzero"`
	Models  JSON          `json:"models,omitzero"`
	Enabled *bool         `json:"enabled,omitzero"`
}
