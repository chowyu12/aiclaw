package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/chowyu12/aiclaw/internal/i18n"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

// 单个文件读取上限。超出就截断并告知模型，而不是把几十 MB 塞进上下文。
const maxReadBytes = 256 * 1024

// scopeOf 给出这次审批可以按目录批准的范围。
//
// 给目录而不是文件：用户想批准的是「往这儿写东西」，按文件批的话
// 下一个文件又要问一次，等于没批。工作区内本来就不问，没有范围可言。
func scopeOf(inside bool, path string) string {
	if inside {
		return ""
	}
	return filepath.Dir(path)
}

// writeEffect 选一档副作用：工作区里面的写不问，外面的问一句。
func writeEffect(inside bool) Effect {
	if inside {
		return EffectWrite
	}
	return EffectWriteOutside
}

// outsideReason 给审批框一句人话，说清「为什么这次要问」。
//
// 不写清楚的话，用户看到的是一个突然冒出来的确认框，而他刚刚被告知
// 「只有危险操作才会问」。
func outsideReason(env *Env, inside bool) string {
	if inside {
		return ""
	}
	if strings.TrimSpace(env.Workspace) == "" {
		return i18n.D("这个会话没有设置工作区，所有写入都会先问一句")
	}
	return i18n.D("这个路径在会话工作区（{workspace}）之外", "workspace", env.Workspace)
}

// RegisterFileTools 登记文件类内置工具。
func RegisterFileTools(registry *Registry) error {
	for _, tool := range []Tool{
		readTool(), writeTool(), editTool(), listTool(), grepTool(),
		viewImageTool(), currentTimeTool(),
	} {
		if err := registry.Register(tool); err != nil {
			return err
		}
	}
	return nil
}

