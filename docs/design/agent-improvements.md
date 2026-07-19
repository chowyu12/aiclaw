# AiClaw Agent Architecture Improvements

This document is the high-level map of the current Agent architecture. Detailed
protocols live alongside it in `agent-plan-state.md`, `agent-harness-runtime.md`,
`agent-runs.md`, and `memory-system.md`.

## Runtime Shape

```text
user request
  -> durable AgentRun
  -> bootstrap: history + compact memory + compact plan state
  -> LLM / local Agent runtime
  -> tools, sub-agents, generated files, and evidence
  -> harness validation and correction
  -> assistant message + plan snapshot + memory usage links
  -> SSE replay / completed-run API / execution logs
```

The executor is the control plane. Models can request actions through tools,
but they do not own lifecycle state, access boundaries, or persistence rules.

## Plan State

Plan State replaced the earlier chat-visible TODO design. The internal `plan`
control tool lets a model propose `set`, `update`, `revise`, or `read` changes.
The harness validates the transition, persists it, sends a compact state block
to later turns, emits streaming updates, and links the final snapshot to the
assistant message.

```text
pending -> running -> completed
                   -> failed
                   -> blocked
pending -> skipped
```

Only one item can run at a time. If a plan has no running item, the harness
starts the first pending item. A terminal execution failure marks the active
item failed. A validated final response closes a remaining running item.
Tool success is evidence, not an implicit plan transition.

## Durable Memory

The Memory System stores each memory as a database record owned by a user, optionally
bound to an Agent, and tracked through revisions and evidence. The memory
control tool receives user and Agent identity from execution context; a model
cannot select another memory namespace.

At execution bootstrap the system retrieves only active, unexpired, in-scope
memories. It compiles a bounded `<memory_context>` block that explicitly treats
records as potentially stale facts rather than instructions. Candidate and
workspace-import records are never injected. See `memory-system.md` for the
state machine and schema.

## Session Search Isolation

`session_search` is scoped to the current execution identity. Its recent-list
path calls `ListConversations` with that user ID, and both FTS5 and SQL fallback
queries filter by `conversations.user_id`. This keeps historical conversation
search from crossing user boundaries.

## Durable Runs and Streaming

Chat starts a persistent `AgentRun` before a response begins. SSE is a delivery
mechanism, not the lifetime of work: the browser can reconnect by run ID and
receive replayed events, completed snapshots, steps, attachments, Plan State,
and memory usage snapshots. Periodic `ping` events keep idle intermediaries
from closing long-running streams.

## Harness Validation

The harness implements `Contract -> Evidence -> Validate -> Correct` across
pre-tool, post-tool, pre-final, and pre-save stages. It records tool outcomes,
files, plan state, and validation decisions as execution evidence. If a model
tries to finish without required output or supporting evidence, the harness
adds a correction prompt within the configured budget instead of immediately
saving an incomplete reply.

## Local Agent Runtimes

Local CLI Agents use the same durable run records as built-in LLM Agents. A
runtime task includes the user request, conversation context, compact Plan
State, and compact Memory System context. The local runtime claims work by run
ID, writes stdout-derived output back to the run, and the executor associates
the final assistant message, files, plan, and memory evidence exactly once.
