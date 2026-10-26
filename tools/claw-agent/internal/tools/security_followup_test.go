package tools

import (
	"context"
	"errors"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProtectedCaseAliasesFollowFilesystem(t *testing.T) {
	home := realpath(t.TempDir())
	dir := filepath.Join(home, ".ssh")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "fake-key")
	if err := os.WriteFile(file, []byte("FAKE"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(home, ".SSH", "fake-key")
	_, caseErr := os.Stat(alias)
	env := &Env{Home: home, Workspace: home}
	for _, path := range []string{file, alias, filepath.Join(home, ".SSH", "new-key")} {
		_, err := env.ResolveRead(path)
		shouldBlock := path == file || caseErr == nil
		if errors.Is(err, ErrProtected) != shouldBlock {
			t.Fatalf("path=%s blocked=%v expected=%v", path, err, shouldBlock)
		}
		_, _, err = env.ResolveWrite(path)
		if errors.Is(err, ErrProtected) != shouldBlock {
			t.Fatalf("write path=%s blocked=%v", path, err)
		}
	}
	// A protected file which does not yet exist must also reject its case alias.
	env.ProtectedPaths = []string{filepath.Join(home, "missing-secret.json")}
	_, err := env.ResolveRead(filepath.Join(home, "MISSING-SECRET.JSON"))
	if errors.Is(err, ErrProtected) != (caseErr == nil) {
		t.Fatalf("missing protected file: %v", err)
	}
	if _, err := env.ResolveRead(filepath.Join(home, ".ssh-neighbor", "file.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestCommandSecretsRedactedBeforeRetention(t *testing.T) {
	secret := "FAKE-OUTPUT-KEY-123456789"
	t.Setenv("AICLAW_TEST_API_KEY", secret)
	env, _ := newEnv(t, protocol.ApprovalBypass, true)
	saved := ""
	env.SaveOutput = func(_ context.Context, text string, _ bool) (string, error) { saved = text; return "saved", nil }
	env.SecretValues = []string{"FAKE-SESSION-KEY-987654321"}
	registry := fullRegistry(t)
	out, err := call(t, registry, "run_command", `{"command":"printf '%s' \"$AICLAW_TEST_API_KEY\"; printf '%s' FAKE-SESSION-KEY-987654321 >&2; head -c 30000 /dev/zero | tr '\\0' x"}`, env)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{out, saved} {
		if strings.Contains(text, secret) || strings.Contains(text, env.SecretValues[0]) {
			t.Fatal("secret retained in output")
		}
		if !strings.Contains(text, "[REDACTED]") {
			t.Fatal("missing replacement")
		}
	}
}

func TestPersistentShellSecretsRedacted(t *testing.T) {
	t.Setenv("AICLAW_TEST_TOKEN", "FAKE-PERSISTENT-TOKEN-1234")
	env, _ := newEnv(t, protocol.ApprovalBypass, true)
	env.Shells = NewShellPool()
	defer env.Shells.Close()
	out, _, err := env.Shells.Run(context.Background(), "new", `printf '%s' "$AICLAW_TEST_TOKEN"`, env, defaultExecTimeout)
	if err != nil {
		t.Skipf("persistent shell unavailable: %v", err)
	}
	if strings.Contains(out, "FAKE-PERSISTENT") || !strings.Contains(out, "[REDACTED]") {
		t.Fatal("secret in persistent output")
	}
	if got := env.commandRedactor()("ordinary status OK", true); got != "ordinary status OK" {
		t.Fatal(got)
	}
}

func TestKnownSecretsCoverHeadersShortValuesAndCaptureBoundaries(t *testing.T) {
	t.Setenv("AICLAW_SHORT_PASSWORD", "h1")
	t.Setenv("TOKENIZERS_PARALLELISM", "false")
	env := &Env{}
	env.AddSecret("X-API-Key", "FAKE-HEADER-TOKEN-12345")
	env.AddSecret("PATH", "/usr/bin")
	redact := env.commandRedactor()
	if got := redact("h1 FAKE-HEADER-TOKEN-12345 /usr/bin false", true); got != "[REDACTED] [REDACTED] /usr/bin false" {
		t.Fatal(got)
	}
	if got := redact("output FAKE-HEADER-TOKEN-123", false); got != "output [REDACTED]" {
		t.Fatal("captured prefix exposed", got)
	}
}
