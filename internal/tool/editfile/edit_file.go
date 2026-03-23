package editfile

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chowyu12/aiclaw/internal/workspace"
)

type editParams struct {
	FilePath  string `json:"file_path"`
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
}

func Handler(_ context.Context, args string) (string, error) {
	var p editParams
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if p.FilePath == "" {
		return "", fmt.Errorf("file_path is required")
	}
	if p.OldString == "" {
		return "", fmt.Errorf("old_string is required")
	}

	targetPath := resolvePath(p.FilePath)

	data, err := os.ReadFile(targetPath)
	if err != nil {
		return "", fmt.Errorf("read %q: %w", targetPath, err)
	}
	content := string(data)

	count := strings.Count(content, p.OldString)
	if count == 0 {
		return "", fmt.Errorf("old_string not found in %q", targetPath)
	}
	if count > 1 {
		return "", fmt.Errorf("old_string matches %d occurrences in %q, must be unique (provide more context)", count, targetPath)
	}

	newContent := strings.Replace(content, p.OldString, p.NewString, 1)

	if err := os.WriteFile(targetPath, []byte(newContent), 0o644); err != nil {
		return "", fmt.Errorf("write %q: %w", targetPath, err)
	}

	return fmt.Sprintf("Edited %s: replaced 1 occurrence (%d bytes → %d bytes)", targetPath, len(data), len(newContent)), nil
}

func resolvePath(raw string) string {
	if strings.HasPrefix(raw, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, raw[2:])
		}
	}
	if filepath.IsAbs(raw) {
		return raw
	}
	if root := workspace.Root(); root != "" {
		return filepath.Join(root, raw)
	}
	return raw
}
