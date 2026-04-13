package agent

import (
	"context"
	"testing"
)

func TestHookRegistry_FireNoHooks(t *testing.T) {
	r := NewHookRegistry()
	action := r.Fire(t.Context(), HookPreToolUse, &HookPayload{ToolName: "test"})
	if action != HookContinue {
		t.Errorf("expected HookContinue with no hooks, got %d", action)
	}
}

func TestHookRegistry_FireSingleHook(t *testing.T) {
	r := NewHookRegistry()
	var received string
	r.Register(HookPreToolUse, func(_ context.Context, _ HookEvent, p *HookPayload) HookAction {
		received = p.ToolName
		return HookContinue
	})

	r.Fire(t.Context(), HookPreToolUse, &HookPayload{ToolName: "my_tool"})
	if received != "my_tool" {
		t.Errorf("expected 'my_tool', got %q", received)
	}
}

func TestHookRegistry_FireReturnsHighestPriority(t *testing.T) {
	r := NewHookRegistry()
	r.Register(HookPreLLMCall, func(_ context.Context, _ HookEvent, _ *HookPayload) HookAction {
		return HookContinue
	})
	r.Register(HookPreLLMCall, func(_ context.Context, _ HookEvent, _ *HookPayload) HookAction {
		return HookAbort
	})
	r.Register(HookPreLLMCall, func(_ context.Context, _ HookEvent, _ *HookPayload) HookAction {
		return HookSkip
	})

	action := r.Fire(t.Context(), HookPreLLMCall, &HookPayload{})
	if action != HookAbort {
		t.Errorf("expected HookAbort (highest priority), got %d", action)
	}
}

func TestHookRegistry_FireMultipleEvents(t *testing.T) {
	r := NewHookRegistry()
	var preCalled, postCalled bool

	r.Register(HookPreToolUse, func(_ context.Context, _ HookEvent, _ *HookPayload) HookAction {
		preCalled = true
		return HookContinue
	})
	r.Register(HookPostToolUse, func(_ context.Context, _ HookEvent, _ *HookPayload) HookAction {
		postCalled = true
		return HookContinue
	})

	r.Fire(t.Context(), HookPreToolUse, &HookPayload{})
	if !preCalled {
		t.Error("pre hook should have been called")
	}
	if postCalled {
		t.Error("post hook should not have been called yet")
	}

	r.Fire(t.Context(), HookPostToolUse, &HookPayload{})
	if !postCalled {
		t.Error("post hook should have been called")
	}
}

func TestHookRegistry_ExecutionOrder(t *testing.T) {
	r := NewHookRegistry()
	var order []int

	r.Register(HookAgentDone, func(_ context.Context, _ HookEvent, _ *HookPayload) HookAction {
		order = append(order, 1)
		return HookContinue
	})
	r.Register(HookAgentDone, func(_ context.Context, _ HookEvent, _ *HookPayload) HookAction {
		order = append(order, 2)
		return HookContinue
	})
	r.Register(HookAgentDone, func(_ context.Context, _ HookEvent, _ *HookPayload) HookAction {
		order = append(order, 3)
		return HookContinue
	})

	r.Fire(t.Context(), HookAgentDone, &HookPayload{})
	if len(order) != 3 || order[0] != 1 || order[1] != 2 || order[2] != 3 {
		t.Errorf("hooks should execute in registration order, got %v", order)
	}
}
