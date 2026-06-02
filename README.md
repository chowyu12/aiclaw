# AiClaw

AiClaw is a self-hosted AI agent platform for building, operating, and observing tool-using agents. It combines a Go backend, a Vue 3 admin console, multi-provider LLM support, nested sub-agents, persistent memory, runtime planning, tool execution, and messaging-channel integrations in a single deployable binary.

It is designed for people who want an agent system that can do real work: read and edit files, run commands, browse the web, call custom tools, coordinate sub-agents, remember useful context, and expose the same agents through web chat, APIs, and external messaging channels.

## Highlights

- Multi-agent management with per-agent prompts, model settings, tools, skills, MCP servers, and token budgets.
- Runtime Plan State for complex tasks, with live streaming progress and final plan snapshots.
- Nested `sub_agent` execution for parallel research, exploration, shell work, and delegated tasks.
- Built-in tools for files, shell commands, browser automation, web fetching, scheduled jobs, code interpretation, memory, session search, and skills.
- Persistent conversations, execution steps, generated files, and plan state in SQLite, MySQL, or PostgreSQL.
- Web console for providers, agents, tools, skills, channels, chat, and execution logs.
- Messaging-channel integrations for WeCom, WeChat, Feishu, DingTalk, WhatsApp, and Telegram.
- Single-binary deployment with the frontend embedded into the Go server.

## Quick Install

AiClaw publishes prebuilt binaries for Linux and macOS on amd64 and arm64.

```bash
curl -fsSL https://raw.githubusercontent.com/chowyu12/aiclaw/master/install.sh | bash
```

The installer downloads the latest GitHub Release, installs the `aiclaw` binary, registers a system service, starts the server, and prints the web access URL with its login token.

Common commands:

```bash
aiclaw start
aiclaw stop
aiclaw status
aiclaw update
aiclaw version
```

By default, AiClaw stores configuration and runtime data under `~/.aiclaw/` and uses SQLite for the first run.

## What AiClaw Runs

AiClaw has four major runtime layers:

| Layer | Purpose |
| --- | --- |
| Web console | Configure providers, agents, tools, skills, channels, chat, and inspect logs. |
| Agent executor | Builds prompts, calls LLMs, runs tools, manages Plan State, tracks files, and streams output. |
| Tool system | Built-in tools, custom HTTP tools, custom command tools, MCP tools, and skill-defined tools. |
| Persistence | Conversations, messages, execution steps, generated files, memory, schedules, and runtime plans. |

The normal execution loop is:

1. Load the agent, provider, tools, skills, memory, files, and conversation history.
2. Inject compact runtime context, including persistent memory and current Plan State.
3. Call the model with function-calling tools.
4. Execute tool calls, track steps, collect generated files, and stream updates.
5. Let the harness advance Plan State after success or failure.
6. Save the final assistant message, files, execution timeline, and plan snapshot.

## Runtime Plan State

AiClaw uses Plan State instead of chat-visible TODO blocks. The model proposes plan changes through the internal `plan` control tool, while the executor harness owns validation, persistence, lifecycle transitions, streaming, and failure recovery.

Plan item states:

```text
pending -> running -> completed
                  -> failed
                  -> blocked
pending -> skipped
```

Plan State behavior:

- Complex tasks can start with a structured plan.
- Only one item can be `running` in a single plan.
- If no item is running, the harness advances the first pending item.
- Tool or LLM failures mark the running item as `failed`.
- Successful tool rounds can mark the running item as `completed` and advance the next item.
- The final assistant message is linked to the final plan snapshot.
- Streaming chat and execution logs show the plan separately from the assistant's answer.

This keeps progress visible without polluting the final response body or ordinary tool-call history.

## Sub-Agents

Agents can delegate work to child agents through the `sub_agent` tool. Sub-agents have isolated context and their own execution traces, but their steps are still visible under the parent execution timeline.

Execution modes:

| Mode | Tool profile | Best for |
| --- | --- | --- |
| `auto` | Full available toolset, subject to safety limits. | General delegated tasks. |
| `explore` | Read-only exploration tools. | Codebase inspection, research, planning before edits. |
| `shell` | Command-oriented tools. | Build, test, diagnostics, and operational checks. |

Sub-agents can use the parent agent configuration or a selected agent UUID. They can also request the `fast` model profile when the parent agent has one configured.

## Memory And Context

AiClaw separates memory by lifetime and injection point:

| System | Description |
| --- | --- |
| Persistent memory | `MEMORY.md` and `USER.md` style long-lived facts written through the `memory` tool. |
| Plan State | Runtime task progress stored per assistant response. |
| Session archive | Long conversation snapshots for continuation workflows. |
| History loading | Structured truncation keeps recent turns detailed and older turns compact. |
| Runtime compression | Long middle context can be summarized during execution. |
| Skill crystallization | Successful multi-tool workflows can be saved as reusable skill candidates. |

SQLite installations can use FTS5-backed session search. Other databases fall back to SQL text search.

## Built-In Tools

AiClaw includes a broad default toolset:

| Tool | Purpose |
| --- | --- |
| `read` | Read text files and pass images to vision-capable models. |
| `write` | Create or overwrite files. |
| `edit` | Precise find-and-replace editing. |
| `grep` | Regex search over file contents. |
| `find` | Glob-style file discovery. |
| `ls` | Directory listing. |
| `exec` | Run shell commands with working directory and timeout controls. |
| `process` | Manage long-running background command sessions. |
| `web_fetch` | Fetch readable content from URLs with browser fallback. |
| `browser` | Browser automation for navigation, screenshots, snapshots, forms, storage, console, and network inspection. |
| `canvas` | Render HTML/CSS/JS and capture canvas snapshots. |
| `cron` | In-process scheduled jobs with persistence and logs. |
| `code_interpreter` | Execute Python, JavaScript, or shell snippets in an interpreter workflow. |
| `current_time` | Read the current system time. |
| `sub_agent` | Delegate work to child agents. |
| `memory` | Add, replace, remove, read, or recall persistent memory. |
| `session_search` | Search previous conversations. |
| `plan` | Internal runtime planning control. |
| `skill` | List, inspect, promote, or discard generated skill candidates. |

You can also add:

- Custom HTTP tools.
- Custom command tools.
- MCP server tools.
- Skill-provided tools.

## Skills

Skills are reusable agent instructions and optional executable tool bundles. A skill lives in its own directory under `~/.aiclaw/skills/`.

Example structure:

```text
~/.aiclaw/skills/
  brave-web-search/
    SKILL.md
    manifest.json
    index.js
    README.md
```

`SKILL.md` contains the instructions shown to agents. `manifest.json` can declare callable tools, metadata, and runtime settings. Executable skills can implement tool logic in JavaScript or Python.

AiClaw uses two-stage skill loading: it injects skill names, descriptions, and paths first; the model can then read a skill's full instructions only when needed. This keeps prompts smaller when many skills are installed.

## Messaging Channels

AiClaw can expose agents through external channels:

| Channel | Integration style |
| --- | --- |
| WeCom | WebSocket connection. |
| WeChat | iLink polling. |
| Feishu | Webhook. |
| DingTalk | Webhook. |
| WhatsApp | Webhook. |
| Telegram | Webhook plus chat action support. |

Channel conversations are stored alongside web conversations, and execution details can be inspected in the admin console.

## Providers

AiClaw supports multiple model providers:

- OpenAI.
- OpenAI-compatible APIs.
- Qwen.
- Kimi / Moonshot.
- OpenRouter.
- Anthropic Claude.
- Google Gemini.

Each provider can define its base URL, API key, and model list. Agents choose a provider model and can optionally define a fast model for lightweight sub-agent tasks.

## Configuration

The default config file is created at:

```text
~/.aiclaw/config.yaml
```

A typical local configuration uses SQLite:

```yaml
server:
  addr: ":8080"

database:
  driver: sqlite
  dsn: ~/.aiclaw/aiclaw.db

log:
  level: info
  file: ~/.aiclaw/logs/aiclaw.log
```

For production, configure MySQL or PostgreSQL and run AiClaw behind your preferred reverse proxy.

## Build From Source

Requirements:

- Go 1.25 or newer.
- Node.js 18 or newer.
- SQLite, MySQL, or PostgreSQL.

Clone and build:

```bash
git clone https://github.com/chowyu12/aiclaw.git
cd aiclaw

npm --prefix web install
npm --prefix web run build

go build -o aiclaw ./cmd/server
```

Run locally:

```bash
./aiclaw start
```

For development, run backend and frontend tasks separately as needed:

```bash
go run ./cmd/server
npm --prefix web run dev
```

## API Usage

AiClaw supports blocking and streaming chat execution. Agents can also expose token-based API access with `ag-` prefixed tokens.

Blocking request example:

```bash
curl -X POST http://localhost:8080/api/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <web-token-or-agent-token>" \
  -d '{
    "agent_uuid": "<agent-uuid>",
    "user_id": "default",
    "message": "Summarize this repository and list the main risks."
  }'
```

Streaming responses use SSE and can include:

- `delta`: assistant text.
- `step`: a single execution step update.
- `steps`: final or batched execution steps.
- `plan`: current or final Plan State snapshot.
- `files`: files produced during execution.
- `done`: completion marker.

## Observability

AiClaw records:

- LLM call steps.
- Tool call steps.
- Sub-agent child steps.
- Skill matching.
- Generated files.
- Token usage.
- Step duration.
- Errors.
- Final Plan State snapshots.

The execution log page keeps the assistant response, plan snapshot, and step timeline separate so you can understand both the high-level plan and the low-level tool activity.

## Release Builds

Release tags use semantic versions such as `v1.10.0`. Pushing a `v*` tag triggers the GitHub Actions release workflow, which builds:

- `aiclaw-linux-amd64`
- `aiclaw-linux-arm64`
- `aiclaw-darwin-amd64`
- `aiclaw-darwin-arm64`
- `aiclaw-windows-amd64.exe`

Linux and Windows binaries are compressed with UPX. macOS binaries are left uncompressed to avoid Mach-O and Gatekeeper issues.

## Project Status

AiClaw is actively evolving. The architecture is intentionally pragmatic: it favors explicit execution traces, durable runtime state, and inspectable behavior over invisible agent magic.

Useful starting points:

- `internal/agent/` for the executor, Plan State, sub-agents, prompts, and tool execution.
- `internal/tools/` for built-in tool implementations.
- `internal/store/` for persistence interfaces and GORM storage.
- `internal/handler/` for HTTP handlers.
- `web/src/` for the Vue admin console.

## License

No repository license file is currently declared. Confirm usage and redistribution terms with the project owner before using AiClaw in production or distributing binaries.
