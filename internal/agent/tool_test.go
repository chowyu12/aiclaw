package agent

import (
	"testing"

	"github.com/chowyu12/aiclaw/internal/model"
)

func TestToolRegistry_BuildTrackedTools(t *testing.T) {
	registry := NewToolRegistry()

	t.Run("builtin_tool", func(t *testing.T) {
		tracker := NewStepTracker(newMockStore(), 1)
		toolDefs := []model.Tool{
			{Name: "ls", Description: "list dir", HandlerType: model.HandlerBuiltin, Enabled: true},
		}
		tools := registry.BuildTrackedTools(toolDefs, tracker, nil)
		if len(tools) != 1 {
			t.Fatalf("expected 1 tool, got %d", len(tools))
		}
		if tools[0].Name() != "ls" {
			t.Errorf("expected 'ls', got %q", tools[0].Name())
		}
		output, err := tools[0].Call(t.Context(), `{"path":"."}`)
		if err != nil {
			t.Fatalf("tool call error: %v", err)
		}
		if output == "" {
			t.Error("expected non-empty output from ls")
		}
	})

	t.Run("disabled_tool_skipped", func(t *testing.T) {
		tracker := NewStepTracker(newMockStore(), 1)
		toolDefs := []model.Tool{
			{Name: "ls", HandlerType: model.HandlerBuiltin, Enabled: false},
		}
		tools := registry.BuildTrackedTools(toolDefs, tracker, nil)
		if len(tools) != 0 {
			t.Errorf("expected 0 tools, got %d", len(tools))
		}
	})

	t.Run("unknown_builtin_skipped", func(t *testing.T) {
		tracker := NewStepTracker(newMockStore(), 1)
		toolDefs := []model.Tool{
			{Name: "nonexistent_builtin", HandlerType: model.HandlerBuiltin, Enabled: true},
		}
		tools := registry.BuildTrackedTools(toolDefs, tracker, nil)
		if len(tools) != 0 {
			t.Errorf("expected 0 tools, got %d", len(tools))
		}
	})

	t.Run("tracked_tool_records_step", func(t *testing.T) {
		ms := newMockStore()
		tracker := NewStepTracker(ms, 100)
		toolDefs := []model.Tool{
			{Name: "ls", Description: "list dir", HandlerType: model.HandlerBuiltin, Enabled: true},
		}
		tools := registry.BuildTrackedTools(toolDefs, tracker, nil)
		if len(tools) != 1 {
			t.Fatal("expected 1 tool")
		}
		_, err := tools[0].Call(t.Context(), `{"path":"."}`)
		if err != nil {
			t.Fatal(err)
		}
		steps := tracker.Steps()
		if len(steps) != 1 {
			t.Fatalf("expected 1 step, got %d", len(steps))
		}
		if steps[0].StepType != model.StepToolCall {
			t.Errorf("expected tool_call step, got %s", steps[0].StepType)
		}
		if steps[0].Status != model.StepSuccess {
			t.Errorf("expected success status, got %s", steps[0].Status)
		}
	})
}

func TestBuiltinHandlers(t *testing.T) {
	registry := NewToolRegistry()
	ctx := t.Context()

	t.Run("ls_handler", func(t *testing.T) {
		handler := registry.builtins["ls"]
		result, err := handler(ctx, `{"path":"."}`)
		if err != nil {
			t.Fatal(err)
		}
		if result == "" {
			t.Error("expected non-empty output from ls")
		}
	})

	t.Run("grep_handler", func(t *testing.T) {
		handler := registry.builtins["grep"]
		result, err := handler(ctx, `{"pattern":"func.*Handler","path":".", "include":"*.go"}`)
		if err != nil {
			t.Fatal(err)
		}
		if result == "" {
			t.Error("expected non-empty output from grep")
		}
	})
}
