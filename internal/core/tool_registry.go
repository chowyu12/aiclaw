package core

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/chowyu12/aiclaw/internal/model"
)

// ToolHandler is the only executable tool shape accepted by the local runtime.
// A tool cannot be advertised unless it is registered with a handler.
type ToolHandler func(context.Context, model.Thread, ToolCall) (ToolResult, error)

type RegisteredTool struct {
	Definition ToolDefinition
	Handler    ToolHandler
	Source     string
}

// ToolRegistry is the per-turn, collision-checked source of truth for both
// model-visible definitions and execution dispatch.
type ToolRegistry struct {
	tools map[string]RegisteredTool
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{tools: make(map[string]RegisteredTool)}
}

func (r *ToolRegistry) Register(tool RegisteredTool) error {
	name := strings.TrimSpace(tool.Definition.Name)
	if name == "" {
		return fmt.Errorf("tool name is required")
	}
	if tool.Handler == nil {
		return fmt.Errorf("tool %q has no handler", name)
	}
	if current, exists := r.tools[name]; exists {
		return fmt.Errorf("tool %q from %s conflicts with %s", name, tool.Source, current.Source)
	}
	tool.Definition.Name = name
	r.tools[name] = tool
	return nil
}

func (r *ToolRegistry) Definitions() []ToolDefinition {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	definitions := make([]ToolDefinition, 0, len(names))
	for _, name := range names {
		definitions = append(definitions, r.tools[name].Definition)
	}
	return definitions
}

func (r *ToolRegistry) Lookup(name string) (RegisteredTool, bool) {
	tool, ok := r.tools[strings.TrimSpace(name)]
	return tool, ok
}

func (r *ToolRegistry) Validate() error {
	for name, tool := range r.tools {
		if tool.Handler == nil {
			return fmt.Errorf("tool %q has no handler", name)
		}
		if strings.TrimSpace(tool.Definition.Name) != name {
			return fmt.Errorf("tool registry key %q does not match definition %q", name, tool.Definition.Name)
		}
	}
	return nil
}
