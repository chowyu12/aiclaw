package writefile

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chowyu12/aiclaw/internal/tools/result"
	"github.com/chowyu12/aiclaw/internal/workspace"
)

type writeParams struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
	Append   bool   `json:"append"`
}

func Handler(ctx context.Context, args string) (string, error) {
	var p writeParams
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if p.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	if p.Content == "" {
		return "", fmt.Errorf("content is required")
	}

	targetPath := resolvePath(ctx, p.Path)

	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return "", fmt.Errorf("create parent directory: %w", err)
	}

	flag := os.O_WRONLY | os.O_CREATE
	if p.Append {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}

	f, err := os.OpenFile(targetPath, flag, 0o644)
	if err != nil {
		return "", fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	n, err := f.WriteString(p.Content)
	if err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	mime := result.MimeFromExt(filepath.Ext(targetPath))
	return result.NewFileResult(targetPath, mime, fmt.Sprintf("Wrote %d bytes to %s", n, targetPath)), nil
}

func resolvePath(ctx context.Context, raw string) string {
	if strings.HasPrefix(raw, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, raw[2:])
		}
	}
	if filepath.IsAbs(raw) {
		return raw
	}
	sandboxDir := workspace.AgentSandboxFromCtx(ctx)
	return filepath.Join(sandboxDir, raw)
}
