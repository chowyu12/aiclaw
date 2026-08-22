package core

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	memorypkg "github.com/chowyu12/aiclaw/internal/memory"
	"github.com/chowyu12/aiclaw/internal/model"
	skillrunner "github.com/chowyu12/aiclaw/internal/skills"
	"github.com/chowyu12/aiclaw/internal/store"
	"github.com/chowyu12/aiclaw/internal/tools"
	"github.com/chowyu12/aiclaw/internal/tools/mcp"
	"github.com/chowyu12/aiclaw/internal/tools/websearch"
)

type LocalToolDispatcher struct {
	store  store.Store
	mu     sync.Mutex
	mcp    *mcp.Manager
	memory *memorypkg.Service
}

func NewLocalToolDispatcher(s store.Store) *LocalToolDispatcher {
	return &LocalToolDispatcher{store: s, memory: memorypkg.NewService(s)}
}

func (d *LocalToolDispatcher) PrepareTurn(ctx context.Context, thread model.Thread, turn Turn) (context.Context, error) {
	identity := memorypkg.ExecutionContext{
		UserID: thread.UserID, AgentUUID: "aiclaw-desktop", ConversationID: thread.ID,
		RunUUID: turn.ID, Input: turn.Input,
	}
	ctx = memorypkg.WithExecutionContext(ctx, identity)
	if !memorypkg.TurnPolicyFromContext(ctx).GenerateMemories {
		return ctx, nil
	}
	_, _, err := d.memory.RememberExplicit(ctx, identity, turn.Input)
	return ctx, err
}

func (d *LocalToolDispatcher) ContextMessages(ctx context.Context, _ model.Thread) ([]SamplingMessage, error) {
	identity := memorypkg.ExecutionContextFromContext(ctx)
	policy := memorypkg.TurnPolicyFromContext(ctx)
	content := ""
	if policy.UseMemories {
		memoryContext, err := d.memory.BuildContext(ctx, identity, identity.Input)
		if err != nil {
			return nil, err
		}
		if err := d.memory.RecordUsage(ctx, memoryContext, identity); err != nil {
			return nil, err
		}
		if memoryContext.Prompt != "" {
			content = "Local durable memories:\n" + memoryContext.Prompt
		}
	}
	if policy.GenerateMemories {
		content += "\n\nLocal memory behavior: explicit user requests to remember have already been saved for this turn. " +
			"When you notice other durable preferences, profile facts, decisions, procedures, or constraints that will help in future chats, use the memory tool with action=propose so the user can review them. " +
			"Never store secrets, credentials, temporary progress, or instructions that override higher-priority guidance."
	}
	skillStore, ok := d.store.(store.SkillStore)
	if !ok {
		if content == "" {
			return nil, nil
		}
		return []SamplingMessage{{Role: "system", Content: content}}, nil
	}
	items, err := skillStore.ListSkills(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if !item.Enabled || item.Instruction == "" {
			continue
		}
		content += "\n\n## Enabled skill: " + item.Name + "\n" + item.Instruction
	}
	if content == "" {
		return nil, nil
	}
	return []SamplingMessage{{Role: "system", Content: content}}, nil
}

