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
			Description: "抓取用户显式提供的 URL 并提取可读内容。仅当用户消息中直接出现 URL（http/https 链接）时才可调用；不得用于未指定 URL 的泛化联网检索（请改用内置联网搜索）。优先通过 HTTP 直接获取，失败时自动回退到浏览器渲染提取文本。",
			HandlerType: model.HandlerBuiltin,
			Enabled:     true,
			Timeout:     60,
			FunctionDef: mustJSON(map[string]any{
				"name":        "web_fetch",
				"description": "Fetch a URL explicitly supplied by the user and extract readable content. Only call this tool when the user's message contains a concrete http/https URL; do NOT use it for general web research without a user-provided URL (use built-in web search instead). Tries HTTP first, falls back to browser rendering for dynamic pages.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"url": map[string]any{
							"type":        "string",
							"description": "Absolute http(s) URL that MUST appear verbatim in the user's message.",
						},
					},
					"required": []string{"url"},
				},
			}),
		},
		{
			Name:        "browser",
			Description: "控制网页浏览器。支持导航、截图、元素快照与交互、表单填充、Cookie/Storage 管理、Console/Network 监控、设备仿真等。多标签时：每个标签页 snapshot 产生的 ref 仅对该页有效；若操作页与当前激活页不一致，请在 click/type/snapshot 等参数中传入 target_id（由 tabs 或 open_tab 返回）。" +
				"提示：可在 config.yaml 配置 browser.cdp_endpoint=http://127.0.0.1:9222 让本工具 attach 到用户已登录的真实 Chrome（保留登录态/Cookie），适合个人助手场景。",
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
		Name: "sub_agent",
		Description: "将复杂任务拆分为多个独立子任务，分派给子 Agent 并行执行。" +
			"每个子 Agent 拥有独立的推理链和工具集。" +
			"适用场景：并行调研多个方向、对比多个方案、专家分工协作。" +
			"返回结构化 JSON 结果，包含每个子任务的状态、摘要、耗时和 token 用量。",
		HandlerType: model.HandlerBuiltin,
		Enabled:     true,
		Timeout:     600,
		FunctionDef: mustJSON(map[string]any{
			"name": "sub_agent",
			"description": "Delegate tasks to sub-agents for parallel execution. " +
				"Each sub-agent has its own reasoning chain, tools, and isolated context. " +
				"Returns structured JSON with status, summary, tokens, and duration for each task. " +
				"Use cases: parallel research, comparing approaches, expert delegation. " +
				"Max nesting depth: 3.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tasks": map[string]any{
						"type":        "array",
						"description": "Array of tasks to execute in parallel. Each task runs as an independent sub-agent.",
						"minItems":    1,
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"goal": map[string]any{
									"type":        "string",
									"description": "Clear, specific goal for this sub-agent to accomplish",
								},
								"context": map[string]any{
									"type":        "string",
									"description": "Optional background context or constraints the sub-agent should be aware of",
								},
								"agent_uuid": map[string]any{
									"type":        "string",
									"description": "Optional: delegate to a specific agent by UUID (default: same agent as parent)",
								},
								"blocked_tools": map[string]any{
									"type":        "array",
									"description": "Optional: list of tool names the sub-agent is NOT allowed to use",
									"items":       map[string]any{"type": "string"},
								},
								"mode": map[string]any{
									"type":        "string",
									"enum":        []string{"auto", "explore", "shell"},
									"description": "Execution mode. 'auto': full tool access (default). 'explore': read-only, fast codebase exploration (read/grep/find/ls only). 'shell': command execution only (exec/process/read/ls).",
								},
								"model": map[string]any{
									"type":        "string",
									"enum":        []string{"default", "fast"},
									"description": "Model selection. 'default': same model as parent (default). 'fast': use lighter/cheaper model for simple tasks.",
								},
							},
							"required": []string{"goal"},
						},
					},
				},
				"required": []string{"tasks"},
			},
		}),
	},
	{
		Name: "memory",
		Description: "持久记忆工具，跨会话保存重要信息。" +
			"两个存储目标：memory（你的个人笔记：环境事实、项目惯例、工具使用经验）和 user（用户画像：偏好、沟通风格、工作习惯）。" +
			"主动保存：用户纠正你时、用户分享偏好/习惯时、发现环境特征时、学到有用经验时。" +
			"容量超 70% 时自动进入索引模式（system prompt 仅展示 [id]+tag+摘要），用 action=recall + ids 拉取完整内容。",
		HandlerType: model.HandlerBuiltin,
		Enabled:     true,
		FunctionDef: mustJSON(map[string]any{
			"name": "memory",
			"description": "Save durable information to persistent memory that survives across sessions. " +
				"Memory is injected into future system prompts, so keep entries compact and factual.\n\n" +
				"WHEN TO SAVE (proactively, don't wait to be asked):\n" +
				"- User corrects you or says 'remember this'\n" +
				"- User shares a preference, habit, or personal detail\n" +
				"- You discover something about the environment (OS, tools, project structure)\n" +
				"- You learn a convention or workflow specific to this user\n\n" +
				"TWO TARGETS:\n" +
				"- 'user': who the user is — name, role, preferences, communication style\n" +
				"- 'memory': your notes — environment facts, project conventions, lessons learned\n\n" +
				"ACTIONS:\n" +
				"- add: create entry. Optional 'tag' groups related entries (e.g. 'env', 'pref', 'project').\n" +
				"- replace: update existing entry; old_text may be the entry id (preferred) or a unique substring.\n" +
				"- remove: delete entry by id or unique substring.\n" +
				"- read: view current entries.\n" +
				"- recall: fetch full content for one or more ids (used when storage is in INDEX MODE).\n\n" +
				"INDEX MODE: when usage exceeds 70%, the snapshot in system prompt only shows [id]+tag+summary for each entry. " +
				"Call recall(ids=[...]) to retrieve full content for the entries you actually need.\n\n" +
				"Do NOT save task progress, temporary TODO state, or trivial/obvious information.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type":        "string",
						"enum":        []string{"add", "replace", "remove", "read", "recall"},
						"description": "The action to perform",
					},
					"target": map[string]any{
						"type":        "string",
						"enum":        []string{"memory", "user"},
						"description": "Which memory store: 'memory' for personal notes, 'user' for user profile",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "The entry content. Required for 'add' and 'replace'",
					},
					"old_text": map[string]any{
						"type":        "string",
						"description": "Entry id (preferred) or unique substring identifying the entry to replace or remove",
					},
					"tag": map[string]any{
						"type":        "string",
						"description": "Optional short tag for grouping (e.g. 'env', 'pref', 'project'). Used by add/replace.",
					},
					"ids": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "List of entry ids to fetch full content for (required for recall action)",
					},
				},
				"required": []string{"action", "target"},
			},
		}),
	},
	{
		Name: "cron",
		Description: "定时任务调度器。支持定时执行 Agent 提示词或 Shell 命令。" +
			"支持秒级精度 cron 表达式、最大运行次数限制、执行日志查看、启用/禁用切换。",
		HandlerType: model.HandlerBuiltin,
		Enabled:     true,
		FunctionDef: mustJSON(map[string]any{
			"name": "cron",
			"description": "In-process task scheduler. Schedule agent prompts or commands with cron expressions.\n\n" +
				"ACTIONS:\n" +
				"- add: Create a new scheduled job (cron expression + prompt or command)\n" +
				"- list: Show all scheduled jobs with status\n" +
				"- remove: Delete a job by ID\n" +
				"- toggle: Enable/disable a job\n" +
				"- logs: View execution history for a job\n\n" +
				"Supports 6-field cron expressions (sec min hour dom month dow) and descriptors like @daily, @hourly, @every 30m.\n\n" +
				"Examples:\n" +
				"- '0 0 9 * * *' = daily at 9:00 AM\n" +
				"- '0 */5 * * * *' = every 5 minutes\n" +
				"- '@every 30m' = every 30 minutes\n" +
				"- '@daily' = once a day at midnight",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type":        "string",
						"enum":        []string{"add", "list", "remove", "toggle", "logs"},
						"description": "Action to perform",
					},
					"name": map[string]any{
						"type":        "string",
						"description": "Human-readable name for the job (for add)",
					},
					"expression": map[string]any{
						"type":        "string",
						"description": "Cron expression (6-field: sec min hour dom month dow) or descriptor (@daily, @hourly, @every 30m)",
					},
					"type": map[string]any{
						"type":        "string",
						"enum":        []string{"prompt", "command"},
						"description": "Job type: 'prompt' to send a message to an agent, 'command' to run a shell command (default: prompt)",
					},
					"agent_uuid": map[string]any{
						"type":        "string",
						"description": "Target agent UUID for prompt-type jobs",
					},
					"prompt": map[string]any{
						"type":        "string",
						"description": "The prompt to send to the agent (for prompt-type jobs)",
					},
					"command": map[string]any{
						"type":        "string",
						"description": "Shell command to execute (for command-type jobs)",
					},
					"job_id": map[string]any{
						"type":        "string",
						"description": "Job ID for remove/toggle/logs actions",
					},
					"enabled": map[string]any{
						"type":        "boolean",
						"description": "Enable (true) or disable (false) the job (for toggle action)",
					},
					"max_runs": map[string]any{
						"type":        "integer",
						"description": "Max number of times to run (0 = unlimited). Job auto-disables after reaching limit.",
					},
					"description": map[string]any{
						"type":        "string",
						"description": "Description of what this job does",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Max log entries to return (for logs action, default: 10)",
					},
				},
				"required": []string{"action"},
			},
		}),
	},
	{
		Name: "todo",
		Description: "结构化任务规划工具，用于拆解和追踪复杂任务。" +
			"收到复杂请求时（涉及 3+ 步骤），先创建任务列表再逐项执行。每完成一步及时更新状态。",
		HandlerType: model.HandlerBuiltin,
		Enabled:     true,
		FunctionDef: mustJSON(map[string]any{
			"name": "todo",
			"description": "Structured task planning and tracking tool.\n\n" +
				"USE PROACTIVELY for:\n" +
				"1. Complex multi-step tasks (3+ distinct steps)\n" +
				"2. Non-trivial tasks requiring careful planning\n" +
				"3. User provides multiple tasks (numbered/comma-separated)\n\n" +
				"SKIP for:\n" +
				"1. Single, straightforward tasks\n" +
				"2. Tasks completable in < 3 trivial steps\n" +
				"3. Purely informational requests\n\n" +
				"ACTIONS:\n" +
				"- create: Create task list (merge=false replaces all, merge=true adds/updates)\n" +
				"- update: Update task statuses (merge by id)\n" +
				"- read: View current task list\n" +
				"- clear: Remove all tasks\n\n" +
				"STATUSES: pending, in_progress, completed, cancelled\n" +
				"RULES: Only ONE task should be in_progress at a time. Mark complete IMMEDIATELY after finishing.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type":        "string",
						"enum":        []string{"create", "update", "read", "clear"},
						"description": "Action to perform",
					},
					"todos": map[string]any{
						"type":        "array",
						"description": "Array of TODO items to create or update",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"id": map[string]any{
									"type":        "string",
									"description": "Unique identifier for this TODO item",
								},
								"content": map[string]any{
									"type":        "string",
									"description": "Description of the task",
								},
								"status": map[string]any{
									"type":        "string",
									"enum":        []string{"pending", "in_progress", "completed", "cancelled"},
									"description": "Current status",
								},
							},
							"required": []string{"id", "content", "status"},
						},
					},
					"merge": map[string]any{
						"type":        "boolean",
						"description": "If true, merge with existing todos by id. If false, replace all todos. Default: false",
					},
				},
				"required": []string{"action"},
			},
		}),
	},
	{
		Name: "skill",
		Description: "技能结晶（self-evolving skills）。" +
			"aiclaw 在多工具协作成功后会把执行路径自动归档到 skills-pending/。" +
			"用本工具：list_pending 看候选 → read_pending 读全文 → promote 转正成正式 skill / discard 丢弃。" +
			"主动使用：用户说'记下这套流程'、'以后都这样做'、'把刚才的步骤变成 skill' 时。",
		HandlerType: model.HandlerBuiltin,
		Enabled:     true,
		FunctionDef: mustJSON(map[string]any{
			"name": "skill",
			"description": "Manage self-evolving skills.\n\n" +
				"aiclaw automatically captures multi-tool execution paths into skills-pending/ as candidates.\n" +
				"Use this tool to inspect, promote (to ~/.aiclaw/skills/), or discard those candidates.\n\n" +
				"ACTIONS:\n" +
				"- list_pending: list crystallized candidates awaiting review (most recent first)\n" +
				"- read_pending: read the full markdown of a single candidate by file_name\n" +
				"- promote: turn a candidate into a real skill. Requires file_name + name + description.\n" +
				"- discard: delete a candidate that isn't worth keeping.\n" +
				"- list_active: list current active skills under workspace skills/.\n\n" +
				"USE PROACTIVELY when the user says 'remember this workflow', 'next time do it like this', " +
				"'turn the previous steps into a skill', or after solving a non-trivial multi-step task you'd want to repeat.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type":        "string",
						"enum":        []string{"list_pending", "read_pending", "promote", "discard", "list_active"},
						"description": "Action to perform",
					},
					"file_name": map[string]any{
						"type":        "string",
						"description": "Pending candidate file name (required for read_pending/promote/discard). Get it from list_pending.",
					},
					"name": map[string]any{
						"type":        "string",
						"description": "Short skill name (required for promote). Used as the directory slug too.",
					},
					"description": map[string]any{
						"type":        "string",
						"description": "One-sentence skill description (required for promote). LLM will see this when deciding whether to apply the skill.",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Max items to return for list_pending (default 10)",
					},
				},
				"required": []string{"action"},
			},
		}),
	},
	{
		Name: "session_search",
		Description: "搜索过去的对话历史，跨会话回忆。" +
			"两种模式：无查询时返回最近会话列表（零 LLM 成本），有关键词时全文搜索并返回匹配片段。" +
			"主动使用：用户说'上次'、'记得吗'、'我们之前'等时。",
		HandlerType: model.HandlerBuiltin,
		Enabled:     true,
		FunctionDef: mustJSON(map[string]any{
			"name": "session_search",
			"description": "Search past conversation history for long-term recall.\n\n" +
				"TWO MODES:\n" +
				"1. Recent sessions (no query): browse recent conversations with titles and previews. Zero cost.\n" +
				"2. Keyword search (with query): full-text search across all past messages, returns matching snippets with context.\n\n" +
				"USE PROACTIVELY when:\n" +
				"- User says 'we did this before', 'remember when', 'last time'\n" +
				"- User references a topic from a previous session\n" +
				"- You want to check if a similar problem was solved before",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Search keywords. Omit to browse recent sessions",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Max results to return (default: 5, max: 20)",
					},
				},
				"required": []string{},
			},
		}),
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
