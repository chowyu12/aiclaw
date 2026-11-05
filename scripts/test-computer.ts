import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { spawn } from "node:child_process";
import { ComputerController } from "../apps/desktop/src/main/computer.ts";
import { ComputerMcpRuntime, computerExecutable, computerNamespace, type ComputerToolResult } from "../apps/desktop/src/main/computer-runtime.ts";
import { runtimeTarget, verifyIntegrity } from "./stage-computer-runtime.mjs";
const specs = JSON.parse(readFileSync(new URL("../tools/claw-agent/internal/agent/computer-tools.json", import.meta.url), "utf8"));
const app = process.platform === "darwin" ? "com.example.fixture" : "123";
const image = "iVBORw0KGgo=";
function result(text = "fixture window", pixels = false): ComputerToolResult {
  return { content: [{ type: "text", text }, ...(pixels ? [{ type: "image", data: image, mimeType: "image/png" }] : [])] };
}
function harness(options: { postFails?: boolean; inputFails?: boolean; noImage?: boolean; missingField?: boolean; startFails?: boolean } = {}) {
  const calls: { action: string; args: any; connection: number }[] = [];
  let created = 0, closed = 0;
  const controller = new ComputerController(async () => {
    const connection = ++created; let read = 0;
    if (options.startFails && created === 1) throw Error("service startup failed");
    return {
      tools: specs.map((spec: any) => {
        const properties = { ...spec.schema.properties }; delete properties.observation_id;
        if (options.missingField) delete properties.x;
        return { name: spec.action, inputSchema: { properties, required: spec.schema.required?.filter((s: string) => s !== "observation_id") } };
      }),
      async call(action: string, args: any) {
        calls.push({ action, args, connection });
        if (action === "get_app_state") {
          if (++read > 1 && options.postFails) throw Error("capture timeout");
          return result("fixture tree [1] input [2] Submit", !options.noImage && args.include_screenshot !== false);
        }
        if (options.inputFails && action !== "list_apps") return { ...result("Accessibility permission pending for Open Computer Use.app"), isError: true };
        return result(action === "list_apps" ? "Fixture — com.example.fixture" : "input dispatched");
      },
      async close() { closed++; },
    };
  });
  const call = (action: string, args: Record<string, unknown> = {}, sessionId = "chat", turnId = "turn") => controller.perform({ sessionId, turnId, action, arguments: args });
  const observe = async (session = "chat", turn = "turn", args = {}) => {
    const state = await call("get_app_state", { app, ...args }, session, turn);
    return state.text.match(/observation_id: ([\w-]+)/)![1]!;
  };
  return { controller, calls, call, observe, counts: () => ({ created, closed }) };
}

test("service is lazy and discovery uses an isolated MCP connection", async () => {
  const h = harness(); assert.equal(h.counts().created, 0);
  await h.call("list_apps"); await h.call("list_apps", {}, "other");
  assert.deepEqual(h.calls.map(c => c.connection), [1, 2]);
  assert.deepEqual(h.calls[0]!.args, {});
  await h.controller.close(); assert.equal(h.counts().closed, 2);
});

test("arbitrary tools, old Cua routing and extra fields are rejected before dispatch", async () => {
  const h = harness();
  for (const action of ["run_script", "get_window_state", "list_windows", "launch_app", "query", "clipboard_read"]) await assert.rejects(h.call(action), /Unsupported computer operation/);
  for (const key of ["session", "pid", "window_id", "element_token", "delivery_mode", "window_placement", "screenshot_out_file"]) await assert.rejects(h.call("get_app_state", { app, [key]: "override" }), /Unsupported computer argument/);
  assert.equal(h.counts().created, 0);
});

test("self application, helper and Electron cannot be observed or controlled", async () => {
  const h = harness();
  for (const target of ["AIClaw", "com.github.chowyu12.aiclaw", "com.ifuryst.opencomputeruse", "Electron", "/Applications/AIClaw.app", "C:\\apps\\AIClaw.exe", String(process.pid)]) await assert.rejects(h.call("get_app_state", { app: target }), /Refusing/);
  assert.equal(h.counts().created, 0);
});

