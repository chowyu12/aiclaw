package tools

import (
	"encoding/json"

	"github.com/chowyu12/aiclaw/internal/model"
)

func mustJSON(v any) model.JSON {
	data, _ := json.Marshal(v)
	return model.JSON(data)
}

// DefaultBuiltinDefs 返回所有内置工具的元数据定义（名称、描述、参数 schema）。
// 内置工具不保存数据库，始终在内存中生效，默认启用给所有 Agent。
func DefaultBuiltinDefs() []model.Tool {
	return []model.Tool{
		{
			Name:        "current_time",
			Description: "获取当前系统时间，返回 ISO 8601 格式的时间字符串。无需输入参数。",
			HandlerType: model.HandlerBuiltin,
			Enabled:     true,
			FunctionDef: mustJSON(map[string]any{
				"name":        "current_time",
				"description": "Get the current system time in ISO 8601 format",
				"parameters": map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				},
			}),
		},
		{
			Name:        "read",
			Description: "读取文件内容。支持全文读取或按行范围读取（通过 offset/limit 参数）。对于图片文件（png/jpg/gif/webp/svg），会自动将图片传递给视觉模型进行理解和分析。",
			HandlerType: model.HandlerBuiltin,
			Enabled:     true,
			FunctionDef: mustJSON(map[string]any{
				"name":        "read",
				"description": "Read file content. Supports full read or partial read by line range using offset/limit. For image files (png/jpg/gif/webp/svg), the image is automatically passed to the vision model for understanding and analysis.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"file_path": map[string]any{
							"type":        "string",
							"description": "Path to the file to read",
						},
						"offset": map[string]any{
							"type":        "integer",
							"description": "Starting line number (1-based). If set, returns lines with line numbers.",
						},
						"limit": map[string]any{
							"type":        "integer",
							"description": "Maximum number of lines to return (default 200 when offset is set)",
						},
					},
					"required": []string{"file_path"},
				},
			}),
		},
		{
			Name:        "write",
			Description: "创建或覆盖文件。支持绝对路径、~ 开头路径和相对路径（解析到 Agent 沙箱目录）。可选追加模式。自动创建父目录。",
			HandlerType: model.HandlerBuiltin,
			Enabled:     true,
			FunctionDef: mustJSON(map[string]any{
				"name":        "write",
				"description": "Create or overwrite a file. Supports absolute, home-relative (~/...), and relative paths. Creates parent directories automatically.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{
							"type":        "string",
							"description": "File path to write",
						},
						"content": map[string]any{
							"type":        "string",
							"description": "Text content to write to the file",
						},
						"append": map[string]any{
							"type":        "boolean",
							"description": "If true, append to existing file instead of overwriting (default: false)",
						},
					},
					"required": []string{"path", "content"},
				},
			}),
		},
		{
			Name:        "edit",
			Description: "对文件进行精确编辑。查找 old_string 并替换为 new_string。old_string 在文件中必须唯一（不唯一时需提供更多上下文）。",
			HandlerType: model.HandlerBuiltin,
			Enabled:     true,
			FunctionDef: mustJSON(map[string]any{
				"name":        "edit",
				"description": "Make a precise edit to a file. Finds old_string and replaces it with new_string. old_string must match exactly one occurrence in the file.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"file_path": map[string]any{
							"type":        "string",
							"description": "Path to the file to edit",
						},
						"old_string": map[string]any{
							"type":        "string",
							"description": "Exact text to find (must be unique in the file)",
						},
						"new_string": map[string]any{
							"type":        "string",
							"description": "Replacement text",
						},
					},
					"required": []string{"file_path", "old_string", "new_string"},
				},
			}),
		},
		{
			Name:        "grep",
			Description: "按正则表达式模式搜索文件内容。支持目录递归搜索、文件过滤、大小写忽略。自动跳过 .git/node_modules 等目录。",
			HandlerType: model.HandlerBuiltin,
			Enabled:     true,
			Timeout:     60,
			FunctionDef: mustJSON(map[string]any{
				"name":        "grep",
				"description": "Search file contents by regex pattern. Supports recursive directory search, file type filtering, and case-insensitive matching.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"pattern": map[string]any{
							"type":        "string",
							"description": "Regular expression pattern to search for",
						},
						"path": map[string]any{
							"type":        "string",
							"description": "File or directory path to search in (default: current directory)",
						},
						"include": map[string]any{
							"type":        "string",
							"description": "File name glob filter, e.g. '*.go', '*.py'",
						},
						"ignore_case": map[string]any{
							"type":        "boolean",
							"description": "Case-insensitive search (default: false)",
						},
					},
					"required": []string{"pattern"},
				},
			}),
		},
		{
			Name:        "find",
			Description: "按 glob 模式查找文件。支持 ** 递归匹配。自动跳过 .git/node_modules 等目录。",
			HandlerType: model.HandlerBuiltin,
			Enabled:     true,
			Timeout:     60,
			FunctionDef: mustJSON(map[string]any{
				"name":        "find",
				"description": "Find files matching a glob pattern. Supports ** for recursive matching.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"pattern": map[string]any{
							"type":        "string",
							"description": "Glob pattern to match files, e.g. '*.go', '**/*.test.js', 'Makefile'",
						},
						"path": map[string]any{
							"type":        "string",
							"description": "Root directory to search from (default: current directory)",
						},
					},
					"required": []string{"pattern"},
				},
			}),
		},
		{
			Name:        "ls",
			Description: "列出目录内容。显示文件权限、大小、修改时间等信息。",
			HandlerType: model.HandlerBuiltin,
			Enabled:     true,
			FunctionDef: mustJSON(map[string]any{
				"name":        "ls",
				"description": "List directory contents with permissions, size, and modification time.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{
							"type":        "string",
							"description": "Directory path to list (default: current directory)",
						},
					},
				},
			}),
		},
		{
			Name:        "exec",
			Description: "运行 shell 命令。支持 PTY 以适配需要 TTY 的命令行工具（如 docker、kubectl 等），自动检测并使用 PTY。内置危险命令拦截。",
			HandlerType: model.HandlerBuiltin,
			Enabled:     true,
			Timeout:     300,
			FunctionDef: mustJSON(map[string]any{
				"name":        "exec",
				"description": "Execute a shell command with PTY support. Automatically allocates a pseudo-terminal for commands that require TTY. Built-in dangerous command blocking.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"command": map[string]any{
							"type":        "string",
							"description": "Shell command to execute",
						},
						"timeout": map[string]any{
							"type":        "integer",
							"description": "Timeout in seconds (default: 30, max: 300)",
						},
						"working_dir": map[string]any{
							"type":        "string",
							"description": "Working directory for command execution",
						},
					},
					"required": []string{"command"},
				},
			}),
		},
		{
			Name:        "process",
			Description: "管理后台 exec 会话。支持启动后台命令、列出会话、读取输出、终止进程。适用于需要长时间运行的命令（如开发服务器、日志监控）。",
			HandlerType: model.HandlerBuiltin,
			Enabled:     true,
			FunctionDef: mustJSON(map[string]any{
				"name":        "process",
				"description": "Manage background exec sessions. Start long-running commands, list sessions, read output, or kill processes.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action": map[string]any{
							"type":        "string",
							"enum":        []string{"start", "list", "read", "kill"},
							"description": "start: launch background command; list: show sessions; read: get output; kill: terminate session",
						},
						"session_id": map[string]any{
							"type":        "string",
							"description": "Session ID (required for read/kill)",
						},
						"command": map[string]any{
							"type":        "string",
							"description": "Shell command (required for start)",
						},
					},
					"required": []string{"action"},
				},
			}),
		},
		{
			Name:        "web_fetch",
			Description: "抓取 URL 并提取可读内容。优先通过 HTTP 直接获取，失败时自动回退到浏览器渲染提取文本。",
			HandlerType: model.HandlerBuiltin,
			Enabled:     true,
			Timeout:     60,
			FunctionDef: mustJSON(map[string]any{
				"name":        "web_fetch",
				"description": "Fetch a URL and extract readable content. Tries HTTP first, falls back to browser rendering for dynamic pages.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"url": map[string]any{
							"type":        "string",
							"description": "URL to fetch",
						},
					},
					"required": []string{"url"},
				},
			}),
		},
		{
			Name:        "browser",
			Description: "控制网页浏览器。支持导航、截图、元素快照与交互、表单填充、Cookie/Storage 管理、Console/Network 监控、设备仿真等。多标签时：每个标签页 snapshot 产生的 ref 仅对该页有效；若操作页与当前激活页不一致，请在 click/type/snapshot 等参数中传入 target_id（由 tabs 或 open_tab 返回）。",
			HandlerType: model.HandlerBuiltin,
			Enabled:     true,
			Timeout:     120,
			FunctionDef: mustJSON(browserToolDef()),
		},
		{
			Name:        "canvas",
			Description: "展示/评估/快照 Canvas 画布。支持渲染 HTML/CSS/JS 内容、执行 JavaScript 表达式、截取画面快照。",
			HandlerType: model.HandlerBuiltin,
			Enabled:     true,
			Timeout:     60,
			FunctionDef: mustJSON(map[string]any{
				"name":        "canvas",
				"description": "Display, evaluate, or snapshot a Canvas. Render HTML/CSS/JS, run JS expressions, and capture visual snapshots.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action": map[string]any{
							"type":        "string",
							"enum":        []string{"show", "evaluate", "snapshot"},
							"description": "show: render HTML to preview; evaluate: run JS expression on rendered page; snapshot: capture screenshot",
						},
						"html": map[string]any{
							"type":        "string",
							"description": "HTML content to render (required for all actions)",
						},
						"expression": map[string]any{
							"type":        "string",
							"description": "JavaScript expression to evaluate (required for evaluate action)",
						},
						"width": map[string]any{
							"type":        "integer",
							"description": "Viewport width for snapshot (default: 1280)",
						},
						"height": map[string]any{
							"type":        "integer",
							"description": "Viewport height for snapshot (default: 720)",
						},
					},
					"required": []string{"action", "html"},
				},
			}),
		},
		{
			Name: "cron",
			Description: "管理定时任务与唤醒事件。支持创建/列出/删除 cron 定时任务，以及设置提醒唤醒事件。" +
				"设置提醒时，systemEvent 文本需符合提醒触发时的阅读场景，根据时间间隔标注提醒属性，必要时补充上下文。",
			HandlerType: model.HandlerBuiltin,
			Enabled:     true,
			FunctionDef: mustJSON(map[string]any{
				"name": "cron",
				"description": "Manage cron jobs and wake events. " +
					"schedule: create a cron job (optionally with inline script); " +
					"list: show crontab entries; " +
					"remove: delete entries by pattern; " +
					"add_event: set a wake/reminder event with systemEvent text; " +
					"list_events: show all wake events; " +
					"remove_event: delete a wake event.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action": map[string]any{
							"type":        "string",
							"enum":        []string{"schedule", "list", "remove", "add_event", "list_events", "remove_event"},
							"description": "Action to perform",
						},
						"expression": map[string]any{
							"type":        "string",
							"description": "Cron expression, e.g. '0 9 * * *', '*/5 * * * *', '@daily'",
						},
						"command": map[string]any{
							"type":        "string",
							"description": "Command to schedule (for schedule action)",
						},
						"name": map[string]any{
							"type":        "string",
							"description": "Script name when providing inline content (for schedule)",
						},
						"content": map[string]any{
							"type":        "string",
							"description": "Shell script content (for schedule with inline script)",
						},
						"pattern": map[string]any{
							"type":        "string",
							"description": "Text pattern for matching crontab entries (for remove)",
						},
						"log_output": map[string]any{
							"type":        "boolean",
							"description": "Redirect stdout/stderr to log file (for schedule)",
						},
						"system_event": map[string]any{
							"type":        "string",
							"description": "Wake event text for reminders. Should read naturally at trigger time, include reminder context.",
						},
						"interval": map[string]any{
							"type":        "string",
							"description": "Interval for wake events, e.g. '30m', '2h', '1d' (alternative to expression for add_event)",
						},
						"event_id": map[string]any{
							"type":        "string",
							"description": "Event ID for remove_event",
						},
					},
					"required": []string{"action"},
				},
			}),
		},
		{
			Name: "code_interpreter",
			Description: "代码解释器，支持编写并执行 Python/JavaScript/Shell 代码。" +
				"Agent 传入语言类型和代码，工具自动在沙箱目录中创建文件并执行，返回 stdout/stderr 结果。" +
				"适用于数据处理、数学计算、文件生成、API 调试、格式转换等场景。",
			HandlerType: model.HandlerBuiltin,
			Enabled:     true,
			Timeout:     120,
			FunctionDef: mustJSON(map[string]any{
				"name": "code_interpreter",
				"description": "Execute code in a sandboxed environment. Supports Python, JavaScript, and Shell. " +
					"Write code to solve problems like data processing, math computation, file generation, API testing, and format conversion. " +
					"Returns stdout, stderr, exit code, and execution duration.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"language": map[string]any{
							"type":        "string",
							"enum":        []string{"python", "javascript", "shell"},
							"description": "Programming language: python (python3), javascript (node), or shell (sh)",
						},
						"code": map[string]any{
							"type":        "string",
							"description": "The source code to execute",
						},
						"timeout": map[string]any{
							"type":        "integer",
							"description": "Execution timeout in seconds (default: 60, max: 120)",
						},
					},
					"required": []string{"language", "code"},
				},
			}),
		},
		{
			Name:        "desktop",
			Description: "桌面 RPA 工具。推荐用 find_element 通过无障碍 API 精确定位 UI 元素（返回坐标），再 click。截图带坐标标尺作为辅助。典型流程：find_element → 获取精确坐标 → click → 验证。",
			HandlerType: model.HandlerBuiltin,
			Enabled:     true,
			FunctionDef: mustJSON(desktopToolDef()),
		},
	}
}