func readTool() Tool {
	return Tool{
		Name: "read_file",
		Description: "Read a file's contents. Files outside the workspace can be read too (except directories that hold credentials). " +
			"Large files are truncated.",
		Effect: EffectRead,
		Schema: schema(map[string]any{
			"path": map[string]any{
				"type": "string", "description": "File path. Relative paths resolve against the workspace; absolute paths also work",
			},
			"offset": map[string]any{
				"type": "integer", "description": "Line to start reading from, 1-based; omit to read from the beginning", "minimum": 1,
			},
			"limit": map[string]any{
				"type": "integer", "description": "Maximum number of lines to read", "minimum": 1,
			},
		}, "path"),
		Handler: func(_ context.Context, raw json.RawMessage, env *Env) (string, error) {
			var args struct {
				Path   string `json:"path"`
				Offset int    `json:"offset"`
				Limit  int    `json:"limit"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			path, err := env.ResolveRead(args.Path)
			if err != nil {
				return "", err
			}
			info, err := os.Stat(path)
			if err != nil {
				return "", fmt.Errorf("%s: %w", i18n.D("读取失败"), err)
			}
			if info.IsDir() {
				return "", i18n.E("{path} 是目录，不是文件；用 list_dir 看目录内容", "path", args.Path)
			}
			// Office 文件是 zip 包，按文本读出来只是一串乱码，还白占上下文。
			// 直接指到 read_office；老格式则直接说清读不了。
			if kind, legacy := officeKind(path); kind != "" {
				if legacy {
					return "", legacyOfficeError(path)
				}
				return "", i18n.E("{path} 是 Office 文件（压缩包格式），read_file 读不出文字；请改用 read_office", "path", args.Path)
			}

			content, err := os.ReadFile(path)
			if err != nil {
				return "", fmt.Errorf("%s: %w", i18n.D("读取失败"), err)
			}
			truncated := false
			if len(content) > maxReadBytes {
				content = content[:maxReadBytes]
				truncated = true
			}

			text := string(content)
			if args.Offset > 0 || args.Limit > 0 {
				lines := strings.Split(text, "\n")
				start := 0
				if args.Offset > 0 {
					start = args.Offset - 1
				}
				if start >= len(lines) {
					return "", i18n.E("offset {offset} 超出文件行数 {lines}", "offset", args.Offset, "lines", len(lines))
				}
				end := len(lines)
				if args.Limit > 0 && start+args.Limit < end {
					end = start + args.Limit
					truncated = true
				}
				text = strings.Join(lines[start:end], "\n")
			}
			if truncated {
				text += "\n\n" + i18n.D("[内容已截断]")
			}
			return text, nil
		},
	}
}

func writeTool() Tool {
	return Tool{
		Name:        "write_file",
		Description: "Write content to a file, overwriting what is there. Parent directories are created automatically. Writing outside the workspace asks the user for confirmation first.",
		Effect:      EffectWrite,
		Schema: schema(map[string]any{
			"path":    map[string]any{"type": "string", "description": "File path. Relative paths resolve against the workspace"},
			"content": map[string]any{"type": "string", "description": "The complete file content"},
		}, "path", "content"),
		Handler: func(ctx context.Context, raw json.RawMessage, env *Env) (string, error) {
			var args struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			path, inside, err := env.ResolveWrite(args.Path)
			if err != nil {
				return "", err
			}
			if err := env.requestApprovalScoped(
				ctx, writeEffect(inside), protocol.ApprovalWrite,
				i18n.D("写入文件"), path, outsideReason(env, inside), scopeOf(inside, path),
			); err != nil {
				return "", err
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return "", fmt.Errorf("%s: %w", i18n.D("创建父目录失败"), err)
			}
			if err := os.WriteFile(path, []byte(args.Content), 0o644); err != nil {
				return "", fmt.Errorf("%s: %w", i18n.D("写入失败"), err)
			}
			return i18n.D("已写入 {path}（{bytes} 字节）", "path", args.Path, "bytes", len(args.Content)), nil
		},
	}
}

func editTool() Tool {
	return Tool{
		Name: "edit_file",
		Description: "Replace an exact piece of text in a file with new text. " +
			"old_text must appear exactly once in the file, otherwise the call fails — so the wrong spot never gets edited.",
		Effect: EffectWrite,
		Schema: schema(map[string]any{
			"path":     map[string]any{"type": "string", "description": "File path. Relative paths resolve against the workspace"},
			"old_text": map[string]any{"type": "string", "description": "The original text to replace; must be unique"},
			"new_text": map[string]any{"type": "string", "description": "The replacement text"},
		}, "path", "old_text", "new_text"),
		Handler: func(ctx context.Context, raw json.RawMessage, env *Env) (string, error) {
			var args struct {
				Path    string `json:"path"`
				OldText string `json:"old_text"`
				NewText string `json:"new_text"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			if args.OldText == "" {
				return "", i18n.E("old_text 不能为空；要写全新内容用 write_file")
			}
			path, inside, err := env.ResolveWrite(args.Path)
			if err != nil {
				return "", err
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return "", fmt.Errorf("%s: %w", i18n.D("读取失败"), err)
			}
			text := string(content)
			count := strings.Count(text, args.OldText)
			if count == 0 {
				return "", i18n.E("文件里找不到 old_text；先用 read_file 确认原文（注意空白与缩进）")
			}
			if count > 1 {
				return "", i18n.E("old_text 在文件里出现了 {count} 次，无法确定改哪一处；请带上更多上下文使其唯一", "count", count)
			}
			if err := env.requestApprovalScoped(
				ctx, writeEffect(inside), protocol.ApprovalWrite,
				i18n.D("修改文件"), path, outsideReason(env, inside), scopeOf(inside, path),
			); err != nil {
				return "", err
			}
			updated := strings.Replace(text, args.OldText, args.NewText, 1)
			if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
				return "", fmt.Errorf("%s: %w", i18n.D("写入失败"), err)
			}
			return i18n.D("已修改 {path}", "path", args.Path), nil
		},
	}
}

