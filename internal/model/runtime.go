package model

import (
	"strings"
	"time"
)

const (
	RuntimeStatusOnline  = "online"
	RuntimeStatusOffline = "offline"

	RuntimeOfflineAfter = 45 * time.Second

	RuntimeAgentTypeCustom     = "custom"
	RuntimeAgentTypeCodex      = "codex"
	RuntimeAgentTypeCursor     = "cursor"
	RuntimeAgentTypeClaudeCode = "claude-code"
	RuntimeAgentTypeCodeBuddy  = "codebuddy"
	RuntimeAgentTypeOpenClaw   = "openclaw"
	RuntimeAgentTypeHermes     = "hermes"

	RuntimePromptStdin    = "stdin"
	RuntimePromptArgument = "argument"
)

// Runtime is a user-owned compute environment. The runtime client connects
// outward to AiClaw, claims queued local-agent runs, and executes Command with
// Args directly (never through a shell).
type Runtime struct {
	ID          int64       `json:"id" gorm:"primaryKey;autoIncrement"`
	UUID        string      `json:"uuid" gorm:"uniqueIndex;size:36;not null"`
	Name        string      `json:"name" gorm:"size:200;not null"`
	Description string      `json:"description" gorm:"size:500"`
	AgentType   string      `json:"agent_type" gorm:"size:30;default:custom;index"`
	Command     string      `json:"command" gorm:"size:500;not null"`
	Args        StringSlice `json:"args" gorm:"type:text"`
	PromptMode  string      `json:"prompt_mode" gorm:"size:20;default:stdin"`
	Token       string      `json:"token" gorm:"size:100;uniqueIndex;not null"`
	Status      string      `json:"status" gorm:"size:20;default:offline;index"`
	Version     string      `json:"version" gorm:"size:100"`
	LastSeenAt  *time.Time  `json:"last_seen_at,omitzero"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

func NormalizeRuntimeAgentType(agentType string) (string, bool) {
	switch strings.TrimSpace(strings.ToLower(agentType)) {
	case "", RuntimeAgentTypeCustom:
		return RuntimeAgentTypeCustom, true
	case RuntimeAgentTypeCodex, RuntimeAgentTypeCursor, RuntimeAgentTypeClaudeCode,
		RuntimeAgentTypeCodeBuddy, RuntimeAgentTypeOpenClaw, RuntimeAgentTypeHermes:
		return strings.TrimSpace(strings.ToLower(agentType)), true
	default:
		return "", false
	}
}

func NormalizeRuntimePromptMode(promptMode string) (string, bool) {
	switch strings.TrimSpace(strings.ToLower(promptMode)) {
	case "", RuntimePromptStdin:
		return RuntimePromptStdin, true
	case RuntimePromptArgument:
		return RuntimePromptArgument, true
	default:
		return "", false
	}
}

func RuntimeAgentPromptMode(agentType string) string {
	normalized, ok := NormalizeRuntimeAgentType(agentType)
	if !ok || normalized == RuntimeAgentTypeCustom || normalized == RuntimeAgentTypeCodex {
		return RuntimePromptStdin
	}
	return RuntimePromptArgument
}

func (r *Runtime) EffectiveAgentType() string {
	if r == nil {
		return RuntimeAgentTypeCustom
	}
	if agentType, ok := NormalizeRuntimeAgentType(r.AgentType); ok {
		return agentType
	}
	return RuntimeAgentTypeCustom
}

func (r *Runtime) EffectivePromptMode() string {
	if r == nil {
		return RuntimePromptStdin
	}
	if agentType := r.EffectiveAgentType(); agentType != RuntimeAgentTypeCustom {
		return RuntimeAgentPromptMode(agentType)
	}
	if promptMode, ok := NormalizeRuntimePromptMode(r.PromptMode); ok {
		return promptMode
	}
	return RuntimePromptStdin
}

func (r *Runtime) RefreshStatus(now time.Time) {
	if r == nil || r.LastSeenAt == nil || now.Sub(*r.LastSeenAt) > RuntimeOfflineAfter {
		if r != nil {
			r.Status = RuntimeStatusOffline
		}
		return
	}
	r.Status = RuntimeStatusOnline
}

func (r *Runtime) IsOnline(now time.Time) bool {
	if r == nil {
		return false
	}
	r.RefreshStatus(now)
	return r.Status == RuntimeStatusOnline
}

type CreateRuntimeReq struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	AgentType   string   `json:"agent_type"`
	Command     string   `json:"command"`
	Args        []string `json:"args"`
	PromptMode  string   `json:"prompt_mode"`
}

type UpdateRuntimeReq struct {
	Name        *string   `json:"name,omitzero"`
	Description *string   `json:"description,omitzero"`
	AgentType   *string   `json:"agent_type,omitzero"`
	Command     *string   `json:"command,omitzero"`
	Args        *[]string `json:"args,omitzero"`
	PromptMode  *string   `json:"prompt_mode,omitzero"`
}

type RuntimeTaskMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type RuntimeTask struct {
	RunID          string               `json:"run_id"`
	ConversationID string               `json:"conversation_id"`
	AgentName      string               `json:"agent_name"`
	SystemPrompt   string               `json:"system_prompt,omitzero"`
	Messages       []RuntimeTaskMessage `json:"messages"`
	Command        string               `json:"command"`
	Args           []string             `json:"args"`
	PromptMode     string               `json:"prompt_mode"`
	WorkingDir     string               `json:"working_dir,omitzero"`
	TimeoutSeconds int                  `json:"timeout_seconds,omitzero"`
}

type RuntimeRunEvent struct {
	Delta string `json:"delta"`
}

type RuntimeRunComplete struct {
	Content string `json:"content"`
	Error   string `json:"error,omitzero"`
}
