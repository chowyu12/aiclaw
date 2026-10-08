package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/tools"
)

func TestCodeModeRetainsOriginatingPermissions(t *testing.T) {
	s := newTestSession(t, &fakeModel{}, protocol.ApprovalAlways)
	secret := filepath.Join(t.TempDir(), "secret")
	s.Guard(secret)
	oldEmitter, nextEmitter := &recordingEmitter{approve: true}, &recordingEmitter{approve: false}
	old := s.turnEnv("old-turn", oldEmitter)
	if err := s.registry.Register(tools.Tool{Name: "external_action", Effect: tools.EffectExternal, Handler: func(ctx context.Context, _ json.RawMessage, env *tools.Env) (string, error) {
		return "done", env.RequestApproval(ctx, tools.EffectExternal, protocol.ApprovalTool, "action", "", "")
	}}); err != nil {
		t.Fatal(err)
	}
	s.installCodeMode()
	// Simulate later configuration and another turn's execution environment.
	s.mu.Lock()
	s.config.ApprovalPolicy = protocol.ApprovalNever
	s.config.ProtectedPaths[0] = secret + "-changed"
	s.mu.Unlock()
	next := s.turnEnv("next-turn", nextEmitter)
	exec, _ := s.registry.Get("exec")
	args := json.RawMessage(`{"code":"return await tools.external_action({})"}`)
	if _, err := exec.Handler(context.Background(), args, old); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.Handler(context.Background(), args, next); err == nil {
		t.Fatal("later unattended turn reused previous approval")
	}
	if len(oldEmitter.approvals) != 1 || oldEmitter.approvals[0].TurnID != "old-turn" || len(nextEmitter.approvals) != 0 {
		t.Fatal("approval routed to wrong turn")
	}
	if old.Policy != protocol.ApprovalAlways || old.ProtectedPaths[0] != secret {
		t.Fatal("old permission snapshot changed")
	}
	if _, err := old.ResolveRead(secret); err == nil {
		t.Fatal("originating protected path became readable")
	}
	s.Grant("/explicit-session-grant")
	if len(old.Grants()) != 1 || len(next.Grants()) != 1 {
		t.Fatal("explicit session grants should remain shared")
	}
}