test("each input requires the current turn, chat, app and observation", async () => {
  const h = harness();
  await assert.rejects(h.call("click", { app, observation_id: "forged", x: 1, y: 2 }), /Observe this app/);
  const id = await h.observe();
  for (const [session, turn, target] of [["other", "turn", app], ["chat", "turn2", app], ["chat", "turn", process.platform === "darwin" ? "com.other.app" : "124"]]) {
    await assert.rejects(h.call("click", { app: target, observation_id: id, x: 1, y: 2 }, session, turn), /Observe this app/);
  }
  assert.equal(h.calls.filter(c => c.action === "click").length, 0);
  await h.controller.close();
});

test("Unicode input preserves receipt and fresh observation, and invalidates the old ID", async () => {
  const h = harness(); const id = await h.observe(); const text = '北京天气 🌦️\n"你好"';
  const output = await h.call("type_text", { app, observation_id: id, text });
  assert.equal(h.calls[1]!.args.text, text); assert.equal(h.calls[1]!.args.observation_id, undefined);
  assert.equal(output.imageBase64, image); assert.match(output.text, /input dispatched/); assert.match(output.text, /Post-action app observation/);
  await assert.rejects(h.call("click", { app, observation_id: id, element_index: "2" }), /latest observation_id/);
  await h.controller.close();
});

test("tree-only observations allow semantic actions and refuse pixel input", async () => {
  const h = harness(); const id = await h.observe("chat", "turn", { include_screenshot: false });
  await assert.rejects(h.call("click", { app, observation_id: id, x: 1, y: 2 }), /fresh screenshot/);
  await h.call("click", { app, observation_id: id, element_index: "2" }); await h.controller.close();
});

test("missing screenshots and omitted AX do not authorize guessed elements", async () => {
  const h = harness({ noImage: true }); await assert.rejects(h.observe(), /Grant Accessibility and Screen Recording to Open Computer Use/); await h.controller.close();
  const other = harness(); const id = await other.observe("chat", "turn", { include_ax: false });
  await assert.rejects(other.call("click", { app, observation_id: id, element_index: "1" }), /accessibility observation/); await other.controller.close();
});

test("input errors preserve native permissions diagnostics and never replay input", async () => {
  const h = harness({ inputFails: true }); const id = await h.observe();
  await assert.rejects(h.call("click", { app, observation_id: id, element_index: "2" }), /Accessibility permission pending.*Outcome may be unknown/);
  await assert.rejects(h.call("click", { app, observation_id: id, element_index: "2" }), /Observe this app/);
  assert.equal(h.calls.filter(c => c.action === "click").length, 1); assert.equal(h.counts().closed, 1);
});

test("post-action failure preserves dispatch receipt without fabricating success", async () => {
  const h = harness({ postFails: true }); const id = await h.observe();
  const output = await h.call("click", { app, observation_id: id, element_index: "2" });
  assert.match(output.text, /input dispatched/); assert.match(output.text, /verification failed.*capture timeout/); assert.match(output.text, /Do not automatically replay/);
  assert.equal(h.calls.filter(c => c.action === "click").length, 1); assert.equal(h.counts().closed, 1);
});

test("late turn completion cannot close a newer turn and archived chats release state", async () => {
  const h = harness(); await h.observe(); await h.observe("chat", "turn2");
  assert.equal(h.counts().closed, 1); await h.controller.finishTurn("chat", "turn"); assert.equal(h.counts().closed, 1);
  await h.controller.finishTurn("chat", "turn2"); assert.equal(h.counts().closed, 2);
  await h.observe("chat", "turn3"); await h.controller.releaseSession("chat"); assert.equal(h.counts().closed, 3);
});

test("turn completion rejects queued input before it can reach the service", async () => {
  const h = harness(); const id = await h.observe();
  const pending = h.call("click", { app, observation_id: id, element_index: "2" });
  const finished = h.controller.finishTurn("chat", "turn");
  await assert.rejects(pending, /turn has ended/); await finished;
  assert.equal(h.calls.filter(call => call.action === "click").length, 0);
  await assert.rejects(h.observe(), /turn has ended/);
});

test("service startup failure is retryable only on an explicit later observation", async () => {
  const h = harness({ startFails: true }); await assert.rejects(h.observe(), /startup failed/); await h.observe(); assert.equal(h.counts().created, 2); await h.controller.close();
});