func desktopToolDef() map[string]any {
	return map[string]any{
		"name": "desktop",
		"description": "Desktop RPA tool. " +
			"BEST PRACTICE: Use find_element to get precise coordinates via Accessibility API. " +
			"1) find_element(app, text) — searches title/value/description attributes; returns ruler_x/ruler_y. " +
			"2) find_element(app) — no text → lists all interactive elements (buttons, text fields, etc.) with coordinates. " +
			"3) Use returned ruler_x/ruler_y directly as x/y in click/scroll. " +
			"Workflow: find_element → click(ruler_x, ruler_y) → verify. " +
			"Fallback when find_element fails: screenshot → read ruler coords visually → click.",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"screenshot", "click", "type", "press", "scroll", "mouse_move", "list_windows", "focus_window", "find_element"},
				"description": "Action to perform. Use find_element first to get precise coordinates.",
			},
			"x": map[string]any{
				"type":        "integer",
				"description": "X coordinate read from the screenshot ruler (auto-mapped to screen). Required for click/scroll/mouse_move.",
			},
			"y": map[string]any{
				"type":        "integer",
				"description": "Y coordinate read from the screenshot ruler (auto-mapped to screen). Required for click/scroll/mouse_move.",
			},
			"display": map[string]any{
				"type":        "integer",
				"description": "Display index for screenshot (0=primary, 1=secondary, etc.). Default: 0.",
			},
				"text": map[string]any{
					"type":        "string",
					"description": "For type: text to input. For find_element: search text (matches title/value/description; omit to list all interactive elements).",
				},
				"key": map[string]any{
					"type":        "string",
					"description": "Key or combination to press: enter, tab, escape, ctrl+c, cmd+v, alt+f4, shift+tab, etc.",
				},
				"button": map[string]any{
					"type":        "string",
					"enum":        []string{"left", "right", "middle"},
					"description": "Mouse button for click (default: left)",
				},
				"clicks": map[string]any{
					"type":        "integer",
					"description": "Number of clicks (default: 1, use 2 for double-click)",
				},
				"scroll_x": map[string]any{
					"type":        "integer",
					"description": "Horizontal scroll amount (positive=right, negative=left)",
				},
				"scroll_y": map[string]any{
					"type":        "integer",
					"description": "Vertical scroll amount (positive=up, negative=down)",
				},
			"window": map[string]any{
				"type":        "string",
				"description": "Window/app name or title keyword for focus_window",
			},
			"app": map[string]any{
				"type":        "string",
				"description": "App name for find_element (e.g. '企业微信', 'Safari'). Required for find_element.",
			},
				"region": map[string]any{
					"type":        "object",
					"description": "Capture region for screenshot (optional, default: full screen)",
					"properties": map[string]any{
						"x":      map[string]any{"type": "integer", "description": "Top-left X"},
						"y":      map[string]any{"type": "integer", "description": "Top-left Y"},
						"width":  map[string]any{"type": "integer", "description": "Width in pixels"},
						"height": map[string]any{"type": "integer", "description": "Height in pixels"},
					},
				},
			},
			"required": []string{"action"},
		},
	}
}

