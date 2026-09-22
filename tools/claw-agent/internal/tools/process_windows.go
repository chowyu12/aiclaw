//go:build windows

package tools

import "os/exec"

// Windows 上没有进程组的直接等价物（Job 对象要另外一套 API），
// 所以这里只靠 WaitDelay 兜底：进程退了还有人攥着管道时，
// Go 会在时限后自己关掉管道，Wait 不会无限期等下去。
func configureProcessGroup(*exec.Cmd) {}

func cancelByProcessGroup(*exec.Cmd) {}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
