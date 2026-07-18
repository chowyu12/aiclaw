package runtimeclient

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

var (
	localCLIEnvOnce sync.Once
	localCLIEnv     []string
)

const chatGPTBundledCodex = "/Applications/ChatGPT.app/Contents/Resources/codex"

// localCLIEnvironment is shared by detection and execution so a CLI cannot be
// reported as available and then fail only because the child process receives a
// different PATH. launchd starts user services with a deliberately small PATH;
// recover the login shell PATH once and add common user-level installation dirs.
func localCLIEnvironment() []string {
	localCLIEnvOnce.Do(func() {
		base := append([]string(nil), os.Environ()...)
		localCLIEnv = buildLocalCLIEnvironment(base, loginShellPath(base))
	})
	return append([]string(nil), localCLIEnv...)
}

func buildLocalCLIEnvironment(base []string, loginPath string) []string {
	home := environmentValue(base, "HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}

	extras := []string{
		"/opt/homebrew/bin", "/opt/homebrew/sbin",
		"/usr/local/bin", "/usr/local/sbin",
		"/usr/bin", "/bin", "/usr/sbin", "/sbin",
	}
	if runtime.GOOS == "darwin" {
		extras = append(extras, "/opt/local/bin", "/opt/local/sbin")
	}
	if home != "" {
		extras = append(extras,
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, "bin"),
			filepath.Join(home, ".cargo", "bin"),
			filepath.Join(home, "go", "bin"),
			filepath.Join(home, ".volta", "bin"),
			filepath.Join(home, ".bun", "bin"),
			filepath.Join(home, ".npm-global", "bin"),
			filepath.Join(home, "Library", "pnpm"),
			filepath.Join(home, ".asdf", "shims"),
			filepath.Join(home, ".mise", "shims"),
		)
	}

	pathValue := mergePaths(loginPath, environmentValue(base, "PATH"), strings.Join(extras, string(os.PathListSeparator)))
	return replaceEnvironmentValue(base, "PATH", pathValue)
}

func loginShellPath(env []string) string {
	if runtime.GOOS == "windows" {
		return ""
	}

	shell := strings.TrimSpace(environmentValue(env, "SHELL"))
	if shell == "" || !filepath.IsAbs(shell) {
		if runtime.GOOS == "darwin" {
			shell = "/bin/zsh"
		} else {
			shell = "/bin/sh"
		}
	}
	if info, err := os.Stat(shell); err != nil || info.IsDir() {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	const begin = "__AICLAW_PATH_BEGIN__"
	const end = "__AICLAW_PATH_END__"
	cmd := exec.CommandContext(ctx, shell, "-ilc", "printf '__AICLAW_PATH_BEGIN__%s__AICLAW_PATH_END__' \"$PATH\"")
	cmd.Env = buildLocalCLIEnvironment(env, "")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	text := string(output)
	start := strings.LastIndex(text, begin)
	if start < 0 {
		return ""
	}
	text = text[start+len(begin):]
	finish := strings.Index(text, end)
	if finish < 0 {
		return ""
	}
	return strings.TrimSpace(text[:finish])
}

func runtimeCommand(ctx context.Context, command string, args ...string) (*exec.Cmd, error) {
	return runtimeCommandWithEnvironment(ctx, command, args, localCLIEnvironment())
}

func runtimeCommandWithEnvironment(ctx context.Context, command string, args, env []string) (*exec.Cmd, error) {
	resolved, err := resolveRuntimeCommand(command, env)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, resolved, args...)
	cmd.Env = append([]string(nil), env...)
	return cmd, nil
}

func resolveRuntimeCommand(command string, env []string) (string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", errors.New("runtime task has no command")
	}
	if filepath.IsAbs(command) || strings.ContainsRune(command, os.PathSeparator) {
		return command, nil
	}
	if command == "codex" && runtime.GOOS == "darwin" && isExecutable(chatGPTBundledCodex) {
		// The ChatGPT app ships a signed Codex CLI and updates it with the app.
		// Prefer it over a stale or invalid third-party cask when callers use the
		// standard bare command. An explicit path above always wins.
		return chatGPTBundledCodex, nil
	}
	for _, dir := range filepath.SplitList(environmentValue(env, "PATH")) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, command)
		if isExecutable(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("local CLI %q was not found in the runtime PATH; install it or add it to your login shell PATH, then restart AiClaw", command)
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode().Perm()&0o111 != 0
}

func environmentValue(env []string, key string) string {
	prefix := key + "="
	for _, value := range env {
		if strings.HasPrefix(value, prefix) {
			return strings.TrimPrefix(value, prefix)
		}
	}
	return ""
}

func replaceEnvironmentValue(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	replaced := false
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			if !replaced {
				out = append(out, prefix+value)
				replaced = true
			}
			continue
		}
		out = append(out, item)
	}
	if !replaced {
		out = append(out, prefix+value)
	}
	return out
}

func mergePaths(values ...string) string {
	seen := make(map[string]struct{})
	parts := make([]string, 0)
	for _, value := range values {
		for _, part := range filepath.SplitList(value) {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if _, exists := seen[part]; exists {
				continue
			}
			seen[part] = struct{}{}
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, string(os.PathListSeparator))
}
