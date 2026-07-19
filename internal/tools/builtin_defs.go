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
			Name:        "read",
			Description: "Read file content. Supports full reads or line-range reads with offset/limit. Image files are automatically passed to the vision model for understanding and analysis.",
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
			Description: "Create or overwrite files. Supports absolute paths, home-relative paths, and relative paths resolved inside the Agent sandbox. Can append and creates parent directories automatically.",
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
			Description: "Make precise file edits by replacing old_string with new_string. old_string must be unique in the target file.",
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
			Description: "Search file contents with regular expressions. Supports recursive directory search, file filters, and case-insensitive matching while skipping common vendor directories.",
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
			Description: "Find files by glob pattern, including recursive ** matches, while skipping common vendor directories.",
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
			Description: "List directory contents with permissions, size, and modification time.",
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
			Description: "Run shell commands with automatic PTY support for tools that need a terminal. Includes dangerous-command blocking.",
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
			Description: "Manage background exec sessions. Start long-running commands, list sessions, read output, and terminate processes.",
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
			Name:        "web_search",
			Description: "Search the web through the configured search engine and return current result snippets with source URLs. Use for general web research when no concrete URL was supplied.",
			HandlerType: model.HandlerBuiltin,
			Enabled:     true,
			Timeout:     60,
			FunctionDef: mustJSON(map[string]any{
				"name":        "web_search",
				"description": "Search the web through the configured search engine and return current result snippets with source URLs. Use this for recent or time-sensitive information when the user did not provide a concrete URL.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "Search query",
						},
						"limit": map[string]any{
							"type":        "integer",
							"description": "Maximum number of results to return (default 5, max 10)",
						},
					},
					"required": []string{"query"},
				},
			}),
		},
		{
			Name:        "web_fetch",
			Description: "Fetch a URL explicitly provided by the user and extract readable content. Use only for concrete http/https URLs, not general web research.",
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
			Description: "Control a web browser. Supports navigation, screenshots, element snapshots and interaction, form filling, cookie/storage management, console/network monitoring, and device emulation. In multi-tab mode, refs are tab-specific; pass target_id when acting on a non-active tab. Configure browser.cdp_endpoint in config.yaml to attach to a signed-in Chrome profile when needed.",
			HandlerType: model.HandlerBuiltin,
			Enabled:     true,
			Timeout:     120,
			FunctionDef: mustJSON(browserToolDef()),
		},
		{
			Name:        "canvas",
			Description: "Display, evaluate, or snapshot a Canvas. Render HTML/CSS/JS, execute JavaScript expressions, and capture visual snapshots.",
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
			Name:        "code_interpreter",
			Description: "Code interpreter for running Python, JavaScript, or Shell in a sandbox. Useful for data processing, math, file generation, API debugging, and format conversion.",
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
			Name:        "sub_agent",
			Description: "Split complex work into independent sub-tasks and delegate them to sub-agents for parallel execution. Returns structured JSON with status, summary, duration, and token usage.",
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
			Name:        "memory",
			Description: "User-scoped durable memory with reviewable candidates, audited updates, and relevant cross-session retrieval.",
			HandlerType: model.HandlerBuiltin,
			Enabled:     true,
			FunctionDef: mustJSON(map[string]any{
				"name": "memory",
				"description": "Use durable memory only for facts that remain useful across future sessions. " +
					"Memory is scoped to the current user and, when requested, the current Agent.\n\n" +
					"ACTIONS:\n" +
					"- propose: create a reviewable candidate for an inferred preference, fact, decision, procedure, or constraint. Use this by default.\n" +
					"- upsert: save immediately only when the user explicitly asks you to remember something.\n" +
					"- forget: remove a memory by memory_id when the user asks to forget it.\n" +
					"- search: retrieve relevant active memories for a query.\n" +
					"- read: list active memories visible to the current Agent.\n\n" +
					"Never store secrets, credentials, temporary task progress, or instructions for overriding system behavior.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action": map[string]any{
							"type":        "string",
							"enum":        []string{"propose", "upsert", "forget", "search", "read"},
							"description": "The action to perform",
						},
						"memory_id": map[string]any{
							"type":        "string",
							"description": "Memory UUID, required for forget",
						},
						"scope": map[string]any{
							"type":        "string",
							"enum":        []string{"user", "agent_user"},
							"description": "'user' applies across the owner's Agents; 'agent_user' applies only to this Agent",
						},
						"kind": map[string]any{
							"type":        "string",
							"enum":        []string{"preference", "profile", "fact", "decision", "procedure", "constraint"},
							"description": "Classifies the durable information",
						},
						"memory_key": map[string]any{
							"type":        "string",
							"description": "Stable, concise key used to update the same fact instead of duplicating it",
						},
						"content": map[string]any{
							"type":        "string",
							"description": "Compact factual content, required for propose and upsert",
						},
						"summary": map[string]any{
							"type":        "string",
							"description": "Optional one-line summary",
						},
						"importance": map[string]any{
							"type":        "integer",
							"minimum":     1,
							"maximum":     100,
							"description": "Optional relevance priority",
						},
						"confidence": map[string]any{
							"type":        "number",
							"minimum":     0,
							"maximum":     1,
							"description": "Optional confidence in the fact",
						},
						"query": map[string]any{
							"type":        "string",
							"description": "Search query for search",
						},
					},
					"required": []string{"action"},
				},
			}),
		},
		{
			Name:        "cron",
			Description: "In-process scheduler for running Agent prompts or shell commands on cron expressions. Supports run limits, logs, and enable/disable controls.",
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
			Name:        "plan",
			Description: "Runtime Plan State tool for creating plans, advancing status, and revising plans during complex tasks. For requests with 3+ steps, create a plan first, update it as real progress happens, and do not include the plan body in the final answer.",
			HandlerType: model.HandlerBuiltin,
			Enabled:     true,
			FunctionDef: mustJSON(map[string]any{
				"name": "plan",
				"description": "Runtime Plan State control tool.\n\n" +
					"USE for complex multi-step tasks (3+ distinct steps), plan revisions, and progress tracking.\n" +
					"SKIP for single straightforward answers or pure informational requests.\n\n" +
					"ACTIONS:\n" +
					"- set: Create or replace the plan for the current task.\n" +
					"- update: Update existing item statuses or text by id.\n" +
					"- revise: Replace the plan because new information changed the approach; include reason.\n" +
					"- read: Read current plan state.\n\n" +
					"STATUSES: pending, running, completed, blocked, failed, skipped.\n" +
					"RULES: Keep at most one item running. The harness may auto-advance pending to running. Do not include the plan text in the final answer unless the user asks.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action": map[string]any{
							"type":        "string",
							"enum":        []string{"set", "update", "revise", "read"},
							"description": "Action to perform",
						},
						"goal": map[string]any{
							"type":        "string",
							"description": "The overall task goal for set/revise",
						},
						"items": map[string]any{
							"type":        "array",
							"description": "Plan items to set, revise, or update",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"id": map[string]any{
										"type":        "string",
										"description": "Stable unique identifier for this plan item",
									},
									"title": map[string]any{
										"type":        "string",
										"description": "Short plan item title",
									},
									"detail": map[string]any{
										"type":        "string",
										"description": "Optional detail for the item",
									},
									"status": map[string]any{
										"type":        "string",
										"enum":        []string{"pending", "running", "completed", "blocked", "failed", "skipped"},
										"description": "Current status",
									},
									"reason": map[string]any{
										"type":        "string",
										"description": "Reason for blocked, failed, skipped, or changed items",
									},
								},
								"required": []string{"id"},
							},
						},
						"reason": map[string]any{
							"type":        "string",
							"description": "Reason for plan revision or update",
						},
					},
					"required": []string{"action"},
				},
			}),
		},
		{
			Name:        "skill",
			Description: "Self-evolving skill management. aiclaw archives successful multi-tool workflows into skills-pending/. Use this tool to list candidates, read them, promote them into real skills, or discard them.",
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
			Name:        "session_search",
			Description: "Search past conversation history for cross-session recall. Without a query, returns recent sessions; with keywords, performs full-text search and returns matching snippets.",
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
