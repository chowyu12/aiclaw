package model

import "time"

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
	FastModelName     string     `json:"fast_model_name" gorm:"size:200"`
	Temperature       float64    `json:"temperature" gorm:"default:0.7"`
	MaxTokens         int        `json:"max_tokens" gorm:"default:4096"`
	Timeout           int        `json:"timeout" gorm:"default:0"`
	MaxHistory        int        `json:"max_history" gorm:"default:30"`
	MaxIterations     int        `json:"max_iterations" gorm:"default:50"`
	Token             string     `json:"token" gorm:"size:100;uniqueIndex"`
	TokenBudget       int        `json:"token_budget" gorm:"default:0"`
	EnableThinking    bool       `json:"enable_thinking" gorm:"default:true"`
	ReasoningEffort   string     `json:"reasoning_effort" gorm:"size:20;default:medium"`
	EnableWebSearch   bool       `json:"enable_web_search" gorm:"default:false"`
	ToolSearchEnabled bool       `json:"tool_search_enabled" gorm:"default:false"`
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

func (a *Agent) EffectiveReasoningEffort() string {
	switch a.ReasoningEffort {
	case "low", "medium", "high":
		return a.ReasoningEffort
	default:
		return "medium"
	}
}

type UpdateAgentReq struct {
	Name              *string      `json:"name,omitzero"`
	Description       *string      `json:"description,omitzero"`
	SystemPrompt      *string      `json:"system_prompt,omitzero"`
	ProviderID        *int64       `json:"provider_id,omitzero"`
	ModelName         *string      `json:"model_name,omitzero"`
	FastModelName     *string      `json:"fast_model_name,omitzero"`
	Temperature       *float64     `json:"temperature,omitzero"`
	MaxTokens         *int         `json:"max_tokens,omitzero"`
	Timeout           *int         `json:"timeout,omitzero"`
	MaxHistory        *int         `json:"max_history,omitzero"`
	MaxIterations     *int         `json:"max_iterations,omitzero"`
	TokenBudget       *int         `json:"token_budget,omitzero"`
	EnableThinking    *bool        `json:"enable_thinking,omitzero"`
	ReasoningEffort   *string      `json:"reasoning_effort,omitzero"`
	EnableWebSearch   *bool        `json:"enable_web_search,omitzero"`
	ToolSearchEnabled *bool        `json:"tool_search_enabled,omitzero"`
	ToolIDs           []int64      `json:"tool_ids,omitzero"`
	IsDefault         *bool        `json:"is_default,omitzero"`
}

type CreateAgentReq struct {
	Name              string      `json:"name"`
	Description       string      `json:"description"`
	SystemPrompt      string      `json:"system_prompt"`
	ProviderID        int64       `json:"provider_id"`
	ModelName         string      `json:"model_name"`
	FastModelName     string      `json:"fast_model_name"`
	Temperature       float64     `json:"temperature"`
	MaxTokens         int         `json:"max_tokens"`
	Timeout           int         `json:"timeout"`
	MaxHistory        int         `json:"max_history"`
	MaxIterations     int         `json:"max_iterations"`
	TokenBudget       int         `json:"token_budget"`
	EnableThinking    bool        `json:"enable_thinking"`
	ReasoningEffort   string      `json:"reasoning_effort"`
	EnableWebSearch   bool        `json:"enable_web_search"`
	ToolSearchEnabled bool        `json:"tool_search_enabled"`
	ToolIDs           []int64     `json:"tool_ids,omitzero"`
	IsDefault         bool        `json:"is_default"`
}
