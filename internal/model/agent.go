package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// MemOSConfig 存储 MemOS 连接配置。
type MemOSConfig struct {
	BaseURL string `json:"base_url,omitempty" yaml:"base_url,omitempty"`
	APIKey  string `json:"api_key,omitempty" yaml:"api_key,omitempty"`
	UserID  string `json:"user_id,omitempty" yaml:"user_id,omitempty"`
	TopK    int    `json:"top_k,omitempty" yaml:"top_k,omitempty"`
	Async   bool   `json:"async,omitempty" yaml:"async,omitempty"`
}

func (c MemOSConfig) EffectiveTopK() int {
	if c.TopK > 0 {
		return c.TopK
	}
	return 10
}

func (c MemOSConfig) EffectiveUserID() string {
	if c.UserID != "" {
		return c.UserID
	}
	return "aiclaw-user"
}

func (c MemOSConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

func (c *MemOSConfig) Scan(src any) error {
	if src == nil {
		return nil
	}
	var data []byte
	switch v := src.(type) {
	case []byte:
		data = v
	case string:
		data = []byte(v)
	default:
		return fmt.Errorf("unsupported type for MemOSConfig: %T", src)
	}
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, c)
}

// Agent 运行时配置，持久化在数据库 agents 表；支持多 Agent。
type Agent struct {
	ID                int64      `json:"id" gorm:"primaryKey;autoIncrement"`
	UUID              string     `json:"uuid" gorm:"uniqueIndex;size:36;not null"`
	IsDefault         bool       `json:"is_default" gorm:"default:false;index"`
	Name              string     `json:"name" gorm:"size:200;not null"`
	Description       string     `json:"description" gorm:"size:500"`
	SystemPrompt      string     `json:"system_prompt" gorm:"type:text"`
	ProviderID        int64      `json:"provider_id" gorm:"default:0"`
	ModelName         string     `json:"model_name" gorm:"size:200"`
	Temperature       float64    `json:"temperature" gorm:"default:0.7"`
	MaxTokens         int        `json:"max_tokens" gorm:"default:4096"`
	Timeout           int        `json:"timeout" gorm:"default:0"`
	MaxHistory        int        `json:"max_history" gorm:"default:30"`
	MaxIterations     int        `json:"max_iterations" gorm:"default:50"`
	Token             string     `json:"token" gorm:"size:100;uniqueIndex"`
	TokenBudget       int        `json:"token_budget" gorm:"default:0"`
	DisableThinking   bool       `json:"disable_thinking" gorm:"default:false"`
	ToolSearchEnabled bool       `json:"tool_search_enabled" gorm:"default:false"`
	MemOSEnabled      bool        `json:"memos_enabled" gorm:"column:memos_enabled;default:false"`
	MemOSCfg          MemOSConfig `json:"memos_config" gorm:"column:memos_config;type:text"`
	ToolIDs           Int64Slice `json:"tool_ids,omitempty" gorm:"type:text"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`

	// 非数据库字段，仅用于 API 响应时附带已解析的工具列表
	Tools []Tool `json:"tools,omitempty" gorm:"-"`
}

const (
	DefaultAgentMaxHistory    = 30
	DefaultAgentMaxIterations = 50
)

func (a *Agent) TimeoutSeconds() int {
	return a.Timeout
}

func (a *Agent) HistoryLimit() int {
	if a.MaxHistory > 0 {
		return a.MaxHistory
	}
	return DefaultAgentMaxHistory
}

func (a *Agent) IterationLimit() int {
	if a.MaxIterations > 0 {
		return a.MaxIterations
	}
	return DefaultAgentMaxIterations
}

type UpdateAgentReq struct {
	Name              *string      `json:"name,omitzero"`
	Description       *string      `json:"description,omitzero"`
	SystemPrompt      *string      `json:"system_prompt,omitzero"`
	ProviderID        *int64       `json:"provider_id,omitzero"`
	ModelName         *string      `json:"model_name,omitzero"`
	Temperature       *float64     `json:"temperature,omitzero"`
	MaxTokens         *int         `json:"max_tokens,omitzero"`
	Timeout           *int         `json:"timeout,omitzero"`
	MaxHistory        *int         `json:"max_history,omitzero"`
	MaxIterations     *int         `json:"max_iterations,omitzero"`
	TokenBudget       *int         `json:"token_budget,omitzero"`
	DisableThinking   *bool        `json:"disable_thinking,omitzero"`
	ToolSearchEnabled *bool        `json:"tool_search_enabled,omitzero"`
	MemOSEnabled      *bool        `json:"memos_enabled,omitzero"`
	MemOSCfg          *MemOSConfig `json:"memos_config,omitzero"`
	ToolIDs           []int64      `json:"tool_ids,omitzero"`
	IsDefault         *bool        `json:"is_default,omitzero"`
}

type CreateAgentReq struct {
	Name              string      `json:"name"`
	Description       string      `json:"description"`
	SystemPrompt      string      `json:"system_prompt"`
	ProviderID        int64       `json:"provider_id"`
	ModelName         string      `json:"model_name"`
	Temperature       float64     `json:"temperature"`
	MaxTokens         int         `json:"max_tokens"`
	Timeout           int         `json:"timeout"`
	MaxHistory        int         `json:"max_history"`
	MaxIterations     int         `json:"max_iterations"`
	TokenBudget       int         `json:"token_budget"`
	DisableThinking   bool        `json:"disable_thinking"`
	ToolSearchEnabled bool        `json:"tool_search_enabled"`
	MemOSEnabled      bool        `json:"memos_enabled"`
	MemOSCfg          MemOSConfig `json:"memos_config"`
	ToolIDs           []int64     `json:"tool_ids,omitzero"`
	IsDefault         bool        `json:"is_default"`
}
