package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/tools/shellexec"
)

// extraCmdHandlerRules 是除 shellexec.CheckDangerousCommand 外，command 工具额外的保守规则。
// command 工具常用于对外暴露的 HTTP → shell 桥接，限制应比本地 exec 更严，
// 如禁止任意 ssh/scp/sftp/telnet、远程 shell、裸 rm -rf 等。
var extraCmdHandlerRules = []struct {
	id string
	rx *regexp.Regexp
}{
	{"outbound_shell", regexp.MustCompile(`(?i)\b(?:ssh|scp|sftp|telnet)\b`)},
	{"listen_shell", regexp.MustCompile(`(?i)\b(?:nc|ncat)\b\s+(?:-\w*\s+)*-l\b`)},
	{"eval_or_source", regexp.MustCompile(`(?i)(?:^|[\s;|&])(?:eval|source|\.)\s+`)},
	{"rm_recursive", regexp.MustCompile(`(?i)\brm\s+(?:-[a-z]*[rRdf][a-z]*\s+)+`)},
}

func checkDangerousCommand(cmdStr string) error {
	if err := shellexec.CheckDangerousCommand(cmdStr); err != nil {
		return err
	}
	for _, r := range extraCmdHandlerRules {
		if r.rx.MatchString(cmdStr) {
			return fmt.Errorf("dangerous command blocked: matched rule %q", r.id)
		}
	}
	return nil
}

func NewCommandHandler(cfg model.CommandHandlerConfig, timeoutSec int) func(context.Context, string) (string, error) {
	return func(ctx context.Context, input string) (string, error) {
		return commandToolHandler(ctx, cfg, timeoutSec, input)
	}
}

func commandToolHandler(ctx context.Context, cfg model.CommandHandlerConfig, timeoutSec int, input string) (string, error) {
	cmdStr := cfg.Command

	var params map[string]any
	if input != "" {
		json.Unmarshal([]byte(input), &params)
	}
	for key, val := range params {
		cmdStr = strings.ReplaceAll(cmdStr, "{"+key+"}", fmt.Sprint(val))
	}

	if err := checkDangerousCommand(cmdStr); err != nil {
		log.WithFields(log.Fields{"command": cmdStr, "reason": err}).Warn("[Tool] !! command blocked by safety check")
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	shell := cfg.Shell
	if shell == "" {
		shell = "/bin/sh"
	}

	cmd := exec.CommandContext(ctx, shell, "-c", cmdStr)
	setCmdProcAttr(cmd)
	cmd.WaitDelay = 5 * time.Second
	if cfg.WorkingDir != "" {
		cmd.Dir = cfg.WorkingDir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	log.WithFields(log.Fields{"command": cmdStr, "shell": shell, "timeout": timeoutSec}).Info("[Tool] >> exec command")
	err := cmd.Run()

	r := stdout.String()
	if stderr.Len() > 0 {
		r += "\n[stderr]\n" + stderr.String()
	}

	const maxOutput = 10_000
	if len(r) > maxOutput {
		r = r[:maxOutput] + "\n... (output truncated)"
	}

	if err != nil {
		log.WithFields(log.Fields{"command": cmdStr, "error": err}).Warn("[Tool] << command failed")
		return r, fmt.Errorf("command failed: %w", err)
	}

	log.WithField("command", cmdStr).Info("[Tool] << command ok")
	return r, nil
}
