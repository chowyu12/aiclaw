//go:build !windows

package shellexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

const defaultShell = "/bin/bash"

func buildShellCommand(ctx context.Context, shell, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, shell, "-c", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = AugmentEnv(os.Environ())
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	return cmd
}

// AugmentEnv 在当前环境变量基础上，补齐常见的用户态 bin 路径到 PATH。
// 使用 `bash -c` 启动的是非交互、非登录 shell，不会加载 ~/.bash_profile / ~/.zshrc
// / /etc/paths，因此 Homebrew（/opt/homebrew/bin、/usr/local/bin）以及 ~/.local/bin
// / ~/.cargo/bin 等用户装的 CLI 常常在 aiclaw 子进程里找不到。这里把这些常见目录
// 并入 PATH，目录不存在会被过滤掉，无副作用。
func AugmentEnv(env []string) []string {
	extras := []string{
		"/opt/homebrew/bin", "/opt/homebrew/sbin",
		"/usr/local/bin", "/usr/local/sbin",
		"/usr/bin", "/bin", "/usr/sbin", "/sbin",
	}
	if runtime.GOOS == "darwin" {
		extras = append(extras, "/opt/local/bin", "/opt/local/sbin") // MacPorts
	}
	if home, err := os.UserHomeDir(); err == nil {
		extras = append(extras,
			filepath.Join(home, ".local/bin"),
			filepath.Join(home, "bin"),
			filepath.Join(home, ".cargo/bin"),
			filepath.Join(home, "go/bin"),
		)
	}

	var out []string
	pathFound := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			newPath := mergePath(strings.TrimPrefix(kv, "PATH="), extras)
			out = append(out, "PATH="+newPath)
			pathFound = true
		} else {
			out = append(out, kv)
		}
	}
	if !pathFound {
		out = append(out, "PATH="+mergePath("/usr/bin:/bin:/usr/sbin:/sbin", extras))
	}
	return out
}

// mergePath 将 extras 中已存在的目录追加到 current PATH（去重、保序）。
func mergePath(current string, extras []string) string {
	seen := make(map[string]bool)
	var parts []string
	for _, p := range strings.Split(current, ":") {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		parts = append(parts, p)
	}
	for _, p := range extras {
		if p == "" || seen[p] {
			continue
		}
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			continue
		}
		seen[p] = true
		parts = append(parts, p)
	}
	return strings.Join(parts, ":")
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
