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
		return "这个会话没有设置工作区，所有写入都会先问一句"
	}
	return fmt.Sprintf("这个路径在会话工作区（%s）之外", env.Workspace)
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
		Description: "读取一个文件的内容。可以读工作区之外的文件（涉及凭据的目录除外）。" +
			"大文件会被截断。",
		Effect: EffectRead,
		Schema: schema(map[string]any{
			"path": map[string]any{
				"type": "string", "description": "文件路径。相对路径按工作区解析，也可以给绝对路径",
			},
			"offset": map[string]any{
				"type": "integer", "description": "从第几行开始读，1 起；不传从头读", "minimum": 1,
			},
			"limit": map[string]any{
				"type": "integer", "description": "最多读多少行", "minimum": 1,
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
				return "", fmt.Errorf("读取失败：%w", err)
			}
			if info.IsDir() {
				return "", fmt.Errorf("%s 是目录，不是文件；用 list_dir 看目录内容", args.Path)
			}

			content, err := os.ReadFile(path)
			if err != nil {
				return "", fmt.Errorf("读取失败：%w", err)
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
					return "", fmt.Errorf("offset %d 超出文件行数 %d", args.Offset, len(lines))
				}
				end := len(lines)
				if args.Limit > 0 && start+args.Limit < end {
					end = start + args.Limit
					truncated = true
				}
				text = strings.Join(lines[start:end], "\n")
			}
			if truncated {
				text += "\n\n[内容已截断]"
			}
			return text, nil
		},
	}
}

func writeTool() Tool {
	return Tool{
		Name:        "write_file",
		Description: "把内容写入文件，覆盖已有内容。父目录会自动创建。写到工作区之外会先请用户确认。",
		Effect:      EffectWrite,
		Schema: schema(map[string]any{
			"path":    map[string]any{"type": "string", "description": "文件路径。相对路径按工作区解析"},
			"content": map[string]any{"type": "string", "description": "完整文件内容"},
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
				"写入文件", path, outsideReason(env, inside), scopeOf(inside, path),
			); err != nil {
				return "", err
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return "", fmt.Errorf("创建父目录失败：%w", err)
			}
			if err := os.WriteFile(path, []byte(args.Content), 0o644); err != nil {
				return "", fmt.Errorf("写入失败：%w", err)
			}
			return fmt.Sprintf("已写入 %s（%d 字节）", args.Path, len(args.Content)), nil
		},
	}
}

func editTool() Tool {
	return Tool{
		Name: "edit_file",
		Description: "把文件里的一段精确文本替换成新文本。" +
			"old_text 必须在文件中唯一出现，否则报错——这样不会改错地方。",
		Effect: EffectWrite,
		Schema: schema(map[string]any{
			"path":     map[string]any{"type": "string", "description": "文件路径。相对路径按工作区解析"},
			"old_text": map[string]any{"type": "string", "description": "要被替换的原文，必须唯一"},
			"new_text": map[string]any{"type": "string", "description": "替换成的新文本"},
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
				return "", errors.New("old_text 不能为空；要写全新内容用 write_file")
			}
			path, inside, err := env.ResolveWrite(args.Path)
			if err != nil {
				return "", err
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return "", fmt.Errorf("读取失败：%w", err)
			}
			text := string(content)
			count := strings.Count(text, args.OldText)
			if count == 0 {
				return "", errors.New("文件里找不到 old_text；先用 read_file 确认原文（注意空白与缩进）")
			}
			if count > 1 {
				return "", fmt.Errorf("old_text 在文件里出现了 %d 次，无法确定改哪一处；请带上更多上下文使其唯一", count)
			}
			if err := env.requestApprovalScoped(
				ctx, writeEffect(inside), protocol.ApprovalWrite,
				"修改文件", path, outsideReason(env, inside), scopeOf(inside, path),
			); err != nil {
				return "", err
			}
			updated := strings.Replace(text, args.OldText, args.NewText, 1)
			if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
				return "", fmt.Errorf("写入失败：%w", err)
			}
			return fmt.Sprintf("已修改 %s", args.Path), nil
		},
	}
}

func listTool() Tool {
	return Tool{
		Name:        "list_dir",
		Description: "列出一个目录的条目。可以列工作区之外的目录（涉及凭据的目录除外）。",
		Effect:      EffectRead,
		Schema: schema(map[string]any{
			"path": map[string]any{"type": "string", "description": "目录路径；不传列工作区根（没设工作区时是主目录）"},
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
				return "", fmt.Errorf("列目录失败：%w", err)
			}
			if len(entries) == 0 {
				return "（空目录）", nil
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
				lines = append(lines, fmt.Sprintf("%s\t%d 字节", name, info.Size()))
			}
			sort.Strings(lines)
			return strings.Join(lines, "\n"), nil
		},
	}
}

func grepTool() Tool {
	return Tool{
		Name:        "search_files",
		Description: "按正则搜索文件内容，返回命中行及其文件与行号。默认搜工作区，也可以指定别的目录。",
		Effect:      EffectRead,
		Schema: schema(map[string]any{
			"pattern": map[string]any{"type": "string", "description": "Go 正则表达式"},
			"path":    map[string]any{"type": "string", "description": "限定搜索的目录；不传搜整个工作区"},
			"glob":    map[string]any{"type": "string", "description": "文件名通配，例如 *.go"},
			"max_results": map[string]any{
				"type": "integer", "description": "最多返回多少条，默认 100", "minimum": 1, "maximum": 1000,
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
				return "", fmt.Errorf("正则不合法：%w", err)
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
				return "", fmt.Errorf("搜索失败：%w", walkErr)
			}
			if len(matches) == 0 {
				return "没有命中。", nil
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