func listTool() Tool {
	return Tool{
		Name:        "list_dir",
		Description: "List the entries of a directory. Directories outside the workspace can be listed too (except directories that hold credentials).",
		Effect:      EffectRead,
		Schema: schema(map[string]any{
			"path": map[string]any{"type": "string", "description": "Directory path; omit to list the workspace root (the home directory when no workspace is set)"},
		}),
		Handler: func(_ context.Context, raw json.RawMessage, env *Env) (string, error) {
			var args struct {
				Path string `json:"path"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			if strings.TrimSpace(args.Path) == "" {
				args.Path = "."
			}
			path, err := env.ResolveRead(args.Path)
			if err != nil {
				return "", err
			}
			entries, err := os.ReadDir(path)
			if err != nil {
				return "", fmt.Errorf("%s: %w", i18n.D("列目录失败"), err)
			}
			if len(entries) == 0 {
				return i18n.D("（空目录）"), nil
			}
			lines := make([]string, 0, len(entries))
			for _, entry := range entries {
				name := entry.Name()
				if entry.IsDir() {
					lines = append(lines, name+"/")
					continue
				}
				info, err := entry.Info()
				if err != nil {
					lines = append(lines, name)
					continue
				}
				lines = append(lines, name+"\t"+i18n.D("{bytes} 字节", "bytes", info.Size()))
			}
			sort.Strings(lines)
			return strings.Join(lines, "\n"), nil
		},
	}
}

func grepTool() Tool {
	return Tool{
		Name:        "search_files",
		Description: "Search file contents with a regular expression; returns matching lines with their file and line number. Searches the workspace by default; another directory can be given.",
		Effect:      EffectRead,
		Schema: schema(map[string]any{
			"pattern": map[string]any{"type": "string", "description": "Go regular expression"},
			"path":    map[string]any{"type": "string", "description": "Directory to limit the search to; omit to search the whole workspace"},
			"glob":    map[string]any{"type": "string", "description": "File name glob, e.g. *.go"},
			"max_results": map[string]any{
				"type": "integer", "description": "Maximum number of matches to return, default 100", "minimum": 1, "maximum": 1000,
			},
		}, "pattern"),
		Handler: func(_ context.Context, raw json.RawMessage, env *Env) (string, error) {
			var args struct {
				Pattern    string `json:"pattern"`
				Path       string `json:"path"`
				Glob       string `json:"glob"`
				MaxResults int    `json:"max_results"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			expression, err := regexp.Compile(args.Pattern)
			if err != nil {
				return "", fmt.Errorf("%s: %w", i18n.D("正则不合法"), err)
			}
			if strings.TrimSpace(args.Path) == "" {
				args.Path = "."
			}
			root, err := env.ResolveRead(args.Path)
			if err != nil {
				return "", err
			}
			if args.MaxResults <= 0 {
				args.MaxResults = 100
			}

			var matches []string
			walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
				if err != nil {
					return nil // 单个目录读不了就跳过，不中断整次搜索
				}
				if entry.IsDir() {
					// 这些目录搜出来的东西对模型没用，还会把结果淹掉。
					switch entry.Name() {
					case ".git", "node_modules", "vendor", "dist", "build", ".venv", "__pycache__":
						return filepath.SkipDir
					}
					return nil
				}
				if len(matches) >= args.MaxResults {
					return filepath.SkipAll
				}
				if args.Glob != "" {
					ok, matchErr := filepath.Match(args.Glob, entry.Name())
					if matchErr != nil || !ok {
						return nil
					}
				}
				info, err := entry.Info()
				if err != nil || info.Size() > maxReadBytes {
					return nil
				}
				content, err := os.ReadFile(path)
				if err != nil || isBinary(content) {
					return nil
				}
				relative, relErr := filepath.Rel(root, path)
				if relErr != nil {
					relative = path
				}
				for index, line := range strings.Split(string(content), "\n") {
					if len(matches) >= args.MaxResults {
						return filepath.SkipAll
					}
					if expression.MatchString(line) {
						trimmed := strings.TrimSpace(line)
						if len(trimmed) > 300 {
							trimmed = trimmed[:300] + "…"
						}
						matches = append(matches, fmt.Sprintf("%s:%d: %s", relative, index+1, trimmed))
					}
				}
				return nil
			})
			if walkErr != nil && !errors.Is(walkErr, filepath.SkipAll) {
				return "", fmt.Errorf("%s: %w", i18n.D("搜索失败"), walkErr)
			}
			if len(matches) == 0 {
				return i18n.D("没有命中。"), nil
			}
			return strings.Join(matches, "\n"), nil
		},
	}
}

// isBinary 粗判二进制：前 8KB 里有 NUL 就当二进制。
// 目的只是别把二进制内容塞进模型上下文，不需要精确。
func isBinary(content []byte) bool {
	limit := len(content)
	if limit > 8192 {
		limit = 8192
	}
	for _, b := range content[:limit] {
		if b == 0 {
			return true
		}
	}
	return false
}
