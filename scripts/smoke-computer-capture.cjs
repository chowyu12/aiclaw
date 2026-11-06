/** Opt-in real SCStream/AXObserver acceptance. Only the temporary fixture is
 * observed; fixture changes occur inside that app, never through user input.
 */
const { app } = require("electron");
const assert = require("node:assert/strict");
const { spawn, execFile } = require("node:child_process");
const { promisify } = require("node:util");
const { mkdtemp, mkdir, writeFile, rm } = require("node:fs/promises");
const { tmpdir } = require("node:os");
const path = require("node:path");
const { pathToFileURL } = require("node:url");
const { createHash } = require("node:crypto");
const { setTimeout: pause } = require("node:timers/promises");
const run = promisify(execFile);
app.setName("aiclaw-capture-acceptance");
app.setPath("userData", path.join(tmpdir(), "aiclaw-capture-acceptance"));
let runtime, child, temporary;
const protocolPeers = [];
let phase = "setup";
const watchdog = setTimeout(() => { console.error("Native stream acceptance timed out"); void finish(1); }, 120_000);
const state = result => {
  assert.ok(!result.isError, JSON.stringify(result));
  const text = result.content.find(item => item.type === "text")?.text;
  assert.ok(text, "Native capture response must include metadata");
  return { metadata: JSON.parse(text.split("\n")[0]), text, image: result.content.find(item => item.type === "image")?.data };
};
app.whenReady().then(async () => {
  if (process.platform !== "darwin") throw Error("Native stream acceptance requires macOS");
  const { ComputerNativeRuntime } = await import(pathToFileURL(path.join(__dirname, "../apps/desktop/dist/main/computer-runtime.js")));
  runtime = await ComputerNativeRuntime.create();
  const permissions = state(await runtime.call("permission_status", {})).metadata;
  assert.ok(permissions.accessibilityTrusted && permissions.screenCaptureGranted, "The helper needs previously granted AX and Screen Recording; no permission request or fixture input was sent");
  assert.equal(runtime.nativeCapture, true, "The actual helper must advertise native capture");
  temporary = await mkdtemp(path.join(tmpdir(), "aiclaw-stream-fixture-"));
  const contents = path.join(temporary, "Capture Fixture.app", "Contents"), target = "com.aiclaw.capturefixture";
  await mkdir(path.join(contents, "MacOS"), {recursive: true});
  await writeFile(path.join(contents, "Info.plist"), `<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>${target}</string><key>CFBundleExecutable</key><string>Fixture</string><key>CFBundleName</key><string>AIClaw Capture Fixture</string><key>CFBundlePackageType</key><string>APPL</string><key>LSUIElement</key><true/></dict></plist>`);
  const binary = path.join(contents, "MacOS", "Fixture"), commands = path.join(temporary, "command.json");
  await run("xcrun", ["swiftc", path.join(__dirname, "fixtures/computer-capture-macos.swift"), "-o", binary], {timeout: 60_000});
  const badgeProbe = process.env.AICLAW_CAPTURE_BADGE_PROBE === "1";
  child = spawn(binary, [commands, ...(badgeProbe ? ["--titled"] : [])], {stdio: "ignore"});
  let initial;
  for (let attempt = 0; attempt < 40 && !initial; attempt++) {
    try { initial = state(await runtime.call("get_app_state", {app: target, include_ax: true})); } catch { await pause(100); }
  }
  assert.ok(initial?.metadata.window_id, "Temporary fixture must expose its own window");
  await pause(300);
  const start = () => runtime.startCapture({app: target, window_id: initial.metadata.window_id, interval_ms: 500});
  let first = state(await start());
  assert.equal(first.metadata.capture_backend, "screencapturekit-stream");
  assert.ok(first.image && first.metadata.capture_id && first.metadata.capture_sequence >= 1);
  assert.ok(first.metadata.ax_observer_registrations > 0);
  const captureID = first.metadata.capture_id;
  const next = (after, wait = 3000) => runtime.nextCapture({capture_id: captureID, after_sequence: after, wait_ms: wait});
  // AppKit/ScreenCaptureKit may deliver startup appearance frames. Require the
  // real stream to settle, then verify a full wait produces no new sequence.
  let unchanged, settled = false, quiet = 0;
  for (let attempt = 0; attempt < 6; attempt++) {
    unchanged = state(await next(first.metadata.capture_sequence, 800));
    if (unchanged.metadata.capture_sequence === first.metadata.capture_sequence) {
      quiet += 1;
      if (quiet >= 2) { settled = true; break; }
    } else quiet = 0;
    first = unchanged;
  }
  assert.ok(settled, "A static window must not generate synthetic timer updates");
  if (badgeProbe) {
    const before = "/tmp/aiclaw-capture-badge-before-stream.png", after = "/tmp/aiclaw-capture-badge-after-stream.png";
    await writeFile(before, Buffer.from(initial.image, "base64")); await writeFile(after, Buffer.from(first.image, "base64"));
    await runtime.stopCapture({capture_id: captureID});
    await pause(500);
    const afterStop = "/tmp/aiclaw-capture-badge-after-stop.png";
    const stoppedState = state(await runtime.call("get_app_state", {app: target, window_id: initial.metadata.window_id, include_ax: true}));
    await writeFile(afterStop, Buffer.from(stoppedState.image, "base64"));
    console.log(JSON.stringify({passed: true, experiment: "single-shot-before-and-native-stream-after", before, after, after_stop: afterStop, before_metadata: initial.metadata, after_metadata: first.metadata, after_stop_metadata: stoppedState.metadata, indicator_implementation: "No indicator drawn by fixture or Capture.swift; system attribution still requires visual comparison"}));
    return;
  }
  await writeFile(commands, JSON.stringify({command: "semantic", value: "semantic-event-only"}));
  const semantic = state(await next(unchanged.metadata.capture_sequence));
  assert.ok(semantic.metadata.capture_sequence > unchanged.metadata.capture_sequence);
  assert.ok(semantic.metadata.ax_event_count > first.metadata.ax_event_count);
  assert.match(semantic.text, /semantic-event-only/);
  const imageHash = image => createHash("sha256").update(image).digest("hex");
  if (imageHash(semantic.image) !== imageHash(first.image)) {
    await writeFile("/tmp/aiclaw-capture-ax-before.png", Buffer.from(first.image, "base64"));
    await writeFile("/tmp/aiclaw-capture-ax-after.png", Buffer.from(semantic.image, "base64"));
    console.error(JSON.stringify({before: first.metadata, after: semantic.metadata, before_hash: imageHash(first.image), after_hash: imageHash(semantic.image)}));
  }
  assert.equal(imageHash(semantic.image), imageHash(first.image), "AX-only changes must be observable without a new screen frame");
  await writeFile(commands, JSON.stringify({command: "visual", value: "native-stream-pixels"}));
  let visual = semantic;
  for (let attempt = 0; attempt < 4 && visual.image === semantic.image; attempt++) visual = state(await next(visual.metadata.capture_sequence));
  assert.notEqual(imageHash(visual.image), imageHash(semantic.image), "SCStream must deliver changed window pixels");
  assert.match(visual.text, /native-stream-pixels/);
  const pending = assert.rejects(next(visual.metadata.capture_sequence, 10_000), /stop|cancel|closed/i);
  await pause(100);
  const stopAt = Date.now();
  await runtime.stopCapture({capture_id: captureID}); await pending;
  assert.ok(Date.now() - stopAt < 2000, "Stop must wake the frame waiter without waiting behind it");
  await assert.rejects(next(visual.metadata.capture_sequence, 0), /absent|stop|unknown|capture/i);
  // Real independent connections share the native global limit but cannot use
  // each other's capture IDs. Long-polling one connection must leave ordinary
  // operations and stop responsive, including response framing on that socket.
  phase = "four-live-stream-protocol";
  const captures = [];
  for (let index = 0; index < 5; index++) protocolPeers.push(await ComputerNativeRuntime.create());
  for (const peer of protocolPeers.slice(0, 4)) captures.push(state(await peer.startCapture({app: target, window_id: initial.metadata.window_id, interval_ms: 500})));
  assert.equal(new Set(captures.map(capture => capture.metadata.capture_id)).size, 4);
  await assert.rejects(protocolPeers[4].startCapture({app: target, window_id: initial.metadata.window_id, interval_ms: 500}), /limit/i);
  await assert.rejects(protocolPeers[1].nextCapture({capture_id: captures[0].metadata.capture_id, after_sequence: 0, wait_ms: 0}), /belong/i);
  const peer = protocolPeers[0], id = captures[0].metadata.capture_id;
  let latest = captures[0], still = false;
  for (let attempt = 0; attempt < 6 && !still; attempt++) {
    const update = state(await peer.nextCapture({capture_id: id, after_sequence: latest.metadata.capture_sequence, wait_ms: 800}));
    still = update.metadata.capture_sequence === latest.metadata.capture_sequence;
    latest = update;
  }
  assert.ok(still, "The protocol fixture must settle before testing a pending update");
  let waitSettled = false;
  const frameWait = peer.nextCapture({capture_id: id, after_sequence: latest.metadata.capture_sequence, wait_ms: 10_000})
    .then(value => { waitSettled = true; return {value}; }, error => { waitSettled = true; return {error}; });
  await pause(100); assert.equal(waitSettled, false, "The frame wait must actually be pending");
  const ordinaryAt = Date.now();
  const ordinary = state(await peer.call("permission_status", {}));
  const ordinaryElapsedMs = Date.now() - ordinaryAt;
  assert.equal(ordinary.metadata.accessibilityTrusted, true);
  assert.ok(ordinaryElapsedMs < 1500, "A pending capture must not occupy the global ordinary-operation lane");
  await assert.rejects(peer.nextCapture({capture_id: id, after_sequence: latest.metadata.capture_sequence, wait_ms: 0}), /pending|already/i);
  const protocolStopAt = Date.now();
  await peer.stopCapture({capture_id: id});
  const interrupted = await frameWait;
  assert.match(String(interrupted.error), /stop|cancel|closed/i);
  assert.ok(Date.now() - protocolStopAt < 2000);
  const recovered = state(await protocolPeers[4].startCapture({app: target, window_id: initial.metadata.window_id, interval_ms: 500}));
  await assert.rejects(protocolPeers[4].nextCapture({capture_id: recovered.metadata.capture_id, after_sequence: Number.MAX_SAFE_INTEGER, wait_ms: 0}), /outside|limits/i);
  const stillValid = state(await protocolPeers[4].nextCapture({capture_id: recovered.metadata.capture_id, after_sequence: 0, wait_ms: 0}));
  assert.equal(stillValid.metadata.capture_id, recovered.metadata.capture_id, "bad cursor arguments must not stop a valid stream");
  phase = "pending-target-loss";
  const active = [1, 2, 3, 4].map(index => ({peer: protocolPeers[index], capture: index === 4 ? recovered : captures[index]}));
  const lossWaits = [];
  for (const entry of active) {
    const current = state(await entry.peer.nextCapture({capture_id: entry.capture.metadata.capture_id, after_sequence: 0, wait_ms: 0}));
    lossWaits.push(assert.rejects(entry.peer.nextCapture({capture_id: current.metadata.capture_id, after_sequence: current.metadata.capture_sequence, wait_ms: 10_000}), /window|exited|capture|closed|stop/i));
  }
  await assert.rejects(runtime.startCapture({app: target, window_id: initial.metadata.window_id, interval_ms: 500}), /limit/i);
  await writeFile(commands, JSON.stringify({command: "replace"}));
  await Promise.all(lossWaits);
  phase = "replacement-window-discovery";
  let replacement;
  for (let attempt = 0; ; attempt++) {
    try { replacement = state(await runtime.call("get_app_state", {app: target, include_ax: true})); break; }
    catch (error) { if (attempt >= 30) throw error; await pause(100); }
  }
  assert.notEqual(replacement.metadata.window_id, initial.metadata.window_id);
  await pause(300);
  phase = "pending-loss-slot-reuse";
  const replacements = [];
  // Every failed capture must surrender its global slot without explicit stop
  // or disconnect. A fifth independent connection can then use all four slots.
  for (const peer of [runtime, ...protocolPeers.slice(0, 3)]) {
    let current;
    for (let attempt = 0; ; attempt++) {
      try { current = state(await peer.startCapture({app: target, window_id: replacement.metadata.window_id, interval_ms: 500})); break; }
      catch (error) { if (attempt >= 20 || !/limit|existing capture/i.test(String(error))) throw error; await pause(50); }
    }
    replacements.push({peer, id: current.metadata.capture_id});
  }
  phase = "idle-target-loss";
  // A stopped stream with no pending next must also be reaped on new start.
  await writeFile(commands, JSON.stringify({command: "replace", nonce: 2}));
  let secondReplacement;
  for (let attempt = 0; ; attempt++) {
    try {
      secondReplacement = state(await runtime.call("get_app_state", {app: target, include_ax: true}));
      if (secondReplacement.metadata.window_id === replacement.metadata.window_id) throw Error("Fixture replacement is not yet visible");
      break;
    } catch (error) { if (attempt >= 30) throw error; await pause(100); }
  }
  await pause(300);
  phase = "idle-loss-slot-reuse";
  const idleReplacements = [];
  for (const peer of [protocolPeers[3], ...protocolPeers.slice(0, 3)]) {
    let current;
    for (let attempt = 0; ; attempt++) {
      try { current = state(await peer.startCapture({app: target, window_id: secondReplacement.metadata.window_id, interval_ms: 500})); break; }
      catch (error) { if (attempt >= 40 || !/limit|existing capture/i.test(String(error))) throw error; await pause(50); }
    }
    idleReplacements.push({peer, id: current.metadata.capture_id});
  }
  for (const entry of idleReplacements) await entry.peer.stopCapture({capture_id: entry.id});
  for (const peer of protocolPeers) await peer.close();
  protocolPeers.length = 0;
  console.log(JSON.stringify({passed: true, native_stream: true, ax_event_received: true, ax_only_update_without_new_pixels: true, static_frame_deduplicated: true, stopped_wait_without_input_queue_starvation: true, pinned_window_close_refused_retarget: true, global_four_stream_limit: true, cross_connection_capture_id_refused: true, duplicate_wait_refused: true, stopped_slot_reused: true, invalid_cursor_preserved_stream: true, target_loss_released_all_four_slots_without_stop: true, idle_target_loss_reaped_all_four_slots_on_new_start: true, concurrent_jsonl_responses_valid: true, ordinary_request_while_capture_wait_ms: ordinaryElapsedMs, target, initial_window_id: initial.metadata.window_id}));
}).then(() => finish(0)).catch(error => { console.error(JSON.stringify({failed_phase:phase})); console.error(error); return finish(1); });

async function finish(code) {
  clearTimeout(watchdog);
  try { await runtime?.close(); } catch {}
  for (const peer of protocolPeers) try { await peer.close(); } catch {}
  if (child && child.exitCode === null) child.kill();
  if (temporary) await rm(temporary, {recursive: true, force: true});
  app.exit(code);
}
