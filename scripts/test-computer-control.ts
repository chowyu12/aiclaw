import test from "node:test";
import assert from "node:assert/strict";
import { ComputerController } from "../apps/desktop/src/main/computer.ts";
import type { ComputerRuntime, ComputerToolResult } from "../apps/desktop/src/main/computer-runtime.ts";

function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
const app = process.platform === "darwin" ? "com.example.fixture" : "123";
const text = (value: unknown): ComputerToolResult => ({ content: [{ type: "text", text: typeof value === "string" ? value : JSON.stringify(value) }] });
function runtime(call: ComputerRuntime["call"], close: ComputerRuntime["close"] = async () => {}): ComputerRuntime {
  return { tools: ["list_apps", "permission_status", "query", "click", "get_app_state"].map(name => ({ name, inputSchema: { properties: { app: {}, text: {}, element_index: {}, include_ax: {}, include_screenshot: {} } } })), call, close };
}
const request = (action: string, args = {}, turnId = "turn") => ({ sessionId: "chat", turnId, action, arguments: args });

test("pause cancels an active request immediately and rejects queued work without replay", async () => {
  const entered = deferred<void>(), pending = deferred<ComputerToolResult>();
  let calls = 0, closed = 0;
  const states: string[] = [];
  const controller = new ComputerController(async () => runtime(async () => {
    if (++calls === 1) { entered.resolve(); return pending.promise; }
    return text("apps");
  }, async () => { closed++; pending.reject(Error("connection closed")); }));
  controller.subscribe(state => states.push(state.mode));
  const active = controller.perform(request("list_apps"));
  await entered.promise;
  const queued = controller.perform(request("list_apps"));
  const outcomes = Promise.allSettled([active, queued]);
  await controller.setControl("pause");
  assert.equal(closed, 1);
  assert.ok((await outcomes).every(result => result.status === "rejected"));
  assert.equal(calls, 1);
  await assert.rejects(controller.perform(request("list_apps")), /paused or stopped/);
  await controller.setControl("resume");
  await controller.perform(request("list_apps"));
  assert.equal(calls, 2);
  assert.ok(states.includes("paused") && states.includes("ready"));
  await controller.close();
});

test("a runtime resolving after pause and resume is closed before any native call", async () => {
  const creating = deferred<ComputerRuntime>(), entered = deferred<void>();
  let calls = 0, closed = 0;
  const controller = new ComputerController(async () => { entered.resolve(); return creating.promise; });
  const operation = controller.perform(request("list_apps"));
  const outcome = Promise.allSettled([operation]);
  await entered.promise;
  await controller.setControl("pause");
  await controller.setControl("resume");
  creating.resolve(runtime(async () => { calls++; return text("apps"); }, async () => { closed++; }));
  assert.equal((await outcome)[0]!.status, "rejected");
  assert.equal(calls, 0); assert.equal(closed, 1);
  await controller.close();
});

test("resume requires a fresh observation; stop also closes the old turn", async () => {
  let clicks = 0;
  const controller = new ComputerController(async () => runtime(async action => {
    if (action === "query") return text({ matches: [{ index: 1 }], window_id: 456 });
    if (action === "click") clicks++;
    return text("ok");
  }));
  const observed = await controller.perform(request("query", { app, text: "input" }));
  const observation_id = observed.text.match(/observation_id: ([\w-]+)/)![1];
  await controller.setControl("pause"); await controller.setControl("resume");
  await assert.rejects(controller.perform(request("click", { app, element_index: "1", observation_id, verify_after: false })), /Observe this app/);
  assert.equal(clicks, 0);
  assert.deepEqual(controller.affectedSessions(), ["chat"]);
  await controller.setControl("stop"); await controller.setControl("resume");
  await assert.rejects(controller.perform(request("list_apps")), /turn has ended/);
  await controller.perform(request("list_apps", {}, "new-turn"));
  await controller.close();
});

test("finishing a turn disconnects in-flight work without waiting behind its queue", async () => {
  const entered = deferred<void>(), pending = deferred<ComputerToolResult>();
  let closed = false;
  const controller = new ComputerController(async () => runtime(async () => { entered.resolve(); return pending.promise; }, async () => { closed = true; pending.reject(Error("closed")); }));
  const active = controller.perform(request("list_apps"));
  const result = Promise.allSettled([active]);
  await entered.promise;
  await controller.finishTurn("chat", "turn");
  assert.equal(closed, true); assert.equal((await result)[0]!.status, "rejected");
  await controller.close();
});

