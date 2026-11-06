/** Opt-in acceptance: two temporary AppKit apps, no user-app input.
 * Verifies semantic background actions and refusals without foreground fallback.
 */
const { app, clipboard, screen } = require("electron");
const { spawn, execFile } = require("node:child_process");
const { promisify } = require("node:util");
const { mkdtemp, readFile, rm, mkdir, writeFile, copyFile } = require("node:fs/promises");
const { setTimeout: pause } = require("node:timers/promises");
const { tmpdir } = require("node:os");
const path = require("node:path");
const assert = require("node:assert/strict");
const { pathToFileURL } = require("node:url");
const run = promisify(execFile);
app.setName("aiclaw-background-acceptance");
app.setPath("userData", path.join(tmpdir(), "aiclaw-background-acceptance"));
let controller, temporary;
const children = [];
const watchdog = setTimeout(() => { void finish(1); }, 90_000);
app.whenReady().then(async () => {
  if (process.platform !== "darwin") throw Error("Background acceptance requires macOS");
  const { ComputerController } = await import(pathToFileURL(path.join(__dirname, "../apps/desktop/dist/main/computer.js")));
  controller = new ComputerController();
  const call = async (action, args = {}) => {
    try { return await controller.perform({ sessionId: "background-acceptance", turnId: "t1", action, arguments: args }); }
    catch (error) { throw Error(`${action}: ${String(error)}`); }
  };
  const { computerBundle } = await import(pathToFileURL(path.join(__dirname, "../apps/desktop/dist/main/computer-runtime.js")));
  const permissions = JSON.parse((await call("permission_status")).text);
  if (!permissions.accessibilityTrusted || !permissions.screenCaptureGranted) throw Error(`Grant Accessibility and Screen Recording to ${computerBundle()}; no fixture input was dispatched.`);
  temporary = await mkdtemp(path.join(tmpdir(), "aiclaw-background-"));
  const target = "com.aiclaw.backgroundfixture", sentinel = "com.aiclaw.backgroundsentinel";
  const build = async (id, source) => {
    const contents = path.join(temporary, `${id}.app`, "Contents");
    await mkdir(path.join(contents, "MacOS"), { recursive: true });
    await writeFile(path.join(contents, "Info.plist"), `<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>${id}</string><key>CFBundleExecutable</key><string>Fixture</string><key>CFBundleName</key><string>${id}</string><key>CFBundlePackageType</key><string>APPL</string><key>LSUIElement</key><true/></dict></plist>`);
    const binary = path.join(contents, "MacOS", "Fixture");
    if (source) await copyFile(source, binary);
    else await run("swiftc", [path.join(__dirname, "fixtures/computer-macos.swift"), "-o", binary], { timeout: 30_000 });
    children.push(spawn(binary, [path.join(temporary, `${id}.json`)], { stdio: "ignore" }));
    return binary;
  };
  const binary = await build(target);
  await build(sentinel, binary);
  for (const id of [target, sentinel]) {
    for (let attempt = 0; ; attempt++) {
      try { await call("get_app_state", { app: id, include_screenshot: false, include_ax: true }); break; }
      catch (error) { if (attempt >= 30) throw error; await pause(100); }
    }
  }
  await call("modify_app", { app: sentinel, modification: "activate", verify_after: false });
  const foreground = JSON.parse((await call("frontmost_window")).text).app;
  assert.equal(foreground, sentinel, "the other fixture must be frontmost");
  const clipboardSnapshot = async () => Promise.all((await clipboard.read()).map(async item => Promise.all(item.types.map(async type => {
    const value = await item.getType(type);
    return [type, typeof value.arrayBuffer === "function" ? Buffer.from(await value.arrayBuffer()).toString("base64") : JSON.stringify(value)];
  }))));
  const pointer = screen.getCursorScreenPoint(), savedClipboard = await clipboardSnapshot();
  const query = async (name, role) => {
    const state = await call("query", { app: target, text: name, role, exact: true });
    const data = JSON.parse(state.text.split("\n")[0]);
    assert.equal(data.matches.length, 1); assert.equal(data.truncated, false);
    return { app: target, element_index: String(data.matches[0].index), observation_id: state.text.match(/observation_id: ([\w-]+)/)[1], interaction_mode: "background", verify_after: false };
  };
  const value = "后台输入 🌦️";
  const filled = await call("set_value", { ...await query("Fixture input", "AXTextField"), value });
  assert.match(filled.text, /strict AX background mode/);
  await call("select_text", { ...await query("Fixture input", "AXTextField"), text: value, selection: "text" });
  await call("perform_secondary_action", { ...await query("Fixture stepper", "AXIncrementor"), action: "AXIncrement" });
  await call("click", await query("Submit fixture", "AXButton"));
  const receipt = JSON.parse(await readFile(path.join(temporary, `${target}.json`), "utf8"));
  assert.equal(receipt.value, value); assert.equal(receipt.stepper, 1);
  for (const [action, input] of [["press_key", { key: "super+a" }], ["paste", { text: "MUST NOT PASTE" }], ["click", { x: 1, y: 1 }]]) {
    const observed = await call("get_app_state", { app: target });
    await assert.rejects(call(action, { app: target, ...input, observation_id: observed.text.match(/observation_id: ([\w-]+)/)[1], interaction_mode: "background", verify_after: false }), /Background.*(not supported|requires|support unmodified or Shift)/);
  }
  const noProof = await query("Fixture input", "AXTextField");
  await assert.rejects(call("type_text", { app: target, observation_id: noProof.observation_id, interaction_mode: "background", verify_after: false, text: "MUST NOT TYPE" }), /Query observations authorize matched element actions only; take a screenshot/);
  assert.equal(JSON.parse((await call("frontmost_window")).text).app, sentinel);
  assert.deepEqual(screen.getCursorScreenPoint(), pointer);
  assert.deepEqual(await clipboardSnapshot(), savedClipboard);
  const beforeWatch = await query("Fixture input", "AXTextField");
  const initial = await call("watch_start", { app: target, interval_ms: 500 });
  let baseline = JSON.parse(initial.text);
  assert.equal(baseline.reset, true); assert.equal(baseline.action_authority, false); assert.ok(initial.imageBase64);
  await writeFile("/tmp/aiclaw-semantic-watch-start.png", Buffer.from(initial.imageBase64, "base64"));
  let sharingIndicatorTransitions = 0, stable = false;
  for (let attempt = 0; attempt < 5; attempt++) {
    const sample = await call("watch_next", { watch_id: baseline.watch_id, after_sequence: baseline.sequence, wait_ms: 900 });
    const update = JSON.parse(sample.text);
    if (!update.changed) { stable = true; break; }
    // SCStream makes macOS add its own sharing control after the first frame.
    // Validate that exact lifecycle change before testing the static window;
    // do not discard unrelated AX edits or accept an indefinitely moving image.
    console.error(JSON.stringify({stage: "capture_startup_sharing_indicator", baseline, update}));
    assert.equal(update.sampling, "native"); assert.equal(update.reset, false);
    assert.equal(update.image_changed, true); assert.equal(update.ax.delete_count, 0);
    assert.ok(update.ax.lines.every(line => /AXMenuButton Window sharing /.test(line)), "only the native sharing control may appear during startup");
    const lines = [...baseline.ax.lines]; lines.splice(update.ax.start, update.ax.delete_count, ...update.ax.lines);
    assert.ok(lines.some(line => /AXMenuButton Window sharing /.test(line)), "native sharing indicator must be observed, not inferred from image churn");
    assert.ok(sample.imageBase64);
    await writeFile(`/tmp/aiclaw-semantic-watch-sharing-${attempt}.png`, Buffer.from(sample.imageBase64, "base64"));
    baseline = { ...update, ax: {lines} }; sharingIndicatorTransitions++;
  }
  assert.equal(stable, true, "after the native sharing indicator settles, a static inactive window must produce no updates for 900ms");
  await call("set_value", { ...beforeWatch, value: "持续观察 🌦️" });
  const changed = await call("watch_next", { watch_id: baseline.watch_id, after_sequence: baseline.sequence, wait_ms: 3000 });
  const delta = JSON.parse(changed.text);
  assert.equal(delta.changed, true); assert.equal(delta.reset, false);
  const lines = [...baseline.ax.lines]; lines.splice(delta.ax.start, delta.ax.delete_count, ...delta.ax.lines);
  assert.ok(lines.some(line => line.includes("持续观察 🌦️")), "AX delta must reconstruct the changed value");
  assert.ok(lines.every(line => !/\[\d+\]/.test(line)), "subscription samples cannot expose actionable handles");
  assert.equal(delta.image_changed, true); assert.ok(changed.imageBase64);
  const waiting = assert.rejects(call("watch_next", { watch_id: baseline.watch_id, after_sequence: delta.sequence, wait_ms: 10_000 }), /stopped/);
  await pause(20);
  await call("watch_stop", { watch_id: baseline.watch_id }); await waiting;
  assert.equal(JSON.parse((await call("frontmost_window")).text).app, sentinel);
  const summary = { passed: true, foreground_unchanged: true, pointer_unchanged: true, clipboard_unchanged: true, semantic_actions: ["set_value", "select_text", "secondary_action", "click"], unsupported_refused: ["background_cmd_shortcut", "type_text_without_screenshot_proof", "paste", "coordinate_click"], watch: { initial_baseline: true, sharing_indicator_startup_transitions: sharingIndicatorTransitions, stable_wait_ms: 900, static_window_no_update: true, ax_delta_reconstructed: true, changed_image_returned: true, action_observation_survived_sampling: true, stop_cancelled_wait: true }, ...receipt };
  if (process.env.AICLAW_COMPUTER_EVIDENCE_DIR) await writeFile(path.join(process.env.AICLAW_COMPUTER_EVIDENCE_DIR, "background-live-summary.json"), JSON.stringify(summary, null, 2) + "\n");
  console.log(JSON.stringify(summary));
}).then(() => finish(0)).catch(async error => { console.error(error); await finish(1); });
async function finish(code) {
  clearTimeout(watchdog);
  try { await controller?.close(); }
  finally { for (const child of children) child.kill("SIGTERM"); if (temporary) await rm(temporary, { recursive: true, force: true }); app.exit(code); }
}
