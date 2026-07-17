package model

import (
	"strings"
	"time"
)

const (
	RuntimeStatusOnline  = "online"
	RuntimeStatusOffline = "offline"

	BuiltinLocalRuntimeName        = "本机"
	BuiltinLocalRuntimeDescription = "AiClaw 所在机器，启动时自动发现本地智能体 CLI。"

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
	ID          int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	UUID        string `json:"uuid" gorm:"uniqueIndex;size:36;not null"`
	Name        string `json:"name" gorm:"size:200;not null"`
	Description string `json:"description" gorm:"size:500"`
	// Builtin marks the runtime embedded in the AiClaw server process. It is
	// created and refreshed automatically at startup and needs no connection.
	Builtin bool `json:"builtin" gorm:"default:false;index"`
	// DetectedAgents is reported by the connected local daemon. It is the
	// authoritative list for newly configured local Agents.
	DetectedAgents StringSlice `json:"detected_agents" gorm:"type:text"`
	// AgentConfigs stores the user-managed settings for currently detected
	// local CLIs. It is populated for API responses, not persisted on Runtime.
	AgentConfigs []RuntimeAgentConfig `json:"agent_configs,omitempty" gorm:"-"`
	// AgentType/Command/Args/PromptMode are retained for manually configured
	// legacy runtimes. New runtimes are daemon-hosts and use DetectedAgents.
	AgentType  string      `json:"agent_type" gorm:"size:30;default:custom;index"`
	Command    string      `json:"command" gorm:"size:500;not null"`
	Args       StringSlice `json:"args" gorm:"type:text"`
	PromptMode string      `json:"prompt_mode" gorm:"size:20;default:stdin"`
	Token      string      `json:"token" gorm:"size:100;uniqueIndex;not null"`
	Status     string      `json:"status" gorm:"size:20;default:offline;index"`
	Version    string      `json:"version" gorm:"size:100"`
	LastSeenAt *time.Time  `json:"last_seen_at,omitzero"`
	CreatedAt  time.Time   `json:"created_at"`
	UpdatedAt  time.Time   `json:"updated_at"`
}

// RuntimeAgentConfig is the per-runtime configuration for one auto-detected
// agent CLI. The model is applied to new tasks for that CLI; empty means the
// CLI's own default model.
type RuntimeAgentConfig struct {
	ID        int64     `json:"id" gorm:"primaryKey;autoIncrement"`
	RuntimeID int64     `json:"runtime_id" gorm:"uniqueIndex:idx_runtime_agent_config;index"`
	AgentType string    `json:"agent_type" gorm:"size:30;uniqueIndex:idx_runtime_agent_config"`
	Enabled   bool      `json:"enabled" gorm:"default:true"`
	ModelName string    `json:"model_name" gorm:"size:200"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type UpdateRuntimeAgentConfigReq struct {
	Enabled   *bool   `json:"enabled,omitzero"`
	ModelName *string `json:"model_name,omitzero"`
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

type LocalCLISpec struct {
	AgentType  string
	Command    string
	Args       []string
	PromptMode string
	ModelFlag  string
}

func LocalCLISpecFor(agentType string) (LocalCLISpec, bool) {
	normalized, ok := NormalizeRuntimeAgentType(agentType)
	if !ok || normalized == RuntimeAgentTypeCustom {
		return LocalCLISpec{}, false
	}
	specs := map[string]LocalCLISpec{
		RuntimeAgentTypeCodex:      {AgentType: RuntimeAgentTypeCodex, Command: "codex", Args: []string{"exec", "-"}, PromptMode: RuntimePromptStdin, ModelFlag: "-m"},
		RuntimeAgentTypeCursor:     {AgentType: RuntimeAgentTypeCursor, Command: "cursor-agent", Args: []string{"-p", "--output-format", "text"}, PromptMode: RuntimePromptArgument, ModelFlag: "--model"},
		RuntimeAgentTypeClaudeCode: {AgentType: RuntimeAgentTypeClaudeCode, Command: "claude", Args: []string{"-p", "--output-format", "text"}, PromptMode: RuntimePromptArgument, ModelFlag: "--model"},
		RuntimeAgentTypeCodeBuddy:  {AgentType: RuntimeAgentTypeCodeBuddy, Command: "codebuddy", Args: []string{"-p", "--output-format", "text"}, PromptMode: RuntimePromptArgument, ModelFlag: "--model"},
		RuntimeAgentTypeOpenClaw:   {AgentType: RuntimeAgentTypeOpenClaw, Command: "openclaw", Args: []string{"agent", "--local", "--message"}, PromptMode: RuntimePromptArgument},
		RuntimeAgentTypeHermes:     {AgentType: RuntimeAgentTypeHermes, Command: "hermes", Args: []string{"chat", "-q"}, PromptMode: RuntimePromptArgument, ModelFlag: "--model"},
	}
	spec, ok := specs[normalized]
	return spec, ok
}

func (s LocalCLISpec) SupportsModel() bool {
	return s.ModelFlag != "" || s.AgentType == RuntimeAgentTypeOpenClaw
}

func (s LocalCLISpec) ArgsWithModel(modelName string) []string {
	args := append([]string(nil), s.Args...)
	modelName = strings.TrimSpace(modelName)
	if !s.SupportsModel() || modelName == "" {
		return args
	}
	if s.AgentType == RuntimeAgentTypeOpenClaw {
		return append(args, "--agent", modelName)
	}
	if s.AgentType == RuntimeAgentTypeCodex && len(args) >= 2 {
		return append([]string{args[0], s.ModelFlag, modelName}, args[1:]...)
	}
	return append(args, s.ModelFlag, modelName)
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

func (r *Runtime) HasDetectedAgent(agentType string) bool {
	normalized, ok := NormalizeRuntimeAgentType(agentType)
	if !ok || normalized == RuntimeAgentTypeCustom {
		return false
	}
	for _, candidate := range r.DetectedAgents {
		if candidateType, ok := NormalizeRuntimeAgentType(candidate); ok && candidateType == normalized {
			return true
		}
	}
	// Backward compatibility for runtimes created before local discovery.
	return r.EffectiveAgentType() == normalized
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
	AgentType      string               `json:"agent_type"`
	ModelName      string               `json:"model_name,omitzero"`
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
