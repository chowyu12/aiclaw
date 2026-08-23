package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	store   ToolRuntimeStore
	mu      sync.Mutex
	planMu  sync.Mutex
	mcp     *mcp.Manager
	memory  *memorypkg.Service
	root    string
	sampler Sampler
	plans   map[string]*model.PlanState
}

// ToolRuntimeStore is the complete persistence contract of the new desktop
// tool runtime. It intentionally excludes legacy Agent, Runtime, Channel,
// Conversation, daemon, and CDP stores.
type ToolRuntimeStore interface {
	ThreadStore
	store.MemoryStore
	store.SearchEngineStore
	ListMCPServers(context.Context) ([]model.MCPServer, error)
}

type DispatcherOption func(*LocalToolDispatcher)

func WithDispatcherRoot(root string) DispatcherOption {
	return func(d *LocalToolDispatcher) { d.root = strings.TrimSpace(root) }
}

func WithSubAgentSampler(sampler Sampler) DispatcherOption {
	return func(d *LocalToolDispatcher) { d.sampler = sampler }
}

func NewLocalToolDispatcher(s ToolRuntimeStore, options ...DispatcherOption) *LocalToolDispatcher {
	home, _ := os.UserHomeDir()
	d := &LocalToolDispatcher{
		store: s, memory: memorypkg.NewService(s), root: filepath.Join(home, ".aiclaw"),
		plans: make(map[string]*model.PlanState),
	}
	for _, option := range options {
		option(d)
	}
	return d
}

func (d *LocalToolDispatcher) SetSampler(sampler Sampler) { d.sampler = sampler }

func (d *LocalToolDispatcher) PrepareTurn(ctx context.Context, thread model.Thread, turn Turn) (context.Context, error) {
	ctx = withToolTurn(ctx, thread, turn)
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
	var catalog strings.Builder
	catalog.WriteString("\n\nEnabled skill catalog (use the skill tool with action=read_active to load full instructions when needed):")
	matched := 0
	for _, item := range items {
		if !item.Enabled || item.Instruction == "" {
			continue
		}
		catalog.WriteString("\n- ")
		catalog.WriteString(item.Name)
		if item.Description != "" {
			catalog.WriteString(": ")
			catalog.WriteString(item.Description)
		}
		if skillRelevant(identity.Input, item) {
			matched++
			content += "\n\n## Selected skill: " + item.Name + "\n" + item.Instruction
		}
	}
	if strings.Contains(catalog.String(), "\n- ") {
		content += catalog.String()
	}
	_ = matched
	if content == "" {
		return nil, nil
	}
	return []SamplingMessage{{Role: "system", Content: content}}, nil
}

func skillRelevant(input string, skill model.Skill) bool {
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "" {
		return false
	}
	for _, candidate := range []string{skill.Name, skill.Slug, skill.DirName} {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate != "" && strings.Contains(input, candidate) {
			return true
		}
	}
	for _, token := range strings.FieldsFunc(strings.ToLower(skill.Description), func(r rune) bool {
		return r < 'a' || r > 'z'
	}) {
		if len(token) >= 5 && strings.Contains(input, token) {
			return true
		}
	}
	return false
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
	registry, err := d.registry(ctx, thread)
	if err != nil {
		return nil, err
	}
	return registry.Definitions(), nil
}

