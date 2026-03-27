//go:build windows

package daemon

import (
	"os"
	"os/exec"
)

func setSysProcAttr(_ *exec.Cmd) {}

func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(os.Kill) == nil
}

func stopProcess(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Kill()
}
