/** Opt-in macOS live acceptance: a temporary native window plus the shipped overlay.
 * Moves only the owned fixture, reads only its AX/window capture, never user apps. */
const { app, BrowserWindow, ipcMain, screen } = require("electron");
const { execFile, spawn } = require("node:child_process");
const { promisify } = require("node:util");
const { mkdtemp, mkdir, writeFile, readFile, rm } = require("node:fs/promises");
const { tmpdir } = require("node:os");
const { join } = require("node:path");
const { pathToFileURL } = require("node:url");
const { createHash } = require("node:crypto");
const assert = require("node:assert/strict");
const run = promisify(execFile), pause = ms => new Promise(resolve => setTimeout(resolve, ms));
const desktop = join(__dirname, "../apps/desktop");
const { IPC } = require(join(desktop, "dist/shared/ipc.cjs"));
app.on("window-all-closed", () => {});
app.setName("aiclaw-overlay-acceptance"); app.setPath("userData", join(tmpdir(), "aiclaw-overlay-acceptance"));
let controller, overlay, child, temporary, overlayWindow, unsubscribe;
const watchdog = setTimeout(() => { console.error("Overlay acceptance timed out"); void finish(1); }, 150_000);
async function wait(read, condition, description) {
  for (let attempt = 0; attempt < 80; attempt++) { try { const value = await read(); if (condition(value)) return value; } catch {} await pause(100); }
  throw Error(`Timed out: ${description}`);
}
app.whenReady().then(async () => {
  if (process.platform !== "darwin") throw Error("Native overlay acceptance currently requires macOS; pure geometry tests cover other coordinate boundaries.");
  const { ComputerController } = await import(pathToFileURL(join(desktop, "dist/main/computer.js")));
  const { ComputerOverlay, overlayPlacement } = await import(pathToFileURL(join(desktop, "dist/main/computer-overlay.js")));
  const { createComputerRuntime } = await import(pathToFileURL(join(desktop, "dist/main/computer-runtime.js")));
  const runtimeRecords = [], nativeActions = [];
  let nativeHoldStarted;
  controller = new ComputerController(async () => {
    const runtime = await createComputerRuntime();
    const record = { runtime, closed: false, captureId: null, nextFailures: [] }; runtimeRecords.push(record);
    const close = runtime.close.bind(runtime);
    runtime.close = async () => { record.closed = true; return close(); };
    const nativeCall = runtime.call.bind(runtime);
    runtime.call = async (name, args) => {
      nativeActions.push(name);
      const result = nativeCall(name, args);
      if (name === "press_key" && args.duration === 1) nativeHoldStarted?.();
      return result;
    };
    if (runtime.startCapture && runtime.nextCapture) {
      const start = runtime.startCapture.bind(runtime), next = runtime.nextCapture.bind(runtime);
      runtime.startCapture = async args => {
        const result = await start(args);
        record.captureId = JSON.parse(result.content.find(item => item.type === "text").text.split("\n")[0]).capture_id;
        return result;
      };
      runtime.nextCapture = async args => {
        try { return await next(args); }
        catch (error) { record.nextFailures.push(String(error)); throw error; }
      };
    }
    return runtime;
  });
  const target = "com.aiclaw.overlayfixture", sessionId = "overlay-acceptance", turnId = "turn";
  const call = (action, args = {}) => controller.perform({ sessionId, turnId, action, arguments: args });
  const permissions = JSON.parse((await call("permission_status")).text);
  if (!permissions.accessibilityTrusted || !permissions.screenCaptureGranted) throw Error("Grant Accessibility and Screen Recording to AIClaw Computer Use before live acceptance; no fixture input was dispatched.");
  temporary = await mkdtemp(join(tmpdir(), "aiclaw-overlay-"));
  const command = join(temporary, "command.json"), ready = join(temporary, "ready.json");
  const contents = join(temporary, "Overlay Fixture.app/Contents"); await mkdir(join(contents, "MacOS"), { recursive: true });
  await writeFile(join(contents, "Info.plist"), `<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>${target}</string><key>CFBundleExecutable</key><string>Fixture</string><key>CFBundleName</key><string>Overlay Fixture</string><key>CFBundlePackageType</key><string>APPL</string><key>LSUIElement</key><true/></dict></plist>`);
  const binary = join(contents, "MacOS/Fixture");
  await run("xcrun", ["swiftc", join(__dirname, "fixtures/computer-overlay-macos.swift"), "-o", binary], { timeout: 30_000 });
  let sequence = 0;
  async function move(frame) {
    await writeFile(command, JSON.stringify({ ...frame, sequence: ++sequence }));
    return wait(async () => JSON.parse(await readFile(ready, "utf8")), value => value.sequence === sequence && ["x", "y", "width", "height"].every(key => Math.round(value.frame[key]) === Math.round(frame[key])), "fixture window move acknowledgement");
  }
  const area = screen.getPrimaryDisplay().workArea;
  await writeFile(command, JSON.stringify({ x: area.x + 60, y: area.y + 4, width: Math.min(700, area.width - 100), height: Math.min(420, area.height - 100), sequence }));
  child = spawn(binary, [command, ready], { stdio: "ignore" });
  const fixture = await wait(async () => JSON.parse(await readFile(ready, "utf8")), value => value.windowId > 0, "fixture readiness");
  const baseline = await wait(() => call("get_app_state", { app: target, window_id: fixture.windowId, include_ax: false, include_screenshot: true }), value => !!value.imageBase64, "fixture capture");
  const hash = value => createHash("sha256").update(Buffer.from(value, "base64")).digest("hex");
  const foregroundBefore = JSON.parse((await call("frontmost_window")).text).app;
  assert.equal(foregroundBefore, target);
  const initialState = controller.getStatus();
  assert.equal(initialState.active?.app, target, "fixture observation must retain the target");
  assert.equal(initialState.active?.displayName, "Overlay Fixture", "native metadata must expose a readable application name separately from identity");
  assert.equal(initialState.active?.windowId, fixture.windowId);
  assert.ok(await controller.windowBounds(initialState.active), "native query must confirm the target PID, window ID and bounds before showing its control surface");
  const controls = [], queries = [];
  overlay = new ComputerOverlay({ platform: process.platform, displays: () => screen.getAllDisplays(),
    readBounds: async activity => { const bounds = await controller.windowBounds(activity); queries.push(bounds); return bounds; },
    control: async action => { controls.push(action); return controller.setControl(action); },
    createWindow: () => {
      overlayWindow = new BrowserWindow({ width: 370, height: 58, show: false, frame: false, alwaysOnTop: true, resizable: false, skipTaskbar: true, backgroundColor: "#252a32",
        webPreferences: { preload: join(desktop, "dist/preload/computer-overlay.cjs"), contextIsolation: true, nodeIntegration: false, sandbox: true } });
      overlayWindow.webContents.on("preload-error", (_event, _path, error) => console.error("Overlay preload failed:", error.message));
      overlayWindow.webContents.on("did-fail-load", (_event, code, description) => console.error("Overlay resource load failed:", code, description));
      return overlayWindow;
    },
    loadWindow: window => window.loadFile(join(desktop, "dist/renderer/computer-overlay.html")),
    sendStatus: (window, value) => window.webContents.send(IPC.onComputerOverlayStatus, value),
  });
  ipcMain.handle(IPC.computerOverlayStatus, event => overlay.statusFor(event));
  ipcMain.handle(IPC.computerOverlayControl, (event, action) => overlay.controlFrom(event, action));
  unsubscribe = controller.subscribe(state => overlay.update(state));
  overlay.update(controller.getStatus());
  await wait(async () => overlayWindow && !overlayWindow.isDestroyed() && overlayWindow.isVisible() && await overlayWindow.webContents.executeJavaScript("document.getElementById('status').textContent"), value => value && value !== "AIClaw", "visible isolated overlay");
  assert.equal(overlayWindow.isAlwaysOnTop(), true);
  assert.equal(await overlayWindow.webContents.executeJavaScript("document.getElementById('target').textContent"), "Overlay Fixture", "the indicator must show the application name rather than its bundle identifier");
  assert.equal(JSON.parse((await call("frontmost_window")).text).app, foregroundBefore, "showing the indicator must not activate AIClaw");
  assert.equal(await overlayWindow.webContents.executeJavaScript("window.aiclaw === undefined && Object.keys(window.computerControl).sort().join(',') === 'control,onStatus,status'"), true);
  const withOverlay = await call("get_app_state", { app: target, window_id: fixture.windowId, include_ax: false, include_screenshot: true });
  assert.equal(hash(withOverlay.imageBase64), hash(baseline.imageBase64), "the separate control surface must be excluded from target-window capture, even when it overlaps its top edge");
  const positions = [];
  for (const display of screen.getAllDisplays()) {
    const work = display.workArea;
    const moved = await move({ x: work.x + 50, y: work.y + 100, width: Math.min(700, work.width - 100), height: Math.min(420, work.height - 150) });
    const expected = overlayPlacement(moved.frame, screen.getAllDisplays());
    try { await wait(async () => overlayWindow.getBounds(), value => ["x", "y", "width", "height"].every(key => value[key] === expected[key]), "indicator follows real target bounds"); }
    catch (error) { console.error("Overlay tracking diagnostic:", JSON.stringify({ expected, actual: overlayWindow.getBounds(), visible: overlayWindow.isVisible(), target: moved.frame, status: controller.getStatus(), recent_bounds: queries.slice(-4) })); throw error; }
    positions.push({ displayId: display.id, scaleFactor: display.scaleFactor, target: moved.frame, overlay: overlayWindow.getBounds() });
  }
  await overlayWindow.webContents.executeJavaScript("document.getElementById('pause').click()");
  await wait(() => controller.getStatus(), value => value.mode === "paused", "pause from overlay");
  const pausedBounds = overlayWindow.getBounds(), pausedQueries = queries.length;
  await move({ x: area.x + 100, y: area.y + 120, width: 620, height: 380 }); await pause(600);
  assert.deepEqual(overlayWindow.getBounds(), pausedBounds); assert.equal(queries.length, pausedQueries); assert.equal(overlayWindow.isVisible(), true);
  await pause(5500); // Expire any earlier transient user activation before the negative case.
  await overlayWindow.webContents.executeJavaScript("document.getElementById('resume').click()"); await pause(100);
  assert.equal(controller.getStatus().mode, "paused", "synthetic clicks must not resume control");
  await overlayWindow.webContents.executeJavaScript("document.getElementById('resume').click()", true);
  await wait(() => controller.getStatus(), value => value.mode === "ready", "explicit resume");
  assert.equal(overlayWindow.isVisible(), false, "resume alone must not revive a stale target");
  await call("get_app_state", { app: target, window_id: fixture.windowId, include_ax: false, include_screenshot: true });
  await wait(() => overlayWindow.isVisible(), value => value, "fresh observation restores indicator");
  if (process.env.AICLAW_COMPUTER_EVIDENCE_DIR) {
    const dir = process.env.AICLAW_COMPUTER_EVIDENCE_DIR; await mkdir(dir, { recursive: true });
    await writeFile(join(dir, "computer-overlay.png"), (await overlayWindow.webContents.capturePage()).toPNG());
    await writeFile(join(dir, "overlay-target.png"), Buffer.from(baseline.imageBase64, "base64"));
  }
  const hold = Math.min(60_000, Math.max(0, Number(process.env.AICLAW_OVERLAY_HOLD_MS) || 0));
  if (hold) { console.log(JSON.stringify({ ready_for_visual_check: true, fixture: target, overlay: overlayWindow.getBounds() })); await pause(hold); }
  // Stop the genuine connection-owned native stream, then let its pending next
  // request fail. No fake result or controller pause is injected by this test.
  const watching = await call("watch_start", { app: target, window_id: fixture.windowId, interval_ms: 500 });
  const watch = JSON.parse(watching.text);
  assert.equal(watch.sampling, "native");
  const capture = runtimeRecords.findLast(record => record.captureId === watch.metadata.capture_id);
  assert.ok(capture?.captureId && capture.runtime.stopCapture, "the real watch must own a recorded native capture connection");
  const oldInput = await call("get_app_state", { app: target, window_id: fixture.windowId, include_ax: false, include_screenshot: true });
  const oldObservation = oldInput.text.match(/observation_id: ([\w-]+)/)[1];
  const entered = new Promise(resolve => { nativeHoldStarted = resolve; });
  const holding = Promise.allSettled([call("press_key", { app: target, key: "End", duration: 1, observation_id: oldObservation, verify_after: false })]);
  await Promise.race([entered, pause(5000).then(() => { throw Error("Native fixture key hold never started"); })]);
  nativeHoldStarted = undefined;
  const nativeClosesBefore = nativeActions.filter(action => action === "stop_app").length;
  const queuedClose = Promise.allSettled([controller.perform({ sessionId: "overlay-other-chat", turnId: "queued-loss", action: "stop_app", arguments: { app: target, verify_after: false } })]);
  await Promise.allSettled([capture.runtime.stopCapture({ capture_id: capture.captureId })]);
  await wait(() => controller.getStatus(), value => value.mode === "paused" && value.watching === 0, "native capture loss pauses global input");
  assert.ok(capture.nextFailures.some(message => /capture|cancel/i.test(message)), "native nextCapture must really fail on the stopped stream");
  assert.equal(capture.closed, true, "failed native capture connection must be closed");
  await wait(() => runtimeRecords.every(record => record.closed), value => value, "capture loss closes every input and observation connection");
  assert.equal((await holding)[0].status, "rejected");
  const closed = (await queuedClose)[0];
  assert.equal(closed.status, "rejected"); assert.match(String(closed.reason), /paused|stopped|ended/i);
  assert.equal(nativeActions.filter(action => action === "stop_app").length, nativeClosesBefore, "queued close must not reach the native backend after capture loss");
  await assert.rejects(call("press_key", { app: target, key: "End", observation_id: oldObservation, verify_after: false }), /paused|stopped/i);
  await wait(() => overlayWindow.webContents.executeJavaScript("document.body.dataset.mode"), value => value === "paused", "indicator reflects capture-loss pause");
  assert.equal(overlayWindow.isVisible(), true);
  if (process.env.AICLAW_COMPUTER_EVIDENCE_DIR) await writeFile(join(process.env.AICLAW_COMPUTER_EVIDENCE_DIR, "computer-overlay-capture-paused.png"), (await overlayWindow.webContents.capturePage()).toPNG());
  await overlayWindow.webContents.executeJavaScript("document.getElementById('resume').click()", true);
  await wait(() => controller.getStatus(), value => value.mode === "ready", "explicit recovery after native capture loss");
  assert.equal(overlayWindow.isVisible(), false);
  await assert.rejects(call("press_key", { app: target, key: "End", observation_id: oldObservation, verify_after: false }), /Observe|observation/i);
  await call("get_app_state", { app: target, window_id: fixture.windowId, include_ax: false, include_screenshot: true });
  await wait(() => overlayWindow.isVisible(), value => value, "fresh observation recovers the target after capture loss");
  assert.equal(await overlayWindow.webContents.executeJavaScript("document.getElementById('target').textContent"), "Overlay Fixture");
  await overlayWindow.webContents.executeJavaScript("document.getElementById('stop').click()");
  await wait(() => controller.getStatus(), value => value.mode === "stopped", "stop from overlay"); assert.equal(overlayWindow.isVisible(), false);
  const receipt = { passed: true, readable_application_name: true, native_capture_loss_pauses_input: true, capture_failure_closes_watch_connection: true, capture_failure_closes_all_connections: true, capture_failure_blocks_queued_input: true, capture_failure_invalidates_old_observation: true, capture_failure_requires_explicit_recovery_and_new_observation: true, always_on_top: true, showing_does_not_steal_focus: true, isolated_preload: true, native_bounds_tracking: positions, target_capture_excludes_overlay: true, pause_stops_tracking: true, synthetic_resume_refused: true, explicit_resume_requires_fresh_observation: true, stop_hides_overlay: true };
  if (process.env.AICLAW_COMPUTER_EVIDENCE_DIR) await writeFile(join(process.env.AICLAW_COMPUTER_EVIDENCE_DIR, "overlay-live-summary.json"), JSON.stringify(receipt, null, 2) + "\n");
  console.log(JSON.stringify(receipt));
}).then(() => finish(0)).catch(error => { console.error(error); void finish(1); });
async function finish(code) {
  clearTimeout(watchdog); unsubscribe?.(); overlay?.dispose();
  try { await controller?.close(); }
  finally { child?.kill("SIGTERM"); if (temporary) await rm(temporary, { recursive: true, force: true }); app.exit(code); }
}
