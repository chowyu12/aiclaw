package readfile

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chowyu12/aiclaw/internal/tools/result"
	"github.com/chowyu12/aiclaw/internal/workspace"
)

type readParams struct {
	FilePath string `json:"file_path"`
	Offset   int    `json:"offset"`
	Limit    int    `json:"limit"`
}

const maxReadBytes = 256 * 1024

func Handler(_ context.Context, args string) (string, error) {
	var p readParams
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if p.FilePath == "" {
		return "", fmt.Errorf("file_path is required")
	}

	targetPath := resolvePath(p.FilePath)

	info, err := os.Stat(targetPath)
	if err != nil {
		return "", fmt.Errorf("stat %q: %w", targetPath, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%q is a directory, use ls tool instead", targetPath)
	}

	if mime := result.MimeFromExt(filepath.Ext(targetPath)); strings.HasPrefix(mime, "image/") {
		return result.NewFileResult(targetPath, mime, fmt.Sprintf("Image file: %s (%d bytes)", filepath.Base(targetPath), info.Size())), nil
	}

	f, err := os.Open(targetPath)
	if err != nil {
		return "", fmt.Errorf("open %q: %w", targetPath, err)
	}
	defer f.Close()

	if p.Offset <= 0 && p.Limit <= 0 {
		if info.Size() > maxReadBytes {
			return "", fmt.Errorf("file too large (%d bytes), use offset/limit to read a portion", info.Size())
		}
		data, err := os.ReadFile(targetPath)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}

	offset := max(p.Offset, 1)
	limit := p.Limit
	if limit <= 0 {
		limit = 200
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var sb strings.Builder
	lineNo := 0
	collected := 0
	for scanner.Scan() {
		lineNo++
		if lineNo < offset {
			continue
		}
		if collected >= limit {
			break
		}
		sb.WriteString(fmt.Sprintf("%6d|%s\n", lineNo, scanner.Text()))
		collected++
	}

	if err := scanner.Err(); err != nil {
		return sb.String(), fmt.Errorf("read error: %w", err)
	}

	return sb.String(), nil
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
