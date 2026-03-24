package findfile

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type findParams struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
}

const maxResults = 500

func Handler(_ context.Context, args string) (string, error) {
	var p findParams
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if p.Pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}
	if p.Path == "" {
		p.Path = "."
	}

	info, err := os.Stat(p.Path)
	if err != nil {
		return "", fmt.Errorf("stat %q: %w", p.Path, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%q is not a directory", p.Path)
	}

	var results []string
	hasDoubleStar := strings.Contains(p.Pattern, "**")

	err = filepath.WalkDir(p.Path, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == ".svn" || name == "__pycache__" {
				return filepath.SkipDir
			}
			return nil
		}
		if len(results) >= maxResults {
			return filepath.SkipAll
		}

		var matched bool
		if hasDoubleStar {
			matched = doubleStarMatch(p.Pattern, path)
		} else {
			matched, _ = filepath.Match(p.Pattern, d.Name())
		}

		if matched {
			results = append(results, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	if len(results) == 0 {
		return "No files found.", nil
	}

	var sb strings.Builder
	for _, r := range results {
		sb.WriteString(r + "\n")
	}
	if len(results) >= maxResults {
		sb.WriteString(fmt.Sprintf("\n... (showing first %d results, more may exist)\n", maxResults))
	}
	return sb.String(), nil
}

func doubleStarMatch(pattern, path string) bool {
	parts := strings.Split(pattern, "**")
	if len(parts) == 2 {
		prefix := parts[0]
		suffix := parts[1]
		if suffix != "" && suffix[0] == '/' {
			suffix = suffix[1:]
		}
		if prefix != "" {
			if !strings.HasPrefix(path, prefix) {
				return false
			}
		}
		if suffix != "" {
			matched, _ := filepath.Match(suffix, filepath.Base(path))
			return matched
		}
		return true
	}
	matched, _ := filepath.Match(pattern, filepath.Base(path))
	return matched
}
