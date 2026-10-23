package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

func TestAutoCommandReviewAndOtherPolicies(t *testing.T) {
	for _, policy := range []protocol.ApprovalPolicy{protocol.ApprovalAuto, protocol.ApprovalAlways, protocol.ApprovalBypass, protocol.ApprovalNever} {
		t.Run(string(policy), func(t *testing.T) {
			env, asked := newEnv(t, policy, false)
			reviews := 0
			env.Review = func(context.Context, protocol.ApprovalRequestParams) (bool, error) { reviews++; return true, nil }
			_, err := call(t, fullRegistry(t), "run_command", `{"command":"echo reviewed"}`, env)
			if (err != nil) != (policy == protocol.ApprovalAlways) {
				t.Fatalf("unexpected result: %v", err)
			}
			if (reviews == 1) != (policy == protocol.ApprovalAuto) {
				t.Fatalf("reviews = %d", reviews)
			}
			if (len(*asked) == 1) != (policy == protocol.ApprovalAlways) {
				t.Fatalf("approvals = %d", len(*asked))
			}
		})
	}
}

func TestAutoReviewedCommandsRetainSandboxUnlessHumanApproved(t *testing.T) {
	env, registry, outside := sandboxEnv(t)
	env.Policy = protocol.ApprovalAuto
	env.Review = func(context.Context, protocol.ApprovalRequestParams) (bool, error) { return true, nil }
	path := filepath.Join(outside, "blocked.txt")
	args := fmt.Sprintf(`{"command":%q}`, "echo reviewed > "+path)
	if out, err := call(t, registry, "run_command", args, env); err != nil || !strings.Contains(out, "[沙箱]") {
		t.Fatal("missing sandbox rejection", out, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("AI-approved command wrote outside sandbox", err)
	}
	env.Review = func(context.Context, protocol.ApprovalRequestParams) (bool, error) { return false, nil }
	if _, err := call(t, registry, "run_command", args, env); err != nil {
		t.Fatal("human-approved command should retain existing behavior", err)
	}
	if data, err := os.ReadFile(path); err != nil || !strings.Contains(string(data), "reviewed") {
		t.Fatal("human-approved command did not write the requested file", err)
	}
}

func TestAutoReviewFailureFallsBackAndCannotBypassHardGuards(t *testing.T) {
	env, asked := newEnv(t, protocol.ApprovalAuto, false)
	reviews := 0
	env.Review = func(context.Context, protocol.ApprovalRequestParams) (bool, error) {
		reviews++
		return false, errors.New("review failed")
	}
	if _, err := call(t, fullRegistry(t), "run_command", `{"command":"echo reviewed"}`, env); err == nil || reviews != 1 || len(*asked) != 1 {
		t.Fatal("failed review did not ask and honor rejection")
	}
	if _, err := call(t, fullRegistry(t), "run_command", `{"command":"rm -rf /"}`, env); err == nil || reviews != 1 {
		t.Fatal("hard guard reached AI reviewer")
	}
	if _, err := env.ResolveRead("~/.ssh/id_rsa"); err == nil {
		t.Fatal("auto mode bypassed credential guard")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := env.RequestApproval(ctx, EffectExternal, protocol.ApprovalTool, "external", "", ""); !errors.Is(err, context.Canceled) || len(*asked) != 1 {
		t.Fatal("cancelled review opened human approval", err)
	}
}
