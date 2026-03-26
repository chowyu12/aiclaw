package result

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type FileResult struct {
	Type        string `json:"__type"`
	Path        string `json:"path"`
	MimeType    string `json:"mime"`
	Description string `json:"description"`
}

func NewFileResult(filePath, mimeType, description string) string {
	data, _ := json.Marshal(FileResult{
		Type:        "file",
		Path:        filePath,
		MimeType:    mimeType,
		Description: description,
	})
	return string(data)
}

func ParseFileResult(output string) *FileResult {
	var r FileResult
	if json.Unmarshal([]byte(output), &r) == nil && r.Type == "file" && r.Path != "" {
		return &r
	}
	if fr := detectFilePath(output); fr != nil {
		return fr
	}
	return nil
}

func detectFilePath(output string) *FileResult {
	p := strings.TrimSpace(output)
	if p == "" || strings.Contains(p, "\n") || len(p) > 500 {
		return nil
	}
	if !filepath.IsAbs(p) {
		return nil
	}
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return nil
	}
	return &FileResult{
		Type:        "file",
		Path:        p,
		MimeType:    MimeFromExt(filepath.Ext(p)),
		Description: fmt.Sprintf("File: %s", filepath.Base(p)),
	}
}

func MimeFromExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".txt", ".log":
		return "text/plain"
	case ".json":
		return "application/json"
	case ".csv":
		return "text/csv"
	case ".html", ".htm":
		return "text/html"
	case ".xml":
		return "application/xml"
	case ".md":
		return "text/markdown"
	case ".pdf":
		return "application/pdf"
	default:
		return "application/octet-stream"
	}
}

func ExtractJSONField(jsonStr, field string) string {
	var m map[string]any
	if json.Unmarshal([]byte(jsonStr), &m) != nil {
		return jsonStr
	}
	v, ok := m[field]
	if !ok {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	default:
		b, _ := json.Marshal(val)
		return string(b)
	}
}
