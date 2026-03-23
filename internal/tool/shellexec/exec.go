package shellexec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/creack/pty"
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

	shell := findShell()
	cmd := exec.CommandContext(ctx, shell, "-c", p.Command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second
	if p.WorkingDir != "" {
		cmd.Dir = p.WorkingDir
	}

	log.WithFields(log.Fields{"command": p.Command, "timeout": timeout}).Info("[exec] >> run")

	output, exitCode, err := runWithPTY(cmd)

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

func runWithPTY(cmd *exec.Cmd) (string, int, error) {
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return runPipe(cmd)
	}
	defer ptmx.Close()

	var buf bytes.Buffer
	io.Copy(&buf, ptmx)

	err = cmd.Wait()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	return buf.String(), exitCode, err
}

func runPipe(cmd *exec.Cmd) (string, int, error) {
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}

	r := stdout.String()
	if stderr.Len() > 0 {
		r += "\n[stderr]\n" + stderr.String()
	}
	return r, exitCode, err
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
	return defaultShell
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
