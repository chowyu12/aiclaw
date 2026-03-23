package skill

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type RunInput struct {
	Tool      string         `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
	Config    map[string]any `json:"config,omitzero"`
}

func RunTool(ctx context.Context, skillDir, mainFile, toolName string, argsJSON string, config map[string]any, timeout time.Duration) (string, error) {
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ext := strings.ToLower(filepath.Ext(mainFile))
	var cmdName string
	switch ext {
	case ".js":
		cmdName = "node"
	case ".py":
		cmdName = "python3"
	default:
		return "", fmt.Errorf("unsupported main file extension: %s", ext)
	}

	input := RunInput{
		Tool:   toolName,
		Config: config,
	}
	input.Arguments = json.RawMessage(argsJSON)

	inputJSON, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("marshal input: %w", err)
	}

	mainPath := filepath.Join(skillDir, mainFile)
	cmd := exec.CommandContext(ctx, cmdName, mainPath)
	cmd.Dir = skillDir
	cmd.Stdin = bytes.NewReader(inputJSON)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errOutput := stderr.String()
		if errOutput != "" {
			return "", fmt.Errorf("run %s: %w\nstderr: %s", mainFile, err, errOutput)
		}
		return "", fmt.Errorf("run %s: %w", mainFile, err)
	}

	return strings.TrimSpace(stdout.String()), nil
}
