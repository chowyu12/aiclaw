# Durable Agent Runs

## Goal

AiClaw streams chat progress over SSE, but a browser connection is an
unreliable execution boundary. An Agent run must survive page navigation,
transient network loss, and SSE reconnects while remaining observable through
a stable identifier.

`AgentRun` is the durable execution boundary for one top-level Agent turn.
Plan State and messages remain conversation-centric, while `AgentRun` links
the input, final message, timing, status, and execution steps into one
replayable record.

## Lifecycle

```text
POST /chat/runs
  -> persist AgentRun(status=running, uuid)
  -> start background executor context
  -> GET /agent-runs/{uuid}/stream subscribes to live events
  -> persist terminal status and final message reference
  -> GET /agent-runs/{uuid} restores the completed snapshot
```

Run states are deliberately small and terminal:

```text
running -> succeeded
running -> failed
running -> cancelled
```

A conversation accepts one active background run at a time. Plan State is
conversation-scoped, so overlapping runs would otherwise compete for the same
active Plan item and unassigned execution steps.

## Persistence

`agent_runs` contains:

- run UUID, Agent and conversation references, user ID, and original input;
- `running`, `succeeded`, `failed`, or `cancelled` status;
- final assistant `message_id`, rendered content, token usage, duration,
  error text, start time, and finish time.

`execution_steps.run_uuid` is assigned before the harness begins. When the
assistant message is saved, the tracker links only steps for that run to its
`message_id`; this prevents one simultaneous or legacy request from claiming
another run's timeline.

Deleting a conversation or deleting messages from a retry boundary also deletes
the associated `AgentRun` rows. The HTTP delete and retry flows reject a
conversation with a live background run; clients must cancel it first, which
prevents an in-flight executor from writing new orphaned records after cleanup.

## Streaming Protocol

The run stream preserves existing SSE compatibility:

| SSE event | Payload | Purpose |
| --- | --- | --- |
| `message` | `StreamChunk` | Deltas, Plan State, execution steps, and final response. |
| `harness` | harness event | Stable control-plane and evidence trace. |
| `run` | `AgentRunEvent` | `run.started`, `run.completed`, `run.failed`, or `run.cancelled`. |
| `ping` | `{}` | Keeps proxies and browsers from idling out. |

Both `StreamChunk.run_id` and `harness.Event.run_id` use the durable AgentRun
UUID for top-level turns. The event hub is in-memory and keeps a bounded replay
for an active run. Publishing is non-blocking, so a slow SSE reader cannot
stall tools or model output. After the run reaches a terminal state, clients
recover through the persistent run detail endpoint instead of relying on hub
memory.

## Cancellation And Failure Recovery

The background executor uses `context.WithoutCancel` when it starts a run, so
the HTTP request that created it does not control its lifetime. An explicit
`DELETE /agent-runs/{runID}` signals the active run context. A running record
found after an executor restart has no live context to signal; cancellation
closes it persistently with an explanatory error value.

Terminal failure saves the normal error assistant message and links it to the
run. Cancellation is an intentional stop and does not add a synthetic error
message. In both cases the run record remains available for execution-log and
API inspection.

## Frontend Behavior

The chat page creates a run first, stores its conversation UUID immediately,
and then attaches SSE. If the page reloads after completion, message history
and the run detail endpoint reconstruct the final answer, Plan State, files,
and step timeline. Stopping generation sends an explicit run cancellation;
simply navigating away does not terminate server-side work.
