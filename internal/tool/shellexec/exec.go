package shellexec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/chowyu12/aiclaw/internal/workspace"
	log "github.com/sirupsen/logrus"
)

type execParams struct {
	Command    string `json:"command"`
	Timeout    int    `json:"timeout"`
	WorkingDir string `json:"working_dir"`
}

var dangerousPatterns = []string{
	"rm -rf /", "rm -rf /*", "rm -rf ~", "mkfs", "dd if=", ":(){:|:&};:",
	"> /dev/sda", "chmod -R 777 /", "chown -R", "shutdown", "reboot",
	"halt", "poweroff", "init 0", "init 6", "kill -9 1",
	"ssh-keygen", "useradd", "userdel", "usermod", "passwd",
	"visudo", "iptables -F", "iptables -X", "nft flush", "crontab -r",
	"systemctl disable", "> /etc/", "tee /etc/",
	"mount ", "umount ", "fdisk ", "parted ", "wipefs",
}

const (
	maxOutput    = 64_000
	maxTimeout   = 300
	defaultShell = "/bin/bash"
)

func Handler(ctx context.Context, args string) (string, error) {
	var p execParams
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if p.Command == "" {
		return "", fmt.Errorf("command is required")
	}

	if err := checkDangerous(p.Command); err != nil {
		return "", err
	}

	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 30
	}
	timeout = min(timeout, maxTimeout)

	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	log.WithFields(log.Fields{"command": p.Command, "timeout": timeout}).Info("[exec] >> run")

	workDir := resolveWorkingDir(p.WorkingDir)
	if workDir == "" {
		workDir = workspace.Root()
	}
	output, exitCode, err := runWithShellFallback(ctx, p.Command, workDir)

	r := truncate(output, maxOutput)

	if err != nil {
		log.WithFields(log.Fields{"command": p.Command, "exit_code": exitCode, "error": err}).Warn("[exec] << failed")
		if r != "" {
			r += "\n"
		}
		r += fmt.Sprintf("[exit_code: %d]", exitCode)
		return r, nil
	}

	log.WithField("command", p.Command).Info("[exec] << ok")
	return r, nil
}

func runWithShellFallback(ctx context.Context, command, workDir string) (string, int, error) {
	candidates := shellCandidates()
	var lastErr error
	for _, shell := range candidates {
		cmd := exec.CommandContext(ctx, shell, "-c", command)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error {
			if cmd.Process == nil {
				return nil
			}
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		cmd.WaitDelay = 5 * time.Second
		if workDir != "" {
			cmd.Dir = workDir
		}

		output, exitCode, err := runPipe(cmd)
		if err == nil {
			return output, exitCode, nil
		}
		lastErr = err
		if !isSpawnENOENT(err) {
			return output, exitCode, err
		}
		log.WithFields(log.Fields{"shell": shell, "error": err}).Debug("[exec] shell unavailable, trying next")
	}
	if lastErr != nil {
		return "", -1, fmt.Errorf("no usable shell: %w", lastErr)
	}
	return "", -1, fmt.Errorf("no usable shell found")
}

func runPipe(cmd *exec.Cmd) (string, int, error) {
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	} else if err != nil {
		exitCode = -1
	}

	r := stdout.String()
	if stderr.Len() > 0 {
		r += "\n[stderr]\n" + stderr.String()
	}
	return r, exitCode, err
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

	// Prefer widely available POSIX shells first.
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

func resolveWorkingDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	if dir == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return dir
	}
	if strings.HasPrefix(dir, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(dir, "~/"))
		}
	}
	return dir
}

func checkDangerous(cmdStr string) error {
	lower := strings.ToLower(strings.TrimSpace(cmdStr))
	for _, p := range dangerousPatterns {
		if strings.Contains(lower, strings.ToLower(p)) {
			return fmt.Errorf("dangerous command blocked: contains '%s'", p)
		}
	}
	return nil
}

func truncate(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen] + "\n... (output truncated)"
	}
	return s
}
