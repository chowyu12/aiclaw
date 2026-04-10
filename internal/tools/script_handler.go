package tools

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

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/workspace"
)

type scriptRuntime struct {
	Command   string
	Extension string
}

var scriptRuntimes = map[string]scriptRuntime{
	"python":     {Command: "python3", Extension: ".py"},
	"javascript": {Command: "node", Extension: ".js"},
	"shell":      {Command: "sh", Extension: ".sh"},
	"go":         {Command: "go", Extension: ".go"},
}

func NewScriptHandler(cfg model.ScriptHandlerConfig, timeoutSec int) func(context.Context, string) (string, error) {
	return func(ctx context.Context, input string) (string, error) {
		return scriptToolHandler(ctx, cfg, timeoutSec, input)
	}
}

func scriptToolHandler(ctx context.Context, cfg model.ScriptHandlerConfig, timeoutSec int, input string) (string, error) {
	lang := strings.TrimSpace(strings.ToLower(cfg.Language))
	rt, ok := scriptRuntimes[lang]
	if !ok {
		return "", fmt.Errorf("unsupported script language %q, supported: python, javascript, shell, go", lang)
	}

	if _, err := exec.LookPath(rt.Command); err != nil {
		return "", fmt.Errorf("runtime %q not found, please install it first", rt.Command)
	}

	content := cfg.Content
	var params map[string]any
	if input != "" {
		json.Unmarshal([]byte(input), &params)
	}
	for key, val := range params {
		content = strings.ReplaceAll(content, "{"+key+"}", fmt.Sprint(val))
	}

	sandboxDir := workspace.AgentSandboxFromCtx(ctx)
	if sandboxDir == "" {
		return "", fmt.Errorf("workspace not initialized")
	}

	shortID := uuid.New().String()[:8]
	filename := "script_" + shortID + rt.Extension
	filePath := filepath.Join(sandboxDir, filename)

	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write script file: %w", err)
	}
	defer os.Remove(filePath)

	if timeoutSec <= 0 {
		timeoutSec = model.DefaultToolTimeout
	}
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if lang == "go" {
		cmd = exec.CommandContext(execCtx, "go", "run", filePath)
	} else {
		cmd = exec.CommandContext(execCtx, rt.Command, filePath)
	}
	cmd.Dir = sandboxDir
	setCmdProcAttr(cmd)
	cmd.WaitDelay = 5 * time.Second

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	log.WithFields(log.Fields{
		"language": lang, "file": filename, "timeout": timeoutSec,
	}).Info("[ScriptTool] >> executing")

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
		log.WithFields(log.Fields{"language": lang, "file": filename, "error": err}).Warn("[ScriptTool] << execution failed")
		return r, fmt.Errorf("script execution failed: %w", err)
	}

	log.WithFields(log.Fields{"language": lang, "file": filename}).Info("[ScriptTool] << execution ok")
	return r, nil
}
