package result

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// absPathRe 匹配文本中形如 /foo/bar/baz.ext 的绝对路径（带扩展名）。
var absPathRe = regexp.MustCompile(`/(?:[^\s/]+/)*[^\s/]+\.[a-zA-Z0-9]{1,10}`)

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
	// 尝试从 JSON 结构（如 codeinterp 结果）的 stdout 字段中检测文件路径。
	var m map[string]any
	if json.Unmarshal([]byte(output), &m) == nil {
		if stdout, ok := m["stdout"].(string); ok && stdout != "" {
			if fr := detectFilePath(stdout); fr != nil {
				return fr
			}
		}
	}
	return nil
}

// detectFilePath 在文本中寻找指向磁盘上已存在文件的绝对路径。
// 先尝试将整个 trimmed 文本作为路径（单行快速路径），再逐行扫描，
// 最后用正则在文本中搜索嵌入的路径（如 "Saved to /tmp/out.csv"）。
func detectFilePath(output string) *FileResult {
	p := strings.TrimSpace(output)
	if p == "" {
		return nil
	}
	// 快速路径：整段文本就是单个绝对路径
	if !strings.Contains(p, "\n") && len(p) <= 500 && filepath.IsAbs(p) {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return makeFileResult(p)
		}
	}
	// 逐行扫描：每行本身是绝对路径
	for _, line := range strings.Split(p, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || len(line) > 500 || !filepath.IsAbs(line) {
			continue
		}
		if info, err := os.Stat(line); err == nil && !info.IsDir() {
			return makeFileResult(line)
		}
	}
	// 正则搜索：文本中嵌入的绝对路径（如 "文件已保存到 /Users/foo/out.csv"）
	for _, match := range absPathRe.FindAllString(p, -1) {
		if info, err := os.Stat(match); err == nil && !info.IsDir() {
			return makeFileResult(match)
		}
	}
	return nil
}

func makeFileResult(path string) *FileResult {
	return &FileResult{
		Type:        "file",
		Path:        path,
		MimeType:    MimeFromExt(filepath.Ext(path)),
		Description: fmt.Sprintf("File: %s", filepath.Base(path)),
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
