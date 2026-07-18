package runtimeclient

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBuildLocalCLIEnvironmentMergesLoginAndFallbackPaths(t *testing.T) {
	home := t.TempDir()
	env := buildLocalCLIEnvironment([]string{
		"HOME=" + home,
		"PATH=/usr/bin:/bin",
		"TOKEN=keep-me",
	}, "/custom/bin:/usr/bin")

	path := environmentValue(env, "PATH")
	for _, expected := range []string{
		"/custom/bin", "/usr/bin", "/bin", "/usr/local/bin",
		filepath.Join(home, ".local", "bin"), filepath.Join(home, ".volta", "bin"),
	} {
		if !strings.Contains(path, expected) {
			t.Fatalf("PATH missing %q: %s", expected, path)
		}
	}
	if got := environmentValue(env, "TOKEN"); got != "keep-me" {
		t.Fatalf("non-PATH environment variable changed: %q", got)
	}
}

func TestRuntimeCommandResolvesFromSuppliedEnvironment(t *testing.T) {
	dir := t.TempDir()
	command := filepath.Join(dir, "local-agent")
	if err := os.WriteFile(command, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd, err := runtimeCommandWithEnvironment(context.Background(), "local-agent", []string{"--check"}, []string{"PATH=" + dir, "HOME=/tmp/aiclaw-test"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Path != command {
		t.Fatalf("resolved command = %q, want %q", cmd.Path, command)
	}
	if got := environmentValue(cmd.Env, "HOME"); got != "/tmp/aiclaw-test" {
		t.Fatalf("command HOME = %q", got)
	}
}

func TestResolveRuntimeCommandExplainsMissingCLI(t *testing.T) {
	_, err := resolveRuntimeCommand("missing-aiclaw-agent", []string{"PATH=" + t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "login shell PATH") {
		t.Fatalf("missing CLI error = %v", err)
	}
}

func TestResolveRuntimeCommandPrefersBundledCodexOnMacOS(t *testing.T) {
	if runtime.GOOS != "darwin" || !isExecutable(chatGPTBundledCodex) {
		t.Skip("ChatGPT bundled Codex is only available on configured macOS hosts")
	}
	dir := t.TempDir()
	pathCodex := filepath.Join(dir, "codex")
	if err := os.WriteFile(pathCodex, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := resolveRuntimeCommand("codex", []string{"PATH=" + dir})
	if err != nil {
		t.Fatal(err)
	}
	if got != chatGPTBundledCodex {
		t.Fatalf("resolved Codex = %q, want %q", got, chatGPTBundledCodex)
	}
}
