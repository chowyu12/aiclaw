import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { ComputerController } from "../apps/desktop/src/main/computer.ts";
import { ComputerWatches } from "../apps/desktop/src/main/computer-watch.ts";
import type { ComputerToolResult } from "../apps/desktop/src/main/computer-runtime.ts";
const specs = JSON.parse(readFileSync(new URL("../tools/claw-agent/internal/agent/computer-tools.json", import.meta.url), "utf8"));
const app = process.platform === "darwin" ? "com.example.fixture" : "123";
const flush = async () => { for (let i = 0; i < 12; i++) await Promise.resolve(); };
function harness() {
  let value = "before", image = "aW1hZ2U=", pid = 123, birth = 1, handle = 1, closed = 0, created = 0, failure = false;
  const calls: { connection: number; action: string; args: any }[] = [];
  const read = (): ComputerToolResult => failure ? { isError: true, content: [{ type: "text", text: "window closed" }] } : { content: [{ type: "text", text: JSON.stringify({ app, pid, process_start_time: birth, window_id: 456, truncated: false }) + `\n[${handle++}] AXWindow fixture\n  [${handle++}] AXTextField ${value}` }, { type: "image", mimeType: "image/png", data: image }] };
  const runtime = async () => {
    const connection = ++created;
    return {
      tools: specs.map((spec: any) => ({ name: spec.action, inputSchema: { properties: spec.schema.properties, required: [] } })),
      async call(action: string, args: any) {
        calls.push({ connection, action, args });
        if (action === "query") return { content: [{ type: "text", text: JSON.stringify({ matches: [{ index: 1 }], window_id: 456 }) }] };
        if (action === "set_value") { value = args.value; return { content: [{ type: "text", text: "value set" }] }; }
        return read();
      },
      async close() { closed++; },
    };
  };
  const watches = new ComputerWatches(runtime);
  const start = async (session = "chat") => {
    const result = await watches.start(session, "turn", { app, interval_ms: 500 }, () => true);
    return { result, data: JSON.parse(result.text) };
  };
  return { runtime, watches, start, calls, read, counts: () => ({ created, closed }), change: (next: string) => { value = next; }, changeImage: () => { image = "bmV3LWltYWdl"; }, changeProcess: () => { pid++; }, changeBirth: () => { birth++; }, fail: () => { failure = true; } };
}

test("watch coalesces handle-only changes and returns reconstructable AX/image deltas", async t => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const h = harness();
  try {
    const { result, data } = await h.start();
    assert.equal(data.reset, true); assert.equal(data.action_authority, false);
    assert.equal(data.sampling, "polling"); assert.ok(result.imageBase64);
    assert.ok(data.ax.lines.every((line: string) => !/\[\d+\]/.test(line)));
    t.mock.timers.tick(500); await flush();
    assert.equal(JSON.parse((await h.watches.next("chat", "turn", { watch_id: data.watch_id, after_sequence: 1, wait_ms: 0 })).text).changed, false);
    h.change("after"); t.mock.timers.tick(500); await flush();
    const next = await h.watches.next("chat", "turn", { watch_id: data.watch_id, after_sequence: 1, wait_ms: 0 });
    const delta = JSON.parse(next.text);
    assert.equal(delta.reset, false); assert.equal(delta.sequence, 2); assert.equal(delta.base_sequence, 1);
    assert.equal(next.imageBase64, undefined);
    const reconstructed = [...data.ax.lines]; reconstructed.splice(delta.ax.start, delta.ax.delete_count, ...delta.ax.lines);
    assert.deepEqual(reconstructed, ["AXWindow fixture", "  AXTextField after"]);
    h.changeImage(); t.mock.timers.tick(500); await flush();
    const pixels = await h.watches.next("chat", "turn", { watch_id: data.watch_id, after_sequence: 2, wait_ms: 0 });
    assert.equal(pixels.imageBase64, "bmV3LWltYWdl"); assert.equal(JSON.parse(pixels.text).image_changed, true);
    h.change("latest"); t.mock.timers.tick(500); await flush();
    const skipped = JSON.parse((await h.watches.next("chat", "turn", { watch_id: data.watch_id, after_sequence: 1, wait_ms: 0 })).text);
    assert.equal(skipped.reset, true); assert.equal(skipped.dropped_updates, 2);
    assert.deepEqual(skipped.ax.lines, ["AXWindow fixture", "  AXTextField latest"]);
  } finally { await h.watches.close(); }
});

test("watch avoids false deltas when handle digit counts shift a native text cutoff", async t => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  let handle = 9;
  const watches = new ComputerWatches(async () => ({
    tools: [], async close() {},
    async call(_action, args) {
      const tree = `[${handle++}] AXTextField ${"unchanged".repeat(80)}`;
      return {content:[
        {type:"text",text:JSON.stringify({app,pid:123,window_id:456})+"\n"+(args.text_limit === "max" ? tree : tree.slice(0,500))},
        {type:"image",mimeType:"image/png",data:"aW1hZ2U="},
      ]};
    },
  }));
  try {
    const baseline = JSON.parse((await watches.start("chat","turn",{app,interval_ms:500},()=>true)).text);
    t.mock.timers.tick(500); await flush();
    const next = JSON.parse((await watches.next("chat","turn",{watch_id:baseline.watch_id,after_sequence:1,wait_ms:0})).text);
    assert.equal(next.changed,false);
    assert.equal(baseline.ax.lines[0],`AXTextField ${"unchanged".repeat(80)}`);
  } finally {await watches.close();}
});

