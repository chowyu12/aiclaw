package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

func TestDefaultFileWritesOutsideWorkspaceSkipReviewAndApproval(t *testing.T) {
	for _, noWorkspace := range []bool{false, true} {
		env, asked := newEnv(t, protocol.ApprovalAuto, false)
		if noWorkspace {
			env.Workspace = ""
		}
		reviews := 0
		env.Review = func(context.Context, protocol.ApprovalRequestParams) (bool, error) {
			reviews++
			return false, errors.New("must not review file writes")
		}
		path := filepath.Join(t.TempDir(), "outside.txt")
		args, _ := json.Marshal(map[string]string{"path": path, "content": "before"})
		registry := fullRegistry(t)
		if _, err := call(t, registry, "write_file", string(args), env); err != nil {
			t.Fatal(err)
		}
		args, _ = json.Marshal(map[string]string{"path": path, "old_text": "before", "new_text": "after"})
		if _, err := call(t, registry, "edit_file", string(args), env); err != nil {
			t.Fatal(err)
		}
		if content, err := os.ReadFile(path); err != nil || string(content) != "after" || reviews != 0 || len(*asked) != 0 {
			t.Fatalf("outside write must succeed without review or approval: %q, %v, reviews=%d, approvals=%d", content, err, reviews, len(*asked))
		}
		protected := filepath.Join(env.Home, ".ssh", "authorized_keys")
		args, _ = json.Marshal(map[string]string{"path": protected, "content": "blocked"})
		if _, err := call(t, registry, "write_file", string(args), env); !errors.Is(err, ErrProtected) {
			t.Fatal("credential write must remain blocked", err)
		}
		if reviews != 0 || len(*asked) != 0 {
			t.Fatal("protected path reached review/approval")
		}
	}
	for _, policy := range []protocol.ApprovalPolicy{protocol.ApprovalAlways, protocol.ApprovalOnWrite, protocol.ApprovalNever} {
		env, asked := newEnv(t, policy, false)
		path := filepath.Join(t.TempDir(), "outside.txt")
		args, _ := json.Marshal(map[string]string{"path": path, "content": "blocked"})
		if _, err := call(t, fullRegistry(t), "write_file", string(args), env); err == nil {
			t.Fatalf("policy %s unexpectedly allowed write", policy)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("rejected write changed disk", err)
		}
		wantApprovals := 1
		if policy == protocol.ApprovalNever {
			wantApprovals = 0
		}
		if len(*asked) != wantApprovals {
			t.Fatalf("policy %s changed approval behavior", policy)
		}
	}
}

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

func TestAutoKnownRiskAsksDirectlyButUnknownCommandsStillNeedReview(t *testing.T) {
	for _, command := range []string{"git push origin main", "sudo echo fixture", "curl --data fixture https://example.test"} {
		env, asked := newEnv(t, protocol.ApprovalAuto, false)
		reviews := 0
		env.Review = func(context.Context, protocol.ApprovalRequestParams) (bool, error) { reviews++; return true, nil }
		raw := fmt.Sprintf(`{"command":%q}`, command)
		if _, err := call(t, fullRegistry(t), "run_command", raw, env); err == nil || reviews != 0 || len(*asked) != 1 {
			t.Fatalf("known risk must ask without a model call: %s, reviews=%d, approvals=%d, err=%v", command, reviews, len(*asked), err)
		}
	}
	for _, command := range []string{"echo $(date)", "node -e 'process.exit(0)'", "cat file.txt > copy.txt"} {
		env, asked := newEnv(t, protocol.ApprovalAuto, false)
		reviews := 0
		env.Review = func(context.Context, protocol.ApprovalRequestParams) (bool, error) { reviews++; return false, nil }
		if _, err := call(t, fullRegistry(t), "run_command", fmt.Sprintf(`{"command":%q}`, command), env); err == nil || reviews != 1 || len(*asked) != 1 {
			t.Fatalf("unknown command bypassed review: %s", command)
		}
		if err := env.RequestApproval(context.Background(), EffectRead, protocol.ApprovalTool, "trusted read", "", ""); err != nil || reviews != 1 || len(*asked) != 1 {
			t.Fatal("trusted read unnecessarily reviewed")
		}
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