test("runtime capabilities reject unsupported fields rather than silently dropping them", async () => {
  const h = harness({ missingField: true }); const id = await h.observe();
  await assert.rejects(h.call("click", { app, observation_id: id, x: 1, y: 2 }), /does not support click.x/); await h.controller.close();
});

function peer(mode: string, timeout = 1000, prepare?: (args: Record<string, unknown>) => Promise<void>) {
  return new ComputerMcpRuntime(spawn(process.execPath, [new URL("./fixtures/computer-mcp.cjs", import.meta.url).pathname, mode], { stdio: ["pipe", "pipe", "pipe"] }), undefined, timeout, prepare);
}

test("MCP handshake and PNG results travel through real stdio framing", async () => {
  const runtime = peer("ok"); await runtime.initialize(); const result = await runtime.call("list_apps", {});
  assert.equal(result.content[0]!.text, "fixture"); await runtime.close();
});

test("wrong protocol/version and malformed responses fail instead of dispatching", async () => {
  for (const mode of ["version", "malformed", "exit"]) { const runtime = peer(mode); await assert.rejects(runtime.initialize()); await runtime.close(); }
});

test("permission onboarding blocks observation until explicitly granted, then probes only once", async () => {
  let probes = 0;
  const blocked = peer("ok", 1000, async () => { probes++; throw Error("permission setup window is open"); });
  await blocked.initialize(); await assert.rejects(blocked.call("get_app_state", {}), /permission setup/);
  assert.equal(probes, 1); await blocked.close();
  const ready = peer("ok", 1000, async () => { probes++; });
  await ready.initialize(); await ready.call("get_app_state", {}); await ready.call("get_app_state", {});
  assert.equal(probes, 2); await ready.close();
});

test("tree-only permission probes do not suppress a later screenshot permission check", async () => {
  const observed: unknown[] = [];
  const runtime = peer("ok", 1000, async args => { observed.push(args.include_screenshot); });
  await runtime.initialize(); await runtime.call("get_app_state", { include_screenshot: false });
  await runtime.call("get_app_state", {}); await runtime.call("get_app_state", {});
  assert.deepEqual(observed, [false, undefined]); await runtime.close();
});

test("a timed-out input poisons the connection and cannot be replayed", async () => {
  const runtime = peer("timeout", 1000); await runtime.initialize();
  await assert.rejects(runtime.call("click", {}), /outcome is unknown/);
  await assert.rejects(runtime.call("click", {}), /outcome is unknown/); await runtime.close();
});

test("a slow observation has a separate budget while input still times out", async () => {
  const runtime = peer("slow-observation", 1000); await runtime.initialize();
  const result = await runtime.call("get_app_state", {});
  assert.equal(result.content[0]!.text, "slow observation");
  await assert.rejects(runtime.call("click", {}), /outcome is unknown/);
  await runtime.close();
});

test("cross-platform packaging paths and artifact integrity are pinned", () => {
  assert.match(runtimeTarget("darwin", "arm64"), /Open Computer Use.app$/); assert.match(runtimeTarget("win32", "x64"), /windows.*amd64$/);
  assert.match(computerExecutable("root", "linux", "x64"), /linux.*amd64.*open-computer-use$/);
  assert.throws(() => runtimeTarget("aix", "x64"));
  const bytes = Buffer.from("signed upstream archive"), integrity = `sha512-${createHash("sha512").update(bytes).digest("base64")}`;
  verifyIntegrity(bytes, integrity); assert.throws(() => verifyIntegrity(Buffer.from("changed"), integrity), /integrity mismatch/);
  assert.throws(() => verifyIntegrity(bytes, ""), /SHA-512/);
});

test("development and packaged app services cannot replace each other's helper", () => {
  const development = "/workspace/node_modules/open-computer-use/dist/Open Computer Use.app/Contents/MacOS/OpenComputerUse";
  const packaged = "/Applications/AIClaw.app/Contents/Resources/app/node_modules/open-computer-use/dist/Open Computer Use.app/Contents/MacOS/OpenComputerUse";
  assert.equal(computerNamespace(development), computerNamespace(development));
  assert.notEqual(computerNamespace(development), computerNamespace(packaged));
  assert.notEqual(computerNamespace(packaged), computerNamespace(`/release${packaged}`));
});
