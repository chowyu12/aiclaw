//go:build windows

package shellexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
)

func buildShellCommand(ctx context.Context, shell, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, shell, "/C", command)
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
	return cmd
}

func shellCandidates() []string {
	out := make([]string, 0, 4)
	if comspec := os.Getenv("COMSPEC"); comspec != "" {
		out = append(out, comspec)
	}
	if ps, err := exec.LookPath("powershell.exe"); err == nil {
		out = append(out, ps)
	}
	if len(out) == 0 {
		out = append(out, "cmd.exe")
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
		return errors.Is(pe.Err, os.ErrNotExist)
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not found") || strings.Contains(msg, "cannot find")
}
