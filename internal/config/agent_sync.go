package config

import (
	"strings"

	"github.com/chowyu12/aiclaw/internal/model"
)

// SyncAgentConfigFromModel 将内存中的单例 Agent 写回 cfg.Agent（用于落盘到 config.yaml）。
func SyncAgentConfigFromModel(dst *AgentConfig, src *model.Agent) {
	if dst == nil || src == nil {
		return
	}
	dst.UUID = src.UUID
	dst.Name = src.Name
	dst.Description = src.Description
	dst.SystemPrompt = src.SystemPrompt
	dst.ProviderID = src.ProviderID
	dst.ModelName = src.ModelName
	dst.Temperature = src.Temperature
	dst.MaxTokens = src.MaxTokens
	dst.Timeout = src.Timeout
	dst.MaxHistory = src.MaxHistory
	dst.MaxIterations = src.MaxIterations
	dst.Token = strings.TrimSpace(src.Token)
	dst.ToolSearchEnabled = src.ToolSearchEnabled
	dst.MemOSEnabled = src.MemOSEnabled
	dst.MemOSCfg = src.MemOSCfg
	dst.ToolIDs = append([]int64(nil), src.ToolIDs...)
}
