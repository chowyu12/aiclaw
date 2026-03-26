//go:build windows

package tools

import "os/exec"

func setCmdProcAttr(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
}
