//go:build windows

package process

import "os/exec"

func buildCommand(command string) *exec.Cmd {
	return exec.Command("cmd", "/C", command)
}

func setProcAttr(_ *exec.Cmd) {}

func killProcessGroup(cmd *exec.Cmd) {
	cmd.Process.Kill()
}
