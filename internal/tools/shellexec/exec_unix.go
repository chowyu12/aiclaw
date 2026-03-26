//go:build !windows

package shellexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const defaultShell = "/bin/bash"

func buildShellCommand(ctx context.Context, shell, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, shell, "-c", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	return cmd
}

func shellCandidates() []string {
	seen := make(map[string]bool)
	out := make([]string, 0, 8)
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		out = append(out, path)
	}

	for _, s := range []string{"/bin/bash", "/bin/sh", "/usr/bin/bash", "/usr/bin/sh", "/bin/ash"} {
		if _, err := os.Stat(s); err == nil {
			add(s)
		}
	}
	if sh := strings.TrimSpace(os.Getenv("SHELL")); sh != "" {
		add(sh)
		base := filepath.Base(sh)
		if looked, err := exec.LookPath(base); err == nil {
			add(looked)
		}
	}
	for _, name := range []string{"bash", "sh", "ash", "zsh"} {
		if looked, err := exec.LookPath(name); err == nil {
			add(looked)
		}
	}
	for _, s := range []string{"/bin/zsh", "/usr/bin/zsh", defaultShell} {
		if _, err := os.Stat(s); err == nil {
			add(s)
		}
	}
	if len(out) == 0 {
		add(defaultShell)
	}
	return out
}

func isSpawnENOENT(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, exec.ErrNotFound) {
		return true
	}
	var ee *exec.Error
	if errors.As(err, &ee) && errors.Is(ee.Err, os.ErrNotExist) {
		return true
	}
	var pe *os.PathError
	if errors.As(err, &pe) {
		return errors.Is(pe.Err, os.ErrNotExist) || errors.Is(pe.Err, syscall.ENOENT)
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "fork/exec") && strings.Contains(msg, "no such file")
}