// Reload closes cached MCP connections so settings changes take effect on the
// next turn without restarting the desktop application.
func (d *LocalToolDispatcher) Reload() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.mcp != nil {
		d.mcp.Close()
		d.mcp = nil
	}
}
func (d *LocalToolDispatcher) Definitions(ctx context.Context, thread model.Thread) ([]ToolDefinition, error) {
	defs := make([]ToolDefinition, 0)
	policy := memorypkg.TurnPolicyFromContext(ctx)
	for _, tool := range tools.DefaultBuiltinDefs() {
		if tool.Name == "memory" && !policy.GenerateMemories {
			continue
		}
		defs = append(defs, ToolDefinition{Name: tool.Name, Description: tool.Description, Schema: schemaFromFunctionDef(tool.FunctionDef)})
	}
	if thread.SearchEngineID > 0 {
		defs = append(defs, ToolDefinition{Name: "web_search", Description: "Search the web through the configured local search engine", Schema: model.JSON(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`)})
	}
	manager, err := d.mcpManager(ctx)
	if err != nil {
		return nil, err
	}
	for _, tool := range manager.Tools() {
		schema, _ := json.Marshal(tool.Parameters)
		defs = append(defs, ToolDefinition{Name: tool.Name, Description: tool.Description, Schema: model.JSON(schema)})
	}
	if skillStore, ok := d.store.(store.SkillStore); ok {
		skills, skillErr := skillStore.ListSkills(ctx)
		if skillErr != nil {
			return nil, skillErr
		}
		for _, skill := range skills {
			if !skill.Enabled || len(skill.ToolDefs) == 0 {
				continue
			}
			var definitions []model.SkillManifestTool
			if json.Unmarshal(skill.ToolDefs, &definitions) != nil {
				continue
			}
			for _, definition := range definitions {
				schema, _ := json.Marshal(definition.Parameters)
				defs = append(defs, ToolDefinition{Name: definition.Name, Description: definition.Description, Schema: model.JSON(schema)})
			}
		}
	}
	return defs, nil
}
func (d *LocalToolDispatcher) Execute(ctx context.Context, thread model.Thread, call ToolCall) (ToolResult, error) {
	if call.Name == "memory" {
		if !memorypkg.TurnPolicyFromContext(ctx).GenerateMemories {
			return ToolResult{CallID: call.ID, Name: call.Name, Content: `{"ok":false,"error":"memory generation is disabled"}`}, nil
		}
		output, err := d.memory.ToolHandler(ctx, call.Arguments)
		return ToolResult{CallID: call.ID, Name: call.Name, Content: output}, err
	}
	if call.Name == "web_search" {
		output, err := websearch.NewHandler(d.store)(websearch.WithSearchEngineID(ctx, thread.SearchEngineID), call.Arguments)
		return ToolResult{CallID: call.ID, Name: call.Name, Content: output}, err
	}
	if handler, ok := tools.DefaultBuiltins()[call.Name]; ok {
		output, err := handler(ctx, call.Arguments)
		return ToolResult{CallID: call.ID, Name: call.Name, Content: output}, err
	}
	if result, found, err := d.executeSkill(ctx, call); found {
		return result, err
	}
	manager, err := d.mcpManager(ctx)
	if err != nil {
		return ToolResult{CallID: call.ID, Name: call.Name}, err
	}
	output, err := manager.CallTool(ctx, call.Name, call.Arguments)
	return ToolResult{CallID: call.ID, Name: call.Name, Content: output}, err
}

func (d *LocalToolDispatcher) executeSkill(ctx context.Context, call ToolCall) (ToolResult, bool, error) {
	skillStore, ok := d.store.(store.SkillStore)
	if !ok {
		return ToolResult{}, false, nil
	}
	items, err := skillStore.ListSkills(ctx)
	if err != nil {
		return ToolResult{}, true, err
	}
	for _, skill := range items {
		if !skill.Enabled || len(skill.ToolDefs) == 0 {
			continue
		}
		var definitions []model.SkillManifestTool
		if json.Unmarshal(skill.ToolDefs, &definitions) != nil {
			continue
		}
		for _, definition := range definitions {
			if definition.Name != call.Name {
				continue
			}
			if skill.MainFile == "" || skill.InstallDir == "" {
				return ToolResult{CallID: call.ID, Name: call.Name, Content: "Follow enabled skill instructions: " + skill.Instruction}, true, nil
			}
			var config map[string]any
			_ = json.Unmarshal(skill.Config, &config)
			output, runErr := skillrunner.RunTool(ctx, skill.InstallDir, skill.MainFile, call.Name, call.Arguments, config, 30*time.Second)
			return ToolResult{CallID: call.ID, Name: call.Name, Content: output}, true, runErr
		}
	}
	return ToolResult{}, false, nil
}
func (d *LocalToolDispatcher) mcpManager(ctx context.Context) (*mcp.Manager, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.mcp != nil {
		return d.mcp, nil
	}
	servers, err := d.store.ListMCPServers(ctx)
	if err != nil {
		return nil, err
	}
	manager := mcp.NewManager()
	if err := manager.Connect(ctx, servers); err != nil {
		manager.Close()
		return nil, fmt.Errorf("connect MCP: %w", err)
	}
	d.mcp = manager
	return manager, nil
}
func schemaFromFunctionDef(def model.JSON) model.JSON {
	var value struct {
		Parameters json.RawMessage `json:"parameters"`
	}
	if json.Unmarshal(def, &value) == nil && len(value.Parameters) > 0 {
		return model.JSON(value.Parameters)
	}
	return model.JSON(`{"type":"object","properties":{}}`)
}
