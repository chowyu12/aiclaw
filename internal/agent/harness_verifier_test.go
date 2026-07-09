package agent

import (
	"context"
	"testing"

	harnesspkg "github.com/chowyu12/aiclaw/pkg/harness"
)

func TestNewHarnessRuntimeForToolRoundUsesCurrentRoundEvents(t *testing.T) {
	st := &harnessTurnState{
		calledTools: map[string]bool{
			"old_tool":     true,
			"current_tool": true,
		},
	}
	st.verifier.ToolEvents = []harnesspkg.ToolEvent{{
		ToolName: "old_tool",
		Status:   harnesspkg.ToolStatusError,
	}}
	round := toolRoundResult{ToolEvents: []harnesspkg.ToolEvent{{
		ToolName: "current_tool",
		Status:   harnesspkg.ToolStatusSuccess,
	}}}

	runtime := newHarnessRuntimeForToolRound(context.Background(), &execContext{userMsg: "test"}, st, round)

	if got := runtime.Evidence.ToolEvents; len(got) != 1 || got[0].ToolName != "current_tool" {
		t.Fatalf("tool events = %#v, want only current round event", got)
	}
	if got := runtime.Evidence.ExecutionTools; len(got) != 1 || got[0] != "current_tool" {
		t.Fatalf("execution tools = %#v, want current_tool", got)
	}
}
