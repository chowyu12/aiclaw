# Independent desktop execution

AIClaw owns the model loop, approvals and transcript. The independent
**Open Computer Use** service owns application discovery, accessibility snapshots,
window targeting, screenshots and input. AIClaw pins `open-computer-use` **1.1.3**;
it no longer loads Cua Driver, UniFFI bindings or a native desktop library into
Electron. No global npm install or user MCP configuration is required.

## Execution and identity

The host directly starts the bundled native executable with `mcp`. On macOS its
CLI proxies via a Unix socket to a LaunchServices-launched **Open Computer Use.app**.
The upstream bundle ID (`com.ifuryst.opencomputeruse`), Developer ID signature and
bundle contents are preserved. macOS grants Accessibility and Screen Recording
to that app, separately from AIClaw or developer Electron. AIClaw deliberately
does not rename or re-sign the upstream helper. A separate app alone does not
prove permission persistence across updates; signing identity must remain stable.
On the first observation in each connection, the helper's read-only permission
probe checks its own grants. Missing grants open its permission onboarding and
return an error without dispatching input. Users do not need to locate the nested
app manually or run a hidden CLI. AIClaw never grants OS permissions itself.
Windows and Linux run the upstream native executable as a separate process.
The upstream Linux bridge additionally uses system Python 3, PyGObject/AT-SPI
and the desktop capture stack; the permission-free handshake does not verify
those graphical-session dependencies.

The macOS socket namespace includes the canonical executable path and pinned
version. Development, installed releases and package acceptance therefore cannot
replace one another's agent. Clients using the same runtime can share its app.

Each AIClaw chat/turn owns an MCP connection. The host checks protocol version
`2025-03-26`, server identity/version and the native tool manifest before input.
All desktop calls pass through an ordered host queue. End-of-turn, archive,
delete and shutdown close the corresponding connection and discard observations.
A new turn always requires a fresh observation. A late completion cannot close
a newer turn's connection. The upstream macOS app may remain resident after the
proxy exits; AIClaw does not terminate a shared service or install a launch daemon.

## Tool boundary

The fixed nine-tool subset is declared in
`tools/claw-agent/internal/agent/computer-tools.json`: list apps, get app state,
click, drag, key, type, scroll, set value and secondary accessibility action.
The host checks its own allowlist and the loaded native manifest. Old Cua PID,
window ID, snapshot-token and delivery-mode parameters are rejected.

Use `computer_list_apps` to select an app identifier. macOS requires the exact
bundle identifier, preventing fuzzy aliases from routing to AIClaw or Electron.
Windows/Linux require the exact PID from discovery and reject AIClaw's host PID.
AIClaw and helper identifiers/executable names are blocked. App observation
selects the app's key window; OCU owns its cached target window and element
indexes. The adapter does not promise arbitrary explicit-window selection.
`computer_screenshot` returns a host-generated `observation_id` alongside the
native AX tree and screenshot. Input must present the latest ID for that app,
chat and turn. Element actions require AX; pixel actions require a screenshot.
Coordinates are screenshot pixels; OCU handles display scaling.

The host uses an owned temporary configuration, rather than the user's OCU
configuration or inherited OCU environment variables. Global pointer fallbacks
are disabled. Virtual display placement, locked-use installation/unlocking,
clipboard helpers, arbitrary scripts, JS evaluation and unrestricted MCP calls
are not exposed. macOS may activate an app as part of native recovery; the
adapter does not promise every application supports background input. `type_text`
requires a focused editable field; prefer `set_value` for settable AX text fields
when keyboard focus is unavailable. The live fixture checks that semantic route,
not universal background keyboard delivery.

Native action read-back is disabled on supported platforms; the host explicitly
requests a fresh app observation after each input. Dispatch receipts are retained
if verification fails. A timeout or connection error invalidates every affected
observation and closes the connection. No input is automatically replayed.
App observations have a 60-second RPC budget for complex AX trees; input retains
a 30-second budget. A longer observation deadline does not prove UI consistency
when the user and automation operate the same app concurrently.
Missing permissions, native errors, empty results and missing screenshots remain
errors; a successful dispatch is not proof of the requested business outcome.
Screenshots are attached as model-visible images after the tool result.

## Packaging and upgrades

`apps/desktop/package.json` pins the package exactly; `package-lock.json` records
its tarball URL and SHA-512. `scripts/stage-computer-runtime.mjs` downloads and
verifies that archive, checks package identity, then extracts only the target
runtime and upstream LICENSE/THIRD_PARTY_NOTICES. macOS uses the universal app;
Windows/Linux use their architecture-specific binary. Packaging works from a
Linux runner and downloads nothing on first use. The runtime lives outside ASAR.

Dependabot proposes daily updates to `open-computer-use`; upgrades are reviewed,
not automatically merged or released. For each upgrade, update the package,
lockfile, host expected version, fixture version and native smoke together.
Review native tool schemas, errors, app identity and third-party notices. Run
`make check`, stage all four release targets, verify the macOS signature, and run
the live fixture before releasing. Do not replace this dependency with a floating
`latest` download or unrelated global installation.

## Verification

- Host tests cover chat/turn/app isolation, stale observations, self-target
  rejection, native error handling and post-action verification without replay.
- Stdio fixture tests cover MCP framing, version/protocol rejection, disconnects
  and timeout poisoning. They do not prove native input delivery.
- Kernel tests cover approval gates, schema boundaries and model-visible images.
- `scripts/smoke-computer.cjs` checks the real native MCP handshake and nine-tool
  schema on each release OS. It does not take screenshots or perform input and
  needs no macOS permission grants.
- `scripts/smoke-computer-live.cjs` is an opt-in macOS test against a temporary
  AppKit fixture. It requires grants to **Open Computer Use.app**, then tests a
  real screenshot, semantic input/click, Chinese/emoji preservation via semantic `set_value` and a local
  submit receipt. It does not operate existing user applications.
- Windows/Linux desktop delivery and platform-specific application behavior need
  separate graphical-session acceptance; contract checks do not establish it.

Upstream: https://github.com/iFurySt/open-computer-use
