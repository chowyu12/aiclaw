package model

import (
	"testing"
	"time"
)

func TestRuntimeRefreshStatus(t *testing.T) {
	now := time.Now()
	recent := now.Add(-RuntimeOfflineAfter / 2)
	runtime := &Runtime{LastSeenAt: &recent, Status: RuntimeStatusOffline}
	if !runtime.IsOnline(now) {
		t.Fatal("recent heartbeat should be online")
	}
	stale := now.Add(-RuntimeOfflineAfter - time.Second)
	runtime.LastSeenAt = &stale
	if runtime.IsOnline(now) || runtime.Status != RuntimeStatusOffline {
		t.Fatal("stale heartbeat should be offline")
	}
}

func TestRuntimeAgentPromptMode(t *testing.T) {
	for _, agentType := range []string{
		RuntimeAgentTypeCursor,
		RuntimeAgentTypeClaudeCode,
		RuntimeAgentTypeCodeBuddy,
		RuntimeAgentTypeOpenClaw,
		RuntimeAgentTypeHermes,
	} {
		if got := RuntimeAgentPromptMode(agentType); got != RuntimePromptArgument {
			t.Fatalf("%s prompt mode = %s, want argument", agentType, got)
		}
	}
	if got := RuntimeAgentPromptMode(RuntimeAgentTypeCodex); got != RuntimePromptStdin {
		t.Fatalf("codex prompt mode = %s, want stdin", got)
	}
	custom := &Runtime{AgentType: RuntimeAgentTypeCustom, PromptMode: RuntimePromptArgument}
	if got := custom.EffectivePromptMode(); got != RuntimePromptArgument {
		t.Fatalf("custom prompt mode = %s, want argument", got)
	}
}