test("diagnostics read permission state without screenshot, input or permission request", async () => {
  const calls: string[] = []; let closed = 0;
  const controller = new ComputerController(async () => runtime(async action => { calls.push(action); return text({ accessibilityTrusted: true, screenCaptureGranted: false }); }, async () => { closed++; }));
  const status = await controller.diagnostics();
  assert.equal(status.available, true); assert.equal(status.accessibility, true); assert.equal(status.screenCapture, false);
  assert.deepEqual(calls, ["permission_status"]); assert.equal(closed, 1);
  await controller.close();
});

test("unknown control actions cannot enable desktop input", async () => {
  const controller = new ComputerController();
  await controller.setControl("stop");
  await assert.rejects(controller.setControl("enable" as any), /Unknown desktop control action/);
  assert.equal(controller.getStatus().mode, "stopped");
  await controller.close();
});

test("observed target persists through the turn and bounds checks use a separate read connection", async () => {
  const calls: { action: string; args: Record<string, unknown> }[] = [];
  const controller = new ComputerController(async () => runtime(async (action, args) => {
    calls.push({ action, args });
    return text({ app, app_name: "Fixture App", pid: 7, window_id: 456, window_frame: { x: -100, y: 20, width: 800, height: 600 }, matches: [{ index: 1 }] });
  }));
  await controller.perform(request("query", { app, text: "input" }));
  const active = controller.getStatus().active!;
  assert.equal(active.action, "waiting"); assert.equal(active.windowId, 456); assert.equal(active.displayName, "Fixture App");
  assert.deepEqual(await controller.windowBounds(active), { x: -100, y: 20, width: 800, height: 600 });
  assert.equal(calls.at(-1)!.args.max_nodes, 1);
  await controller.finishTurn("chat", "turn");
  assert.equal(controller.getStatus().active, null);
  assert.equal(await controller.windowBounds(active), null);
  await controller.close();
});


test("legacy service diagnostics explain recovery without exposing internal error details", async () => {
  for (const code of ["COMPUTER_LEGACY_BUSY", "COMPUTER_LEGACY_UNVERIFIED"]) {
    const controller = new ComputerController(async () => { throw Object.assign(Error("internal path and process details"), { code }); });
    const status = await controller.diagnostics();
    assert.equal(status.available, false); assert.match(status.message, /旧版/);
    assert.ok(!status.message.includes("internal path"));
    await controller.close();
  }
});

test("losing a live observation pauses desktop input until an explicit resume", async t => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  let closed = 0;
  const controller = new ComputerController(async () => ({
    tools: [], nativeCapture: true,
    async call() { throw Error("No input may be dispatched"); },
    async startCapture() { return { content: [
      { type: "text", text: JSON.stringify({ app, pid: 7, window_id: 456, capture_id: "stream", capture_sequence: 1 }) + "\n[1] AXTextField fixture" },
      { type: "image", mimeType: "image/png", data: "aW1hZ2U=" },
    ] }; },
    async nextCapture() { throw Error("Screen sharing was stopped"); },
    async stopCapture() { return { content: [] }; },
    async close() { closed++; },
  }));
  await controller.perform(request("watch_start", { app }));
  t.mock.timers.tick(0);
  for (let i = 0; i < 20; i++) await Promise.resolve();
  assert.equal(controller.getStatus().mode, "paused");
  assert.equal(controller.getStatus().watching, 0); assert.equal(closed, 1);
  await assert.rejects(controller.perform(request("list_apps")), /paused or stopped/);
  await controller.close();
});

test("an action invalidates another chat's older observation of the same app", async () => {
  let clicks = 0;
  const controller = new ComputerController(async () => runtime(async action => {
    if (action === "query") return text({ matches: [{ index: 1 }], window_id: 456 });
    if (action === "click") clicks++;
    return text("ok");
  }));
  const query = async (sessionId: string) => (await controller.perform({ ...request("query", { app, text: "input" }), sessionId })).text.match(/observation_id: ([\w-]+)/)![1];
  const first = await query("first"), old = await query("second");
  await controller.perform({ ...request("click", { app, element_index: "1", observation_id: first, verify_after: false }), sessionId: "first" });
  await assert.rejects(controller.perform({ ...request("click", { app, element_index: "1", observation_id: old, verify_after: false }), sessionId: "second" }), /Observe this app/);
  assert.equal(clicks, 1);
  const fresh = await query("second");
  await controller.perform({ ...request("click", { app, element_index: "1", observation_id: fresh, verify_after: false }), sessionId: "second" });
  assert.equal(clicks, 2);
  await controller.close();
});