func browserToolDef() map[string]any {
	allActions := []string{
		"navigate", "screenshot", "snapshot", "get_text", "evaluate", "pdf",
		"click", "type", "hover", "drag", "select", "fill_form", "scroll",
		"upload", "wait", "dialog", "tabs", "open_tab", "close_tab", "close",
		"console", "network", "cookies", "storage", "press",
		"back", "forward", "reload",
		"extract_table", "resize",
		"set_device", "set_media", "highlight",
	}

	return map[string]any{
		"name": "browser",
		"description": "Browser automation tool. Actions: " +
			"navigate/back/forward/reload (navigation), " +
			"snapshot (get interactive elements with refs), " +
			"click/type/press/hover/drag/select/fill_form/scroll (interaction), " +
			"screenshot/pdf/get_text/extract_table (data extraction), " +
			"console/network (monitoring), " +
			"cookies/storage (state management), " +
			"resize/set_device/set_media (emulation), " +
			"highlight (debugging), " +
			"evaluate (run JS), " +
			"wait (wait for condition), " +
			"tabs/open_tab/close_tab/close (tab management), " +
			"dialog/upload (misc). " +
			"Multi-tab: refs from snapshot are scoped per tab. If the active tab differs from the page you snapshotted, pass target_id (from tabs or open_tab) on snapshot, click, type, and other ref-using actions. " +
			"Use snapshot first to see refs like e1, then use those refs on the same target_id.",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        allActions,
					"description": "Action to perform",
				},
				"url":           map[string]any{"type": "string", "description": "URL for navigate/open_tab"},
				"ref":           map[string]any{"type": "string", "description": "Element ref from snapshot on this tab (e.g. 'e1'); must match target_id used when snapshot was taken"},
				"text":          map[string]any{"type": "string", "description": "Text to type"},
				"expression":    map[string]any{"type": "string", "description": "JavaScript expression for evaluate"},
				"selector":      map[string]any{"type": "string", "description": "CSS selector (alternative to ref)"},
				"full_page":     map[string]any{"type": "boolean", "description": "Full page screenshot"},
				"submit":        map[string]any{"type": "boolean", "description": "Press Enter after typing"},
				"slowly":        map[string]any{"type": "boolean", "description": "Type character by character"},
				"button":        map[string]any{"type": "string", "enum": []string{"left", "right", "middle"}, "description": "Mouse button for click"},
				"double_click":  map[string]any{"type": "boolean", "description": "Double-click"},
				"start_ref":     map[string]any{"type": "string", "description": "Drag start element ref"},
				"end_ref":       map[string]any{"type": "string", "description": "Drag end element ref"},
				"values":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Select option values"},
				"fields":        map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"ref": map[string]any{"type": "string"}, "value": map[string]any{"type": "string"}, "type": map[string]any{"type": "string"}}}, "description": "Form fields [{ref,value,type}]"},
				"target_id":     map[string]any{"type": "string", "description": "Tab ID from tabs or open_tab; omit for active tab. With multiple tabs, pass the same tab you used for snapshot so refs stay valid"},
				"wait_time":     map[string]any{"type": "integer", "description": "Wait milliseconds"},
				"wait_text":     map[string]any{"type": "string", "description": "Wait for text to appear on page"},
				"wait_selector": map[string]any{"type": "string", "description": "Wait for CSS selector to become visible"},
				"wait_url":      map[string]any{"type": "string", "description": "Wait for URL to contain string"},
				"wait_fn":       map[string]any{"type": "string", "description": "JS expression to poll until truthy"},
				"wait_load":     map[string]any{"type": "string", "enum": []string{"networkidle", "domcontentloaded", "load"}, "description": "Wait for page load state"},
				"accept":        map[string]any{"type": "boolean", "description": "Accept (true) or dismiss (false) dialog"},
				"prompt_text":   map[string]any{"type": "string", "description": "Prompt dialog input text"},
				"paths":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "File paths for upload"},
				"scroll_y":      map[string]any{"type": "integer", "description": "Scroll to Y offset (pixels, 0=bottom)"},
				"level":         map[string]any{"type": "string", "enum": []string{"error", "warn", "info", "log"}, "description": "Console log level filter"},
				"filter":        map[string]any{"type": "string", "description": "URL keyword filter for network requests"},
				"clear":         map[string]any{"type": "boolean", "description": "Clear buffer after reading (console/network)"},
				"operation":     map[string]any{"type": "string", "enum": []string{"get", "set", "clear"}, "description": "Operation for cookies/storage"},
				"cookie_name":   map[string]any{"type": "string", "description": "Cookie name for set"},
				"cookie_value":  map[string]any{"type": "string", "description": "Cookie value for set"},
				"cookie_url":    map[string]any{"type": "string", "description": "Cookie URL scope for set"},
				"cookie_domain": map[string]any{"type": "string", "description": "Cookie domain for set"},
				"storage_type":  map[string]any{"type": "string", "enum": []string{"local", "session"}, "description": "Storage type"},
				"key":           map[string]any{"type": "string", "description": "Storage key for get/set"},
				"value":         map[string]any{"type": "string", "description": "Storage value for set"},
				"key_name":      map[string]any{"type": "string", "description": "Key name for press (Enter/Tab/Escape/etc)"},
				"width":         map[string]any{"type": "integer", "description": "Viewport width for resize"},
				"height":        map[string]any{"type": "integer", "description": "Viewport height for resize"},
				"device":        map[string]any{"type": "string", "description": "Device name for set_device"},
				"color_scheme":  map[string]any{"type": "string", "enum": []string{"dark", "light", "no-preference"}, "description": "Color scheme for set_media"},
			},
			"required": []string{"action"},
		},
	}
}
