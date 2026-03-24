//go:build windows

package tool

import "os/exec"

func setCmdProcAttr(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
}
