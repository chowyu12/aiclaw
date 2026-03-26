package grepfile

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type grepParams struct {
	Pattern    string `json:"pattern"`
	Path       string `json:"path"`
	Include    string `json:"include"`
	IgnoreCase bool   `json:"ignore_case"`
}

type match struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Content string `json:"content"`
}

const maxMatches = 200

func Handler(_ context.Context, args string) (string, error) {
	var p grepParams
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if p.Pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}
	if p.Path == "" {
		p.Path = "."
	}

	flags := ""
	if p.IgnoreCase {
		flags = "(?i)"
	}
	re, err := regexp.Compile(flags + p.Pattern)
	if err != nil {
		return "", fmt.Errorf("invalid regex %q: %w", p.Pattern, err)
	}

	info, err := os.Stat(p.Path)
	if err != nil {
		return "", fmt.Errorf("stat %q: %w", p.Path, err)
	}

	var matches []match
	if info.IsDir() {
		matches, err = searchDir(p.Path, re, p.Include)
	} else {
		matches, err = searchFile(p.Path, re)
	}
	if err != nil {
		return "", err
	}

	if len(matches) == 0 {
		return "No matches found.", nil
	}

	var sb strings.Builder
	truncated := false
	if len(matches) > maxMatches {
		matches = matches[:maxMatches]
		truncated = true
	}

	currentFile := ""
	for _, m := range matches {
		if m.File != currentFile {
			if currentFile != "" {
				sb.WriteString("\n")
			}
			sb.WriteString(m.File + ":\n")
			currentFile = m.File
		}
		sb.WriteString(fmt.Sprintf("  %d: %s\n", m.Line, m.Content))
	}

	if truncated {
		sb.WriteString(fmt.Sprintf("\n... (showing first %d matches, more exist)\n", maxMatches))
	}

	return sb.String(), nil
}

func searchDir(root string, re *regexp.Regexp, include string) ([]match, error) {
	var results []match
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
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
		if include != "" {
			matched, _ := filepath.Match(include, d.Name())
			if !matched {
				return nil
			}
		}
		if len(results) >= maxMatches {
			return filepath.SkipAll
		}
		m, _ := searchFile(path, re)
		results = append(results, m...)
		return nil
	})
	return results, err
}

func searchFile(path string, re *regexp.Regexp) ([]match, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()

	var results []match
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 512*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if re.MatchString(line) {
			results = append(results, match{
				File:    path,
				Line:    lineNo,
				Content: truncateLine(line, 200),
			})
		}
	}
	return results, nil
}

func truncateLine(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}
