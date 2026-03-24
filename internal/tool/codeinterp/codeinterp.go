package codeinterp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/workspace"
)

type codeParams struct {
	Language string `json:"language"`
	Code     string `json:"code"`
	Timeout  int    `json:"timeout"`
}

type codeResult struct {
	OK         bool   `json:"ok"`
	Language   string `json:"language"`
	File       string `json:"file"`
	ExitCode   int    `json:"exit_code"`
	Stdout     string `json:"stdout,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"duration_ms"`
}

type langRuntime struct {
	Command        string
	Extension      string
	DefaultTimeout int
}

var runtimes = map[string]langRuntime{
	"python":     {Command: "python3", Extension: ".py", DefaultTimeout: 60},
	"javascript": {Command: "node", Extension: ".js", DefaultTimeout: 60},
	"shell":      {Command: "sh", Extension: ".sh", DefaultTimeout: 60},
}

var dangerousCodePatterns = map[string][]string{
	"python": {
		"os.system(", "subprocess.call(", "subprocess.Popen(",
		"subprocess.run(", "__import__('os')", "__import__('subprocess')",
		"shutil.rmtree('/'", "shutil.rmtree(\"/\"",
		"open('/etc/", "open(\"/etc/",
	},
	"javascript": {
		"child_process", "require('fs').rm", "require(\"fs\").rm",
		"fs.rmSync('/'", "fs.rmSync(\"/\"",
		"process.exit(", "require('net')", "require(\"net\")",
	},
	"shell": {
		"rm -rf /", "rm -rf /*", "rm -rf ~", "mkfs", "dd if=",
		":(){:|:&};:", "> /dev/sda", "chmod -R 777 /",
		"shutdown", "reboot", "halt", "poweroff",
		"curl|sh", "wget|sh", "crontab -r",
	},
}

const maxOutput = 10_000
const maxTimeout = 120

func Handler(ctx context.Context, args string) (string, error) {
	var p codeParams
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return marshalResult(codeResult{OK: false, Error: "invalid parameters: " + err.Error()}), nil
	}

	p.Language = strings.TrimSpace(strings.ToLower(p.Language))
	p.Code = strings.TrimSpace(p.Code)

	if p.Code == "" {
		return marshalResult(codeResult{OK: false, Error: "code is required"}), nil
	}

	rt, ok := runtimes[p.Language]
	if !ok {
		return marshalResult(codeResult{
			OK:    false,
			Error: fmt.Sprintf("unsupported language %q, supported: python, javascript, shell", p.Language),
		}), nil
	}

	if _, err := exec.LookPath(rt.Command); err != nil {
		return marshalResult(codeResult{
			OK:       false,
			Language: p.Language,
			Error:    fmt.Sprintf("runtime %q not found, please install it first", rt.Command),
		}), nil
	}

	if err := checkDangerousCode(p.Language, p.Code); err != nil {
		log.WithFields(log.Fields{"language": p.Language, "reason": err}).Warn("[CodeInterpreter] code blocked by safety check")
		return marshalResult(codeResult{
			OK:       false,
			Language: p.Language,
			Error:    err.Error(),
		}), nil
	}

	timeout := rt.DefaultTimeout
	if p.Timeout > 0 {
		timeout = min(p.Timeout, maxTimeout)
	}

	sandboxDir := workspace.AgentSandboxFromCtx(ctx)
	if sandboxDir == "" {
		return marshalResult(codeResult{OK: false, Error: "workspace not initialized"}), nil
	}

	shortID := uuid.New().String()[:8]
	filename := "exec_" + shortID + rt.Extension
	filePath := filepath.Join(sandboxDir, filename)

	if err := os.WriteFile(filePath, []byte(p.Code), 0o644); err != nil {
		return marshalResult(codeResult{
			OK:       false,
			Language: p.Language,
			Error:    "write code file: " + err.Error(),
		}), nil
	}

	log.WithFields(log.Fields{
		"language": p.Language,
		"file":     filename,
		"timeout":  timeout,
	}).Info("[CodeInterpreter] >> executing code")

	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	start := time.Now()
	cmd := exec.CommandContext(execCtx, rt.Command, filePath)
	cmd.Dir = sandboxDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	duration := time.Since(start)

	result := codeResult{
		OK:         err == nil,
		Language:   p.Language,
		File:       filename,
		ExitCode:   cmd.ProcessState.ExitCode(),
		Stdout:     truncate(stdout.String(), maxOutput),
		Stderr:     truncate(stderr.String(), maxOutput),
		DurationMs: duration.Milliseconds(),
	}

	if err != nil {
		result.Error = err.Error()
		log.WithFields(log.Fields{
			"language": p.Language, "file": filename,
			"exit_code": result.ExitCode, "error": err,
		}).Warn("[CodeInterpreter] << execution failed")
	} else {
		log.WithFields(log.Fields{
			"language": p.Language, "file": filename,
			"duration_ms": result.DurationMs,
		}).Info("[CodeInterpreter] << execution ok")
	}

	return marshalResult(result), nil
}

func checkDangerousCode(language, code string) error {
	lower := strings.ToLower(code)
	patterns := dangerousCodePatterns[language]
	for _, p := range patterns {
		if strings.Contains(lower, strings.ToLower(p)) {
			return fmt.Errorf("dangerous code blocked: contains '%s'", p)
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

func marshalResult(r codeResult) string {
	b, _ := json.Marshal(r)
	return string(b)
}
