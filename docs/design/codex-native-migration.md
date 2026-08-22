# Codex-style local application migration

AIClaw now uses the same local-first separation used by Codex: a local
interaction client sits above an execution core and a durable local state
store. It does not start the former HTTP/Vue console when the `aiclaw` binary
is launched.

| Codex area | AIClaw implementation |
| --- | --- |
| TUI local client | `internal/app`: foreground terminal application and slash commands |
| configuration | `internal/config`: persisted YAML configuration |
| thread store | `Conversation`, `Message`, `AgentRun`, steps and plans in SQLite |
| model provider | `Provider` records selected by each Agent; API credentials remain user-configured |
| agent core | `internal/agent.Executor`, tools, memory, plans, validation and subagents |
| MCP client | `internal/tools/mcp.Manager` using local database MCP records |
| web search | existing built-in or external search configuration selected per Agent |
| local runtime | `internal/runtimeclient` discovers locally installed coding-agent CLIs |

## Runtime rules

- The native app always uses SQLite at `<workspace>/aiclaw.db`; it never starts
  a setup web server or requires a web token.
- Provider settings are not replaced by a built-in model vendor. Providers,
  models and API keys remain user-owned database records.
- Conversations, run history, plans, memories, MCP configuration and search
  configuration remain local database records and are available after restart.
- `aiclaw` and `aiclaw start` open the foreground app. `stop`, `restart`, and
  `status` no longer manage a background web service.

## Local commands

`/provider`, `/agent`, `/conversation`, `/resume`, `/new`, `/mcp`, `/search`
and `/status` expose the local control plane. Plain text is submitted to the
selected Agent and is persisted to the selected local conversation. `/help`
lists the exact command syntax.