test("watch scope, cancellation, idle expiry and capture failure fail closed", async t => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const h = harness();
  try {
    const { data } = await h.start();
    await assert.rejects(h.watches.next("other", "turn", { watch_id: data.watch_id }), /another chat/);
    await assert.rejects(h.watches.next("chat", "turn", { watch_id: data.watch_id, after_sequence: 999 }), /Expected integer/);
    const waiting = assert.rejects(h.watches.next("chat", "turn", { watch_id: data.watch_id, after_sequence: 1 }), /stopped/);
    await h.watches.stop("chat", "turn", { watch_id: data.watch_id }); await waiting;
    const restarted = await h.start();
    h.fail(); t.mock.timers.tick(500); await flush();
    await assert.rejects(h.watches.next("chat", "turn", { watch_id: restarted.data.watch_id, after_sequence: 1 }), /window closed/);
    await h.watches.stop("chat", "turn");
  } finally { await h.watches.close(); }
  const idle = harness(); const { data } = await idle.start();
  t.mock.timers.tick(60_001); await flush();
  assert.equal(idle.counts().closed, 1);
  await assert.rejects(idle.watches.next("chat", "turn", { watch_id: data.watch_id }), /absent/);
  await idle.watches.close();
});

test("capture uses a separate connection and never invalidates action observations", async t => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const h = harness(), controller = new ComputerController(h.runtime);
  const call = (action: string, args = {}) => controller.perform({ sessionId: "chat", turnId: "turn", action, arguments: args });
  try {
    const started = JSON.parse((await call("watch_start", { app, interval_ms: 500 })).text);
    const query = await call("query", { app, text: "input" });
    const observation_id = query.text.match(/observation_id: ([\w-]+)/)![1];
    t.mock.timers.tick(500); await flush();
    await call("set_value", { app, observation_id, element_index: "1", value: "after", verify_after: false });
    assert.equal(h.calls.find(item => item.action === "query")?.connection, 2);
    assert.equal(h.calls.filter(item => item.action === "get_app_state")[1]?.connection, 1);
    const waiting = assert.rejects(call("watch_next", { watch_id: started.watch_id, after_sequence: 1 }), /stopped/);
    await controller.finishTurn("chat", "turn"); await waiting;
    assert.equal(h.counts().closed, 2);
  } finally { await controller.close(); }
});

test("subscription startup cannot survive a finished turn or changing process", async t => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const h = harness();
  await assert.rejects(h.watches.start("chat", "turn", { app }, () => false), /turn ended/);
  assert.equal(h.counts().closed, 1);
  const { data } = await h.start(); h.changeProcess();
  t.mock.timers.tick(500); await flush();
  await assert.rejects(h.watches.next("chat", "turn", { watch_id: data.watch_id, after_sequence: 1 }), /process or window changed/);
  await h.watches.stop("chat", "turn");
  const reused = await h.start(); h.changeBirth();
  t.mock.timers.tick(500); await flush();
  await assert.rejects(h.watches.next("chat", "turn", { watch_id: reused.data.watch_id, after_sequence: 1 }), /process or window changed/);
  await h.watches.close();
});

test("native capture uses its own stream and preserves AX/image delta semantics", async t => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const calls: string[] = [];
  const capture = (sequence: number): ComputerToolResult => ({ content: [
    { type: "text", text: JSON.stringify({ app, pid: 123, window_id: 456, capture_id: "stream", capture_sequence: sequence, capture_backend: "screencapturekit" }) + `\n[${sequence}] AXTextField ${sequence === 1 ? "before" : "after"}` },
    { type: "image", mimeType: "image/png", data: "aW1hZ2U=" },
  ] });
  const watches = new ComputerWatches(async () => ({
    tools: [], nativeCapture: true,
    async call() { throw Error("Native subscriptions must not poll screenshots"); },
    async startCapture() { calls.push("start"); return capture(1); },
    async nextCapture(args) { calls.push("next"); assert.equal(args.after_sequence, 1); return capture(5); },
    async stopCapture() { return { content: [] }; },
    async close() { calls.push("close"); },
  }));
  try {
    const baseline = JSON.parse((await watches.start("chat", "turn", { app, interval_ms: 500 }, () => true)).text);
    assert.equal(baseline.sampling, "native");
    t.mock.timers.tick(0); await flush();
    const update = await watches.next("chat", "turn", { watch_id: baseline.watch_id, after_sequence: 1, wait_ms: 0 });
    const data = JSON.parse(update.text);
    assert.equal(data.sequence, 5); assert.equal(data.sampling, "native"); assert.equal(data.dropped_updates, 3);
    assert.equal(data.image_changed, false); assert.equal(update.imageBase64, undefined);
    assert.deepEqual(data.ax.lines, ["AXTextField after"]);
  } finally { await watches.close(); }
  assert.deepEqual(calls, ["start", "next", "close"]);
});
