//go:build !windows

package process

import (
	"os"
	"os/exec"
	"syscall"
)

func buildCommand(command string) *exec.Cmd {
	shell := findShell()
	return exec.Command(shell, "-c", command)
}

func setProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessGroup(cmd *exec.Cmd) {
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); err != nil {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

func findShell() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	for _, s := range []string{"/bin/bash", "/bin/zsh", "/bin/sh"} {
		if _, err := os.Stat(s); err == nil {
			return s
		}
	}
	return "/bin/sh"
}
