# Code Mode execution contract

AIClaw keeps the Goja JavaScript engine and uses a disposable `claw-agent code-mode-worker`
process for each production cell. This is tool orchestration, not a Node.js environment:
there is no `require`, direct filesystem, network or process API in JavaScript. Tools,
approvals and credentials remain in the parent Agent. The worker receives only the admitted
catalog, source and execution limits; inherited application credentials are not forwarded.

The comparison baseline is public Codex main
[`e0370f0`](https://github.com/openai/codex/tree/e0370f0b0693ad75b7d07b4d5b79aeaf3995b199)
and release [`rust-v0.167.0`](https://github.com/openai/codex/releases/tag/rust-v0.167.0).
This is an independent implementation, not a claim of desktop-runtime parity.

## Discovery and session state

The initial prompt includes at most 40 complete input signatures. `ALL_TOOLS` lists callable
names and descriptions. `tools.tool_search({query, limit:8})` ranks lexical name/description
matches; `tools.tool_describe({name})` retrieves one exact tool's full description, input
schema and TypeScript declaration. This is not semantic search or Codex's BM25 implementation.
Inspect unfamiliar declarations before invoking a tool. Each admitted cell retains its catalog
snapshot; subsequent cells receive refreshed MCP definitions.

The parent owns serialized `store/load` values. Refreshing tools replaces only the catalog,
not the store, admission gate or cell manager. An idle settings remount copies serialized data
to the replacement live session; it never transfers old approvals, tool bindings or cell IDs. Values are copied JSON snapshots: mutating a
loaded object does not save it. Limits are 1 MiB per value, 8 MiB and 256 keys per live chat;
keys contain 1–256 UTF-8 bytes. This store survives worker replacement and catalog refresh,
not application restart or chat fork. Functions and circular objects cannot be stored.

## Execution and observation

`exec` remains a JSON function tool for provider compatibility. It takes `title`, `code`,
optional `yield_time_ms` (default 30000, range 0–60000) and `max_output_tokens` (default
6000, range 1–6144). Text budgeting approximates one token as four UTF-8 bytes; it is not a
tokenizer guarantee. Media has a separate budget.

A short script returns normally. A long script returns `Script running with cell ID ...`.
Call top-level `wait({cell_id, yield_time_ms, max_output_tokens})` for only the new output,
or add `terminate:true` to cancel and join its nested calls. Observing never reruns source
or replays actions. A completed, collected cell cannot be reopened. There are at most eight
uncollected cells per session; cells have a ten-minute wall-clock ceiling and stop when their
originating turn ends, is interrupted or is steered. The turn joins canceled workers before
emitting completion. Uncollected running cells cause an explicit incomplete-turn error.

`notify(value)` prints and requests a yield; `yield_control()` requests a yield without
stopping execution. Bounded `setTimeout`/`clearTimeout` support awaited delays (maximum
60000 ms, 200 outstanding timers). Unawaited timers do not keep a script alive. Already
admitted tool calls are drained or canceled and joined, preserving AIClaw's existing policy.
Tool waiting is excluded from the 30-second active-VM budget. A cell admits at most 200 tool
calls. Independent reads may overlap; writes and unknown effects exclude all other nested
calls across cells. Dependent and desktop actions must be explicitly awaited in order.

## Tool evidence and media

Direct and nested tools share recording, timeout and cancellation logic. Nested handlers
receive the same current approval environment. Each child emits start/completion events with
its parent, timing, failure, sources and artifacts. Child evidence is retained as UI state,
including an uncertain record if completion is absent. It never becomes extra model tool
messages. Only the script-selected output enters model history.

Text-only MCP results retain their prior text/JSON behavior. In Code Mode, results containing
media or `structuredContent` preserve the MCP envelope. Select individual content blocks:

```js
const result = await tools.some_mcp_tool({});
const picture = result.content.find(item => item.type === 'image');
if (picture) image(picture);
```

`image`, `audio` and `generatedImage` accept matching base64 data URLs or individual MCP
media blocks, never HTTP URLs. Images are attached to model input and explicit image output
is shown inside the execution step. Generated images and audio are saved as artifacts through
the current file-write approval policy. Audio is playable in the step; it is not silently
sent as unsupported native audio to a text-only provider. Use `transcribe_audio` on the saved
file when transcription is needed. Explicit output metadata survives chat restoration.

## Resource and recovery boundaries

Sources and arguments are capped at 1 MiB, media at 8 MiB per item, uncollected media at
16 items / 16 MiB per observation, IPC frames at 24 MiB, and single tool-result transport at
8 MiB. Text previews are bounded to 24 KiB and retained copies to 8 MiB with explicit
completeness metadata and `read_tool_output` pagination.

The worker uses Go's 128 MiB soft memory limit plus a sampled heap watchdog. This is not a
hard OS memory quota; a large allocation can exceed the threshold before the watchdog runs.
The separate process confines fatal VM/OOM failures to the worker. The parent retains session
state and starts a fresh worker for a later explicit execution. It never automatically retries
the failed cell or its side effects. Host tool implementations must honor cancellation; these
controls cannot roll back an operation already performed or force-cancel an uncooperative
external tool.

Verification covers catalog refresh, state copies/quotas, discovery declarations, incremental
wait, no replay, termination, nested evidence restoration, media/text separation, MCP errors,
real worker exhaustion/recovery and cancellation propagation. Tests use fixture tools and model
servers; they do not establish real-provider behavior or end-to-end desktop task quality.
