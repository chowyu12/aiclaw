# Native desktop execution

AIClaw owns the model loop, approvals and transcript. The Electron host embeds
Cua Driver **0.38.0** for application/window discovery, accessibility snapshots,
Unicode input and native window-scoped actions. It replaces the previous
AppleScript/PowerShell input implementation. No separately installed Cua app,
daemon, Python environment or user MCP configuration is required.

## Call boundary

1. The computer-use plugin enables a fixed subset of native tools. Discover the
   requested app and its windows, then observe the exact `pid` + `window_id`.
2. A snapshot returns a tree, element tokens and, by default, a screenshot.
   Prefer element tokens; pixel coordinates refer to that window's screenshot.
   The driver handles Retina/display conversion.
3. Input follows the current chat approval policy. The host independently
   resolves window ownership, blocks AIClaw/Electron approval windows and requires
   a matching observation in the same chat. An element token cannot be redirected
   to another window by supplying a different PID.
4. The host forwards the action to the native driver in an isolated chat session.
   Background delivery is the default. Unsupported background routes return an
   error; there is no automatic foreground fallback. The model can explicitly
   request foreground delivery for an action under the same approval policy.
5. A successful dispatch returns its original receipt plus a new window
   observation. A receipt is not proof that the intended UI change happened.
   If observation fails after dispatch, preserve that fact and request another
   observation; do not replay the action (especially Return/Send).
6. The kernel attaches the screenshot as a model-visible image following the tool
   result. Host RPC errors remain errors, including permission failures. Empty
   results and unintentionally missing screenshots are rejected.

The exposed tools are discovery, launch, window observation, click/double-click/
right-click, text, key/chord, scroll and drag. Arbitrary driver scripts, desktop
input, clipboard, browser profile attachment and file-output options are not
forwarded. Browser automation continues to use AIClaw's existing browser tools.

A single ordered host queue prevents native operations from interleaving. Driver
sessions and remembered observations are isolated by AIClaw chat ID. Archiving or
deleting a chat ends its driver session; stopping the kernel shuts down the
native runtime. Native loading is lazy and can be retried after a load failure.

## Packaging and updates

`apps/desktop/package.json` pins the SDK; `package-lock.json` pins the SDK,
bindings and all target native artifacts with integrity hashes.
`scripts/stage-computer-driver.mjs` copies the runtime JS dependencies and fetches
only the target native package, verifies its lockfile SHA-512 and package identity,
then includes it in the Electron bundle. This also works when the Linux release
runner cross-packages macOS and Windows. Nothing is downloaded on first use.
The driver and binding notices in `docs/third-party` ship with the app.

Dependabot checks the published `@trycua/cua-driver` npm package daily, scoped
by `.github/dependabot.yml`, and proposes an update PR. This starts after that
configuration reaches the default branch. It does not automatically merge or
publish a release. Published SDK packages are the upgrade input; unreleased
upstream commits are not installed on user machines.

For each upgrade:

1. Review upstream release notes and breaking changes; update the SDK's exact
   version and lockfile together, including every target native package and its
   bindings. Verify third-party license/source notices against that version.
2. Update the exposed schemas and host argument allowlist together. Keep window
   identity, snapshot isolation and error/unknown-outcome semantics intact.
3. Run `make check` and stage all four release targets to verify native package
   versions, integrity and runtime files.
4. Run the opt-in macOS live fixture and a real Electron app regression covering
   Chinese input, sending, screenshot read-back and unsupported background input.
   Make platform-specific limits explicit; these checks do not establish a
   Windows/Linux desktop acceptance result.
5. Update the pinned version references in the native smoke and documentation,
   then ship the reviewed upgrade in a normal AIClaw release.

When upgrading the SDK, review the window-scoped subset in
`tools/claw-agent/internal/agent/computer-tools.json` and the host allowlist
together. `scripts/smoke-computer.cjs` loads the real native SDK inside Electron
and checks every exposed field against the native runtime's current contract;
it does not observe or manipulate user applications.

## Verification and limits

- Host tests cover session/window isolation, self-input prevention, Unicode
  argument preservation, stale-token refusal, error propagation and post-action
  observation failure without retry. These use a fixture driver, so they do not
  establish successful input delivery into real applications.
- Kernel tests cover real host-error request/response handling, approval gates,
  invalid/empty results and model-visible post-action screenshots.
- Native smoke verifies the Electron/native ABI and tool schemas. It makes no
  desktop calls and needs no Accessibility or Screen Recording grants.
- `scripts/smoke-computer-live.cjs` is an opt-in macOS acceptance test. It compiles
  and launches a temporary AppKit fixture, then uses the real controller and
  native SDK to discover the window, capture it, insert Chinese/emoji text,
  AX-click Submit and verify the fixture's received value. It touches no existing
  user application and is separate from the permission-free CI smoke.
- Real desktop acceptance requires a logged-in graphical session and the target
  host application's OS permissions. On macOS these are Accessibility and Screen
  Recording for **AIClaw**. Developer Electron builds have their own permission
  identity. Native app, Electron/web-view and custom-canvas delivery must be
  checked separately; a unit test cannot substitute for those checks.
- A valid accessibility tree may be partial. Preserve truncation/degradation
  metadata. A successful native call, unchanged screenshot or text field value
  alone must not be described as a verified end-to-end task result.

Upstream: https://github.com/trycua/cua/tree/cua-driver-rs-v0.38.0/libs/cua-driver
