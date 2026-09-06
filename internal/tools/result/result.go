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
var inlineCodeRe = regexp.MustCompile("`([^`\\n]+)`")

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
	items := ParseFileResults(output)
	if len(items) > 0 {
		return &items[0]
	}
	return nil
}

// ParseFileResults extracts every unique local file reference from a tool
// result or assistant message. Structured file results are retained even when
// the file has since moved, while free-form paths are included only when they
// currently resolve to regular files.
func ParseFileResults(output string) []FileResult {
	results := make([]FileResult, 0, 1)
	seen := make(map[string]struct{})
	add := func(item FileResult, requireExisting bool) {
		item.Path = strings.TrimSpace(strings.TrimPrefix(item.Path, "file://"))
		if strings.HasPrefix(item.Path, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				item.Path = filepath.Join(home, item.Path[2:])
			}
		}
		if item.Path == "" || !filepath.IsAbs(item.Path) {
			return
		}
		item.Path = filepath.Clean(item.Path)
		if requireExisting {
			info, err := os.Stat(item.Path)
			if err != nil || info.IsDir() {
				return
			}
		}
		if _, ok := seen[item.Path]; ok {
			return
		}
		seen[item.Path] = struct{}{}
		if item.Type == "" {
			item.Type = "file"
		}
		if item.MimeType == "" {
			item.MimeType = MimeFromExt(filepath.Ext(item.Path))
		}
		if item.Description == "" {
			item.Description = fmt.Sprintf("File: %s", filepath.Base(item.Path))
		}
		results = append(results, item)
	}

	var r FileResult
	if json.Unmarshal([]byte(output), &r) == nil && r.Type == "file" && r.Path != "" {
		add(r, false)
	}
	// 尝试从 JSON 结构（如 codeinterp 结果）的 stdout 字段中检测文件路径。
	var m map[string]any
	if json.Unmarshal([]byte(output), &m) == nil {
		if stdout, ok := m["stdout"].(string); ok && stdout != "" {
			for _, item := range detectFilePaths(stdout) {
				add(item, true)
			}
		}
	}
	for _, item := range detectFilePaths(output) {
		add(item, true)
	}
	return results
}

// detectFilePath 在文本中寻找指向磁盘上已存在文件的绝对路径。
// 先尝试将整个 trimmed 文本作为路径（单行快速路径），再逐行扫描，
// 最后用正则在文本中搜索嵌入的路径（如 "Saved to /tmp/out.csv"）。
func detectFilePath(output string) *FileResult {
	items := detectFilePaths(output)
	if len(items) > 0 {
		return &items[0]
	}
	return nil
}

func detectFilePaths(output string) []FileResult {
	p := strings.TrimSpace(output)
	if p == "" {
		return nil
	}
	results := make([]FileResult, 0, 1)
	seen := make(map[string]struct{})
	add := func(path string) {
		path = strings.TrimSpace(strings.TrimPrefix(path, "file://"))
		if strings.HasPrefix(path, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				path = filepath.Join(home, path[2:])
			}
		}
		if !filepath.IsAbs(path) {
			return
		}
		path = filepath.Clean(path)
		if _, ok := seen[path]; ok {
			return
		}
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			seen[path] = struct{}{}
			results = append(results, *makeFileResult(path))
		}
	}
	// 快速路径：整段文本就是单个绝对路径
	if !strings.Contains(p, "\n") && len(p) <= 500 && filepath.IsAbs(p) {
		add(p)
	}
	// Markdown inline code preserves paths containing spaces.
	for _, match := range inlineCodeRe.FindAllStringSubmatch(p, -1) {
		if len(match) == 2 {
			add(match[1])
		}
	}
	// 逐行扫描：每行本身是绝对路径
	for _, line := range strings.Split(p, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || len(line) > 500 || !filepath.IsAbs(line) {
			continue
		}
		add(line)
	}
	// 正则搜索：文本中嵌入的绝对路径（如 "文件已保存到 /Users/foo/out.csv"）
	for _, match := range absPathRe.FindAllString(p, -1) {
		add(strings.TrimRight(match, ".,;:!?)，。；：！）》】"))
	}
	return results
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
	case ".doc", ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".xls", ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".ppt", ".pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
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
