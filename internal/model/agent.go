package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// MemOSConfig 存储 MemOS 连接配置（随 config.yaml 的 agent.memos_config 持久化）。
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

// Agent 运行时与 API 使用的单例配置（进程内权威副本，持久化到 config.yaml 的 agent 段；无数据库表）。
type Agent struct {
	ID                int64       `json:"id,omitempty"`
	UUID              string      `json:"uuid"`
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
	Token             string      `json:"token"`
	ToolSearchEnabled bool        `json:"tool_search_enabled"`
	MemOSEnabled      bool        `json:"memos_enabled"`
	MemOSCfg          MemOSConfig `json:"memos_config"`
	ToolIDs           []int64     `json:"tool_ids,omitempty"`

	Tools []Tool `json:"tools,omitempty"`
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
	ToolSearchEnabled *bool        `json:"tool_search_enabled,omitzero"`
	MemOSEnabled      *bool        `json:"memos_enabled,omitzero"`
	MemOSCfg          *MemOSConfig `json:"memos_config,omitzero"`
	ToolIDs           []int64      `json:"tool_ids,omitzero"`
}
