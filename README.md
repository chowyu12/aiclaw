# AiClaw

AiClaw is a self-hosted AI agent platform for building, operating, and observing tool-using agents. It combines a Go backend, a Vue 3 admin console, multi-provider LLM support, nested sub-agents, persistent memory, runtime planning, tool execution, and messaging-channel integrations in a single deployable binary.

It is designed for people who want an agent system that can do real work: read and edit files, run commands, browse the web, call custom tools, coordinate sub-agents, remember useful context, and expose the same agents through web chat, APIs, and external messaging channels.

## Highlights

- Multi-agent management with per-agent prompts, model settings, tools, skills, MCP servers, and token budgets.
- Runtime Plan State for complex tasks, with live streaming progress and final plan snapshots.
- Harness runtime validation with contract, evidence, validation gates, correction prompts, and traceable self-check steps.
- Nested `sub_agent` execution for parallel research, exploration, shell work, and delegated tasks.
- Built-in tools for files, shell commands, browser automation, web search, web fetching, scheduled jobs, code interpretation, memory, session search, and skills.
- Two-level web search configuration: model-native search for supported models, or external search engines such as Tavily, SerpAPI, and Aliyun IQS.
- Persistent conversations, execution steps, generated files, and plan state in SQLite, MySQL, or PostgreSQL.
- Web console for providers, agents, tools, skills, channels, chat, and execution logs.
- Local agent runtimes that connect outward, execute user-configured CLI agents, and stream replies into the same chat UI.
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

AiClaw has five major runtime layers:

| Layer | Purpose |
| --- | --- |
| Web console | Configure providers, agents, tools, skills, channels, chat, and inspect logs. |
| Agent executor | Builds prompts, calls LLMs, runs tools, manages Plan State, tracks files, and streams output. |
| Harness runtime | Turns the user objective into a task contract, records evidence, validates tool/final/save stages, and asks the model to correct incomplete work. |
| Tool system | Built-in tools, custom HTTP tools, custom command tools, MCP tools, and skill-defined tools. |
| Persistence | Conversations, messages, execution steps, generated files, memory, schedules, and runtime plans. |
| Local runtime client | Claims queued local-Agent runs and launches argv commands on the user's machine without a shell. |

The normal execution loop is:

1. Load the agent, provider, tools, skills, memory, files, and conversation history.
2. Inject compact runtime context, including persistent memory and current Plan State.
3. Call the model with function-calling tools.
4. Validate requested tools, execute allowed tool calls, track steps, collect generated files, and stream updates.
5. Link each business-tool step to its active Plan item as evidence; only explicit `plan` updates and final lifecycle closure change item status.
6. Validate candidate final answers; if a response is empty, progress-only, missing required evidence, or missing promised artifacts, inject a correction prompt and continue.
7. Validate the final content before saving, then persist the assistant message, files, execution timeline, and plan snapshot.

## Runtime Plan State

AiClaw uses Plan State instead of chat-visible TODO blocks. The model proposes plan changes through the internal `plan` control tool, while the agent harness owns validation, persistence, lifecycle transitions, streaming, and failure recovery.

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
- If no item is running, the harness initializes the first pending item as `running`.
- Tool outcomes are evidence, not implicit status transitions. The model uses `plan` to complete, block, skip, or revise items.
- A terminal execution failure marks the active item as `failed`; a validated final response closes any still-running item.
- The final assistant message is linked to the final plan snapshot.
- Streaming chat and execution logs show the plan separately from the assistant's answer.

This keeps progress visible without polluting the final response body or ordinary tool-call history.

## Durable Agent Runs

Chat keeps using Server-Sent Events for low-latency updates, but an SSE
connection is no longer the lifetime of an Agent turn. Every top-level turn is
stored as an `AgentRun` with a stable run ID, input, status, final message,
token count, duration, and execution steps. Each execution step also carries
the run ID, which makes concurrent histories and reconnects unambiguous.

The web chat starts a background run and then subscribes to it:

1. `POST /api/v1/chat/runs` creates the durable run and immediately returns its `uuid`.
2. `GET /api/v1/agent-runs/{runID}/stream` streams existing `message` and `harness` SSE events, plus lifecycle `run` events and periodic `ping` events.
3. `GET /api/v1/agent-runs/{runID}` returns the durable final snapshot, including steps, generated files, and Plan State, for page reloads or missed live events.
4. `DELETE /api/v1/agent-runs/{runID}` requests cancellation without deleting the audit trail.

Disconnecting or refreshing the browser only detaches that subscriber. The run
continues in the executor until it succeeds, fails, or is explicitly cancelled.
The live event hub keeps a bounded replay for short reconnects; the database is
the source of truth for completed runs and after a service restart.

## Local Agent Runtimes

AiClaw is local-first. At startup it creates a built-in **Local** runtime,
scans the server process `PATH`, and executes detected agent CLIs in the same
process. No connection command, extra daemon, or token is required.

1. Install and authenticate the desired agent CLI on the machine that runs
   AiClaw.
2. Start AiClaw and open **Runtimes** to verify the automatically detected
   CLIs.
3. Create an Agent with execution mode **Local**. The built-in runtime is
   selected automatically; choose one detected CLI and an optional working
   directory.