func (d *LocalToolDispatcher) registry(ctx context.Context, thread model.Thread) (*ToolRegistry, error) {
	registry := NewToolRegistry()
	policy := memorypkg.TurnPolicyFromContext(ctx)
	handlers := tools.DefaultBuiltins()
	for _, tool := range tools.DefaultBuiltinDefs() {
		if tool.Name == "memory" && !policy.GenerateMemories {
			continue
		}
		if tool.Name == "web_search" && thread.SearchEngineID == 0 {
			continue
		}
		handler, ok := handlers[tool.Name]
		var execute ToolHandler
		if ok {
			execute = func(ctx context.Context, _ model.Thread, call ToolCall) (ToolResult, error) {
				output, runErr := handler(ctx, call.Arguments)
				return ToolResult{CallID: call.ID, Name: call.Name, Content: output}, runErr
			}
		} else {
			execute = d.coreHandler(tool.Name)
		}
		if execute == nil {
			return nil, fmt.Errorf("builtin tool %q is advertised without an executor", tool.Name)
		}
		if err := registry.Register(RegisteredTool{
			Definition: ToolDefinition{Name: tool.Name, Description: tool.Description, Schema: schemaFromFunctionDef(tool.FunctionDef)},
			Handler:    execute, Source: "builtin",
		}); err != nil {
			return nil, err
		}
	}
	manager, err := d.mcpManager(ctx)
	if err != nil {
		return nil, err
	}
	for _, tool := range manager.Tools() {
		schema, _ := json.Marshal(tool.Parameters)
		name := tool.Name
		if err := registry.Register(RegisteredTool{
			Definition: ToolDefinition{Name: name, Description: tool.Description, Schema: model.JSON(schema)},
			Source:     "mcp:" + tool.ServerName,
			Handler: func(ctx context.Context, _ model.Thread, call ToolCall) (ToolResult, error) {
				output, callErr := manager.CallTool(ctx, name, call.Arguments)
				return ToolResult{CallID: call.ID, Name: call.Name, Content: output}, callErr
			},
		}); err != nil {
			return nil, err
		}
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
			if _, permissionErr := skillrunner.ValidateExecutable(skill); permissionErr != nil {
				continue
			}
			var definitions []model.SkillManifestTool
			if json.Unmarshal(skill.ToolDefs, &definitions) != nil {
				continue
			}
			for _, definition := range definitions {
				schema, _ := json.Marshal(definition.Parameters)
				skillCopy, definitionCopy := skill, definition
				if err := registry.Register(RegisteredTool{
					Definition: ToolDefinition{Name: definitionCopy.Name, Description: definitionCopy.Description, Schema: model.JSON(schema)},
					Source:     "skill:" + skillCopy.Name,
					Handler: func(ctx context.Context, _ model.Thread, call ToolCall) (ToolResult, error) {
						return d.executeSkillRecord(ctx, skillCopy, definitionCopy, call)
					},
				}); err != nil {
					return nil, err
				}
			}
		}
	}
	if err := registry.Validate(); err != nil {
		return nil, err
	}
	return registry, nil
}
func (d *LocalToolDispatcher) Execute(ctx context.Context, thread model.Thread, call ToolCall) (ToolResult, error) {
	registry, err := d.registry(ctx, thread)
	if err != nil {
		return ToolResult{CallID: call.ID, Name: call.Name}, err
	}
	tool, ok := registry.Lookup(call.Name)
	if !ok {
		return ToolResult{CallID: call.ID, Name: call.Name}, fmt.Errorf("tool %q is not registered", call.Name)
	}
	return tool.Handler(ctx, thread, call)
}

func (d *LocalToolDispatcher) executeSkillRecord(ctx context.Context, skill model.Skill, _ model.SkillManifestTool, call ToolCall) (ToolResult, error) {
	if skill.MainFile == "" || skill.InstallDir == "" {
		return ToolResult{CallID: call.ID, Name: call.Name, Content: "Follow enabled skill instructions: " + skill.Instruction}, nil
	}
	var config map[string]any
	_ = json.Unmarshal(skill.Config, &config)
	permissions, permissionErr := skillrunner.ValidateExecutable(skill)
	if permissionErr != nil {
		return ToolResult{CallID: call.ID, Name: call.Name}, permissionErr
	}
	output, runErr := skillrunner.RunTool(ctx, skill.InstallDir, skill.MainFile, call.Name, call.Arguments, config, permissions, 30*time.Second)
	return ToolResult{CallID: call.ID, Name: call.Name, Content: output}, runErr
}

func (d *LocalToolDispatcher) coreHandler(name string) ToolHandler {
	switch name {
	case "memory":
		return func(ctx context.Context, _ model.Thread, call ToolCall) (ToolResult, error) {
			if !memorypkg.TurnPolicyFromContext(ctx).GenerateMemories {
				return ToolResult{CallID: call.ID, Name: call.Name, Content: `{"ok":false,"error":"memory generation is disabled"}`}, nil
			}
			output, err := d.memory.ToolHandler(ctx, call.Arguments)
			return ToolResult{CallID: call.ID, Name: call.Name, Content: output}, err
		}
	case "web_search":
		return func(ctx context.Context, thread model.Thread, call ToolCall) (ToolResult, error) {
			output, err := websearch.NewHandler(d.store)(websearch.WithSearchEngineID(ctx, thread.SearchEngineID), call.Arguments)
			return ToolResult{CallID: call.ID, Name: call.Name, Content: output}, err
		}
	case "sub_agent":
		return d.executeSubAgent
	case "plan":
		return d.executePlan
	case "skill":
		return d.executeSkillManager
	case "session_search":
		return d.executeSessionSearch
	default:
		return nil
	}
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
