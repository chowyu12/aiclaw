/** Opt-in real input cancellation; only an owned temporary AppKit key receiver
 * gets input. No existing application, clipboard, user data or send operation.
 */
const { app } = require("electron");
const assert = require("node:assert/strict");
const { spawn, execFile } = require("node:child_process");
const { promisify } = require("node:util");
const { mkdtemp, mkdir, writeFile, readFile, rm } = require("node:fs/promises");
const { tmpdir } = require("node:os");
const path = require("node:path");
const { pathToFileURL } = require("node:url");
const { setTimeout: pause } = require("node:timers/promises");
const run = promisify(execFile);
app.setName("aiclaw-key-release-acceptance");
app.setPath("userData", path.join(tmpdir(), "aiclaw-key-release-acceptance"));
let temporary, child, controller;
const watchdog = setTimeout(() => { console.error("Real key-release acceptance timed out"); void finish(1); }, 90_000);
const observationID = result => {
  const id = result.text.match(/observation_id: ([\w-]+)/)?.[1];
  assert.ok(id, "A real screenshot observation must authorize the input");
  return id;
};

app.whenReady().then(async () => {
  assert.equal(process.platform, "darwin");
  const { ComputerController } = await import(pathToFileURL(path.join(__dirname, "../apps/desktop/dist/main/computer.js")));
  const { ComputerNativeRuntime } = await import(pathToFileURL(path.join(__dirname, "../apps/desktop/dist/main/computer-runtime.js")));
  const permissionsRuntime = await ComputerNativeRuntime.create();
  try {
    const permissions = JSON.parse((await permissionsRuntime.call("permission_status", {})).content[0].text);
    assert.ok(permissions.accessibilityTrusted && permissions.screenCaptureGranted, "Previously granted helper permissions are required; no permission request or input was sent");
  } finally { await permissionsRuntime.close(); }
  temporary = await mkdtemp(path.join(tmpdir(), "aiclaw-key-release-"));
  const contents = path.join(temporary, "Key Release Fixture.app", "Contents"), target = "com.aiclaw.keyreleasefixture";
  await mkdir(path.join(contents, "MacOS"), {recursive: true});
  await writeFile(path.join(contents, "Info.plist"), `<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>${target}</string><key>CFBundleExecutable</key><string>Fixture</string><key>CFBundleName</key><string>Key Release Fixture</string><key>CFBundlePackageType</key><string>APPL</string><key>LSUIElement</key><true/></dict></plist>`);
  const executable = path.join(contents, "MacOS", "Fixture"), receipt = path.join(temporary, "events.json");
  await run("xcrun", ["swiftc", path.join(__dirname, "fixtures/computer-key-release-macos.swift"), "-o", executable], {timeout: 60_000});
  child = spawn(executable, [receipt], {stdio: "ignore"});
  const events = async () => {
    try { return JSON.parse(await readFile(receipt, "utf8")).events; }
    catch (error) { if (error.code === "ENOENT") return []; throw error; }
  };
  const until = async (predicate, timeout = 5000) => {
    const deadline = Date.now() + timeout;
    do { const value = await events(); if (predicate(value)) return value; await pause(20); } while (Date.now() < deadline);
    throw Error("The temporary fixture did not record the expected key event");
  };
  for (let attempt = 0; attempt < 50; attempt++) {
    try { if (JSON.parse(await readFile(receipt, "utf8")).ready) break; } catch {}
    await pause(50);
  }
  const cases = [];
  for (const mode of ["pause", "stop"]) {
    const calls = [];
    controller = new ComputerController(async () => {
      const runtime = await ComputerNativeRuntime.create(), call = runtime.call.bind(runtime);
      runtime.call = (action, args) => { calls.push(action); return call(action, args); };
      return runtime;
    });
    const request = (sessionId, action, args, turnId = `${mode}-turn`) => ({sessionId, turnId, action, arguments: args});
    const holdSession = `${mode}-hold`, queuedSession = `${mode}-queued`;
    // A normal quit is a lifecycle action with no observation dependency.
    // Cross-chat snapshot invalidation cannot make this a false cancellation
    // pass: if the queued action reaches native code, this owned fixture exits.
    let heldObservation;
    for (let attempt = 0; ; attempt++) {
      try { heldObservation = await controller.perform(request(holdSession, "get_app_state", {app: target, include_ax: true})); break; }
      catch (error) { if (attempt >= 30) throw error; await pause(100); }
    }
    const oldID = observationID(heldObservation), offset = (await events()).length;
    const active = controller.perform(request(holdSession, "press_key", {app: target, key: "a", duration: 10, observation_id: oldID, verify_after: false}));
    const queued = controller.perform(request(queuedSession, "stop_app", {app: target, verify_after: false}));
    const outcomes = Promise.allSettled([active, queued]);
    await until(value => value.slice(offset).some(event => event.kind === "down" && event.keyCode === 0));
    const stopAt = Date.now();
    await controller.setControl(mode);
    const afterRelease = await until(value => value.slice(offset).some(event => event.kind === "up" && event.keyCode === 0), 2000);
    const releaseElapsedMs = Date.now() - stopAt;
    assert.ok(releaseElapsedMs < 2000, "Pause/stop must release the held key before the ten-second duration ends");
    assert.ok((await outcomes).every(result => result.status === "rejected"));
    assert.equal(calls.filter(action => action === "press_key").length, 1);
    assert.equal(calls.filter(action => action === "stop_app").length, 0, "Queued lifecycle action must never reach the native service");
    await controller.setControl("resume");
    const freshTurn = `${mode}-resumed-turn`;
    await assert.rejects(controller.perform(request(holdSession, "press_key", {app: target, key: "b", observation_id: oldID, verify_after: false}, freshTurn)), /Observe this app/);
    assert.equal(calls.filter(action => action === "press_key").length, 1, "A stale observation after resume must not dispatch input");
    const fresh = await controller.perform(request(holdSession, "get_app_state", {app: target, include_ax: true}, freshTurn));
    const resumedOffset = afterRelease.length;
    await controller.perform(request(holdSession, "press_key", {app: target, key: "c", duration: 0.01, observation_id: observationID(fresh), verify_after: false}, freshTurn));
    const finished = await until(value => value.slice(resumedOffset).some(event => event.kind === "down" && event.keyCode === 8) && value.slice(resumedOffset).some(event => event.kind === "up" && event.keyCode === 8));
    cases.push({mode, requested_hold_ms: 10_000, release_elapsed_ms: releaseElapsedMs, queued_action: "stop_app", queued_native_calls: 0, stale_observation_refused: true, new_observation_input_verified: true, native_call_counts: Object.fromEntries([...new Set(calls)].map(action => [action, calls.filter(value => value === action).length])), events: finished.slice(offset)});
    await controller.close(); controller = undefined;
  }
  console.log(JSON.stringify({passed: true, fixture_only: true, user_app_input: false, clipboard_used: false, cases}));
}).then(() => finish(0)).catch(error => { console.error(error); return finish(1); });

async function finish(code) {
  clearTimeout(watchdog);
  try { await controller?.close(); } catch {}
  if (child && child.exitCode === null) child.kill();
  if (temporary) await rm(temporary, {recursive: true, force: true});
  app.exit(code);
}
