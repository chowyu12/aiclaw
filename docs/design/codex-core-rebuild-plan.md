# Codex core rebuild plan

This replaces AIClaw's current request-oriented executor architecture with a
Codex-style thread and rollout runtime. The local terminal app remains a
consumer of the runtime protocol, not the owner of execution state.

## Target layers

1. **Protocol** — typed inbound commands and outbound lifecycle events:
   thread creation/resume, turn start, assistant deltas, reasoning deltas,
   tool lifecycle, plan changes, errors and turn completion.
2. **Thread store** — SQLite-backed durable thread metadata plus append-only
   rollout items. It owns read, list, archive, fork and revert operations.
3. **Rollout** — records every model-visible item and tool result in order,
   then rebuilds bounded model context when a thread is resumed.
4. **Session and turn** — one active turn per thread; a turn owns cancellation,
   context capture, model sampling, tool dispatch and event emission.
5. **Provider and tools adapters** — adapt AIClaw's user-configured Provider,
   tool registry, MCP manager and search handlers behind the new interfaces.
6. **Local app** — renders protocol events and sends commands; it does not
   invoke an executor directly.

## Data migration

Existing `Conversation` rows will become legacy thread projections. A migration
will create a Thread for each conversation and translate Messages, execution
steps, plans and run boundaries into ordered rollout items. Existing Provider,
MCP, search and memory records stay in place and are referenced by the new
thread/session configuration.

## Compatibility boundaries

- Provider records continue to own endpoint, credentials and selected model.
- SQLite is the sole runtime store for the local application.
- MCP and web-search tools remain normal turn tools and their results are
  persisted as rollout items.
- HTTP handlers are removed from the local runtime path; no new protocol will
  depend on the old web API or SSE format.

## Delivery order

The protocol and SQLite thread/rollout store land first with replay tests.
The session/turn engine then replaces `agent.Executor`, followed by the App
event consumer and legacy data migration. Each stage must support resume before
the next stage can replace the preceding layer.
