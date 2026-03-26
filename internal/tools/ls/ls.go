package ls

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type lsParams struct {
	Path string `json:"path"`
}

func Handler(_ context.Context, args string) (string, error) {
	var p lsParams
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if p.Path == "" {
		p.Path = "."
	}

	targetPath := resolvePath(p.Path)

	info, err := os.Stat(targetPath)
	if err != nil {
		return "", fmt.Errorf("stat %q: %w", targetPath, err)
	}
	if !info.IsDir() {
		return formatEntry(targetPath, info), nil
	}

	entries, err := os.ReadDir(targetPath)
	if err != nil {
		return "", fmt.Errorf("read dir %q: %w", targetPath, err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Directory: %s (%d entries)\n\n", targetPath, len(entries)))

	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			sb.WriteString(fmt.Sprintf("  ? %s (error reading info)\n", entry.Name()))
			continue
		}
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		sb.WriteString(fmt.Sprintf("  %s %10d  %s  %s\n",
			info.Mode().String(),
			info.Size(),
			info.ModTime().Format("2006-01-02 15:04"),
			name,
		))
	}

	return sb.String(), nil
}

func formatEntry(path string, info os.FileInfo) string {
	return fmt.Sprintf("%s %10d  %s  %s\n",
		info.Mode().String(),
		info.Size(),
		info.ModTime().Format("2006-01-02 15:04"),
		filepath.Base(path),
	)
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
	return raw
}
