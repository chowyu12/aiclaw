package model

import (
	"strings"
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

func TestRuntimeDetectedAgentAndSpec(t *testing.T) {
	runtime := &Runtime{DetectedAgents: StringSlice{RuntimeAgentTypeCodex, RuntimeAgentTypeHermes}}
	if !runtime.HasDetectedAgent(RuntimeAgentTypeCodex) || runtime.HasDetectedAgent(RuntimeAgentTypeCursor) {
		t.Fatal("detected-agent membership is incorrect")
	}
	spec, ok := LocalCLISpecFor(RuntimeAgentTypeClaudeCode)
	if !ok || spec.Command != "claude" || spec.PromptMode != RuntimePromptArgument {
		t.Fatalf("unexpected Claude Code spec: %#v, %v", spec, ok)
	}
}

func TestLocalCLISpecArgsWithModel(t *testing.T) {
	codex, ok := LocalCLISpecFor(RuntimeAgentTypeCodex)
	if !ok || !codex.SupportsModel() {
		t.Fatal("codex should support model selection")
	}
	if got := strings.Join(codex.ArgsWithModel("gpt-test"), " "); got != "exec -m gpt-test -" {
		t.Fatalf("codex args = %q", got)
	}

	openclaw, ok := LocalCLISpecFor(RuntimeAgentTypeOpenClaw)
	if !ok || !openclaw.SupportsModel() {
		t.Fatal("openclaw should support selecting a registered agent")
	}
	if got := strings.Join(openclaw.ArgsWithModel("ops"), " "); got != "agent --local --message --agent ops" {
		t.Fatalf("openclaw args = %q", got)
	}
}
