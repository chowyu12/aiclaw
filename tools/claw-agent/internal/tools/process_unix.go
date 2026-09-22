//go:build !windows

package tools

import (
	"os/exec"
	"syscall"
)

/*
进程组与收尾。

踩过的那次：模型跑了一条 `cd … && git fetch …`，git 挂在那儿等凭据。60 秒的
超时**确实触发了**，直接子进程（sandbox-exec / sh）也确实被杀了——但 git 是
**孙进程**，它活了下来（被 launchd 收养，PPID=1），而且**还攥着我们的 stdout
管道**。Go 的 `cmd.Run()` 要等输出管道关闭，于是那一步永远停在「执行中」，
界面上看是卡死，实际是我们在等一个已经没人管的进程。

两道一起上：
  - `Setpgid` 让命令自成一个进程组，取消时按**进程组**杀（负号 pid），
    孙子辈一起收；
  - `WaitDelay` 兜住漏网的：进程都退了还有人攥着管道时，Go 会在这个时限后
    自己关掉管道让 Wait 返回，而不是无限期等。
*/

func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// cancelByProcessGroup 让 ctx 取消时按进程组杀。
//
// **只能用在 CommandContext 建出来的命令上**：Go 对着没有 ctx 的命令设置
// Cancel 会直接报错（常驻会话的 shell 就是那种，它要活过单条命令）。
func cancelByProcessGroup(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// 负号 = 整个进程组。只杀 cmd.Process 的话，它拉起来的 git / npm
		// 会活下来，而且继续占着我们的管道。
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

// killProcessGroup 主动收掉一个已经起来的命令及其全部子孙。
func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		// 进程组没建起来（Start 失败之类）时退回只杀自己。
		_ = cmd.Process.Kill()
	}
}