4. Select that Agent in Chat. AiClaw runs the CLI directly and streams stdout
   into the durable agent run.

Runtime commands are executed directly as `command + args`; they are never
passed through a shell.

### Optional remote runtime

To execute an agent CLI on another machine, add a **Remote Runtime** in the
Runtimes page, then run the generated command on that machine:

```bash
aiclaw runtime connect --server https://your-aiclaw.example.com --token rt-...
```

The remote runtime connects outbound, scans its own `PATH`, and becomes
available alongside the built-in local runtime. This is optional; it is not
needed for the machine running AiClaw itself.

The built-in runtime currently auto-detects these non-interactive CLI agents:

| CLI | Detected executable | Prompt delivery |
| --- | --- | --- |
| OpenAI Codex | `codex app-server --listen stdio://` | JSON-RPC |
| Cursor | `cursor-agent -p --output-format stream-json --yolo` | JSONL events |
| Claude Code | `claude -p --output-format stream-json --input-format stream-json` | stream-json |
| Tencent CodeBuddy | `codebuddy -p --output-format stream-json --input-format stream-json` | stream-json |
| OpenClaw | `openclaw agent --local --json --session-id …` | JSON result |
| Hermes Agent | `hermes acp` | Agent Communication Protocol (ACP) |

Expand a runtime in the Runtimes page to manage each detected CLI separately.
You can enable or disable it, and set its default model for future local tasks.
Codex, Cursor, Claude Code, and CodeBuddy receive the configured model through
their respective provider protocol. Hermes switches the ACP session model.
For OpenClaw, the field selects a registered OpenClaw Agent ID; that Agent owns
the actual model configuration.

AiClaw preserves local CLI login and provider configuration. Headless provider
protocols auto-approve their task-local tool requests, so only enable them in
workspaces you trust. The
similarly named WorkBuddy product does not currently have a documented
standalone headless CLI; it needs an official API or CLI contract before it can
be added to automatic discovery.

## Harness Runtime

AiClaw exposes a stable `pkg/harness` package for both harness events and execution validation. The executor still owns the main loop: the control plane handles budget, plan lifecycle closure, and persistence, while the verifier layer owns four validation stages:

| Stage | Purpose |
| --- | --- |
| `pre_tool` | Enforce tool policy before a tool call is executed. |
| `post_tool` | Record tool results, failures, and generated files into the evidence ledger. |
| `pre_final` | Gate candidate final answers before the run can finish. |
| `pre_save` | Recheck the final content after attachment links are rendered and before the assistant message is stored. |

The runtime is based on `Contract -> Evidence -> Validate -> Correct`:

- `TaskContract` captures the objective, output mode, required evidence, artifact expectations, plan requirements, and correction budget.
- `EvidenceLedger` records execution tools, tool events, generated file evidence, plan snapshots, validation events, and correction events.
- Validators reject empty final answers, progress-only final answers, unfinished plans, missing successful evidence after failed tools, missing artifacts, and invalid structured JSON when enabled.
- Failed validation creates `harness` execution steps and, while attempts remain, appends a correction prompt to the next model round.

This makes the execution trace explicit: the model can propose completion, but the harness decides whether there is enough evidence to finish.

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
| `web_search` | Search the web through an enabled external search engine selected by the agent. |
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

## Web Search

Agents can use web search in two modes:

| Mode | Behavior |
| --- | --- |
| Built-in | For models whose capability profile supports web search, AiClaw sends `extra_body: {"enable_search": true}` with the model request and records a `web_search` execution step showing the search input and request configuration. |
| External | AiClaw exposes the `web_search` tool to the agent and routes calls through the selected search engine configuration. Tool input, output, duration, and errors are visible in chat progress and execution logs. |

External search engines are configured from the web console's Search Engine menu. Multiple configurations can be saved, enabled or disabled independently, tested from the list, and tested directly from the create/edit dialog before saving. Supported providers:

| Provider | Notes |
| --- | --- |
| Tavily | Uses the Tavily search API. |
| SerpAPI | Uses SerpAPI organic search results. |
| Aliyun IQS | Uses Alibaba Cloud IQS `POST https://cloud-iqs.aliyuncs.com/search/unified` with Bearer API key authentication and the `LiteAdvanced` engine. |

When an agent uses external mode, it must select an enabled search engine. Disabled configurations can still be tested from the Search Engine page, but they are not available for live agent execution.

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

Each provider can define its base URL, API key, and model list. Agents may define a fast model for lightweight sub-agent tasks and a separate fallback model for transient primary-model failures. Fallbacks are used only when their known streaming and tool-calling capabilities are compatible with the active request.

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
- `harness`: a stable `harness.v1` protocol event, sent as its own SSE event type.
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
- Built-in and external web search activity as `web_search` steps.
- Harness validation and correction activity as `harness` steps.
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
- `pkg/harness/` for the stable harness event protocol and runtime validation primitives.
- `internal/tools/` for built-in tool implementations.
- `internal/store/` for persistence interfaces and GORM storage.
- `internal/handler/` for HTTP handlers.
- `web/src/` for the Vue admin console.

## License

No repository license file is currently declared. Confirm usage and redistribution terms with the project owner before using AiClaw in production or distributing binaries.
