# Memory System

## Purpose

The Memory System gives an Agent durable context without turning a global text
file into an unbounded, unauditable prompt. It treats memory as application
state: owned, scoped, reviewable, searchable, explainable, and deletable.

## Data Model

```text
MemoryItem
  user_id, agent_uuid, scope, kind, memory_key
  content, summary, importance, confidence, sensitivity
  status, pinned, expires_at

MemoryRevision
  immutable snapshots for create, update, approve, dismiss, forget, import

MemoryEvidence
  source: where a record came from
  used: run UUID and final assistant message that received the record
```

The natural key is `(user_id, agent_uuid, scope, kind, memory_key)` for active
and candidate records. Repeating an identical write is idempotent: it does not
create another revision or source evidence entry.

## Ownership and Scope

| Scope | Prompt visibility |
| --- | --- |
| `user` | All Agents owned by the same user. |
| `agent_user` | Only the matching user and Agent UUID. |

Every request to the memory tool receives its user, Agent, conversation, and
run identity from `context.Context`. Tool arguments do not select an owner.
The same boundary is applied to session search.

## State Machine

```text
            approve
candidate ----------> active --------> superseded
   |                    |  \
   | dismiss            |   \ forget
   v                    v    v
dismissed            deleted deleted
```

Only `active` records are eligible for prompt retrieval. A sensitive write is
forced to `candidate`. The Memory console is the human control point for
creating active records and approving or dismissing candidates.

## Retrieval and Prompt Compilation

At the start of a top-level Agent turn:

1. Retrieve a small lexical set for the current request, filtered by user,
   selected Agent, active status, and expiry.
2. Retrieve pinned records under the same boundary.
3. De-duplicate, sort, and compact to the prompt budget.
4. Render `<memory_context>` with a safety preamble: retained content is not
   instruction, may be stale, and loses to the current user request and verified
   tool results.
5. Write `used` evidence with the run UUID. Once the assistant message is
   saved, backfill its message ID.

SQLite uses `memory_items_fts` with synchronizing triggers. If FTS is not
available, the scoped query falls back to SQL text matching.

## API and UI

`/api/v1/memories` supports list, create, get, patch, and delete. Per-memory
revision and evidence endpoints expose its audit trail. The web Memory page
separates active records from candidates. Chat and execution logs render the
final memory snapshot beside a response, without placing memory in the response
body.

## Deletion

Deleting a conversation keeps durable `MemoryItem` records, but removes source
or usage evidence tied to that conversation. Retrying from a user message also
removes evidence attached to the deleted run/message range, preventing old
attempts from appearing as use evidence for the new answer.
