import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createServer, createConnection } from "node:net";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { createHash } from "node:crypto";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { ComputerController } from "../apps/desktop/src/main/computer.ts";
import { ComputerNativeRuntime, computerExecutable, computerNamespace, COMPUTER_PROTOCOL, computerSocket, type ComputerToolResult } from "../apps/desktop/src/main/computer-runtime.ts";
import { ComputerPortableRuntime } from "../apps/desktop/src/main/computer-portable-runtime.ts";
import { stageComputerRuntime } from "./stage-computer-runtime.mjs";
const specs = JSON.parse(readFileSync(new URL("../tools/claw-agent/internal/agent/computer-tools.json", import.meta.url), "utf8"));
const app = process.platform === "darwin" ? "com.example.fixture" : "123";
const image = "iVBORw0KGgo=";
function result(text = "fixture window", pixels = false): ComputerToolResult {
  return { content: [{ type: "text", text }, ...(pixels ? [{ type: "image", data: image, mimeType: "image/png" }] : [])] };
}
function harness(options: { postFails?: boolean; transientPostFailure?: boolean; inputFails?: boolean; noImage?: boolean; missingField?: boolean; startFails?: boolean; noQuery?: boolean; launchUnverified?: boolean; htmlPasteOnly?: boolean } = {}) {
  const calls: { action: string; args: any; connection: number }[] = [];
  let created = 0, closed = 0;
  const controller = new ComputerController(async () => {
    const connection = ++created; let read = 0;
    if (options.startFails && created === 1) throw Error("service startup failed");
    return {
      tools: specs.filter((spec: any) => !options.noQuery || spec.action !== "query").map((spec: any) => {
        const properties = { ...spec.schema.properties }; delete properties.observation_id;
        if (options.missingField) delete properties.x;
        if (options.htmlPasteOnly && spec.action === "paste") properties.format = { enum: ["text", "html"] };
        return { name: spec.action, inputSchema: { properties, required: spec.schema.required?.filter((s: string) => s !== "observation_id") } };
      }),
      async call(action: string, args: any) {
        calls.push({ action, args, connection });
        if (action === "query") return result(JSON.stringify({ matches: [{ index: 1000001, role: "AXTextField", title: "Fixture input" }], truncated: false, window_id: 456 }));
        if (action === "get_app_state") {
          if (++read > 1 && options.postFails) throw Error("capture timeout");
          if (read === 2 && options.transientPostFailure) return { ...result("Target app exposes no accessible windows"), isError: true };
          return result("fixture tree [1] input [2] Submit", !options.noImage && args.include_screenshot !== false);
        }
        if (options.inputFails && action !== "list_apps") return { ...result("Accessibility permission pending for AIClaw Computer Use.app"), isError: true };
        if (action === "start_app" && process.platform !== "darwin") return result(JSON.stringify({ app, verified: !options.launchUnverified, launch_requested: true }));
        return result(action === "list_apps" ? "Fixture — com.example.fixture" : "input dispatched");
      },
      async close() { closed++; },
    };
  });
  const call = (action: string, args: Record<string, unknown> = {}, sessionId = "chat", turnId = "turn") => controller.perform({ sessionId, turnId, action, arguments: args });
  const observe = async (session = "chat", turn = "turn", args = {}) => {
    const state = await call("get_app_state", { app, include_ax: true, ...args }, session, turn);
    return state.text.match(/observation_id: ([\w-]+)/)![1]!;
  };
  return { controller, calls, call, observe, counts: () => ({ created, closed }) };
}

test("portable Markdown paste renders structural HTML and escapes embedded markup", async () => {
  const h = harness({ htmlPasteOnly: true });
  try {
    const observation_id = await h.observe();
    await h.call("paste", { app, observation_id, text: "# 标题 🌦️\n\n**加粗** & <script>alert(1)</script>\n\n|列|值|\n|-|-|\n|A|B|", format: "md", verify_after: false });
    const sent = h.calls.at(-1)!.args;
    assert.equal(sent.format, "html");
    assert.match(sent.text, /<h1>标题 🌦️<\/h1>/);
    assert.match(sent.text, /<strong>加粗<\/strong>/);
    assert.match(sent.text, /<table>/);
    assert.ok(!sent.text.includes("<script>"));
  } finally { await h.controller.close(); }
});

test("service is lazy and discovery uses an isolated MCP connection", async () => {
  const h = harness(); assert.equal(h.counts().created, 0);
  await h.call("list_apps"); await h.call("list_apps", {}, "other");
  assert.deepEqual(h.calls.map(c => c.connection), [1, 2]);
  assert.deepEqual(h.calls[0]!.args, {});
  await h.controller.close(); assert.equal(h.counts().closed, 2);
});

test("arbitrary tools, old Cua routing and extra fields are rejected before dispatch", async () => {
  const h = harness();
  for (const action of ["run_script", "get_window_state", "list_windows", "launch_app", "clipboard_read"]) await assert.rejects(h.call(action), /Unsupported computer operation/);
  for (const key of ["session", "pid", "element_token", "delivery_mode", "window_placement", "screenshot_out_file"]) await assert.rejects(h.call("get_app_state", { app, [key]: "override" }), /Unsupported computer argument/);
  assert.equal(h.counts().created, 0);
});

test("self application, helper and Electron cannot be observed or controlled", async () => {
  const h = harness();
  for (const target of ["AIClaw", "com.github.chowyu12.aiclaw", "com.github.chowyu12.aiclaw.computer-use", "Electron", "/Applications/AIClaw.app", "C:\\apps\\AIClaw.exe", String(process.pid)]) await assert.rejects(h.call("get_app_state", { app: target }), /Refusing/);
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

test("switching the active app invalidates the previous native window context", async () => {
  const h = harness(); const id = await h.observe();
  await h.call("get_app_state", { app: process.platform === "darwin" ? "com.other.fixture" : "124" });
  await assert.rejects(h.call("click", { app, observation_id: id, x: 1, y: 2 }), /Observe this app/);
  assert.equal(h.calls.filter(c => c.action === "click").length, 0); await h.controller.close();
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
  const h = harness({ noImage: true }); await assert.rejects(h.observe(), /Grant Accessibility and Screen Recording to AIClaw Computer Use/); await h.controller.close();
  const other = harness(); const id = await other.observe("chat", "turn", { include_ax: false });
  await assert.rejects(other.call("click", { app, observation_id: id, element_index: "1" }), /accessibility observation/); await other.controller.close();
});

test("input errors preserve native permissions diagnostics and never replay input", async () => {
  const h = harness({ inputFails: true }); const id = await h.observe();
  await assert.rejects(h.call("click", { app, observation_id: id, element_index: "2" }), /Accessibility permission pending.*Outcome may be unknown/);
  await assert.rejects(h.call("click", { app, observation_id: id, element_index: "2" }), /Observe this app/);
  assert.equal(h.calls.filter(c => c.action === "click").length, 1); assert.equal(h.counts().closed, 0);
  await h.call("query", { app, role: "AXTextField" });
  assert.equal(h.counts().created, 1); await h.controller.close();
});

test("post-action failure preserves dispatch receipt without fabricating success", async () => {
  const h = harness({ postFails: true }); const id = await h.observe();
  const output = await h.call("click", { app, observation_id: id, element_index: "2" });
  assert.match(output.text, /input dispatched/); assert.match(output.text, /verification failed.*capture timeout/); assert.match(output.text, /Do not automatically replay/);
  assert.equal(h.calls.filter(c => c.action === "click").length, 1); assert.equal(h.counts().closed, 1);
});

test("transient window settling retries observation without replaying input", async () => {
  const h = harness({ transientPostFailure: true }); const id = await h.observe();
  const state = await h.call("click", { app, observation_id: id, element_index: "2" });
  assert.ok(state.imageBase64); assert.match(state.text, /observation_id/);
  assert.equal(h.calls.filter(c => c.action === "click").length, 1);
  assert.equal(h.calls.filter(c => c.action === "get_app_state").length, 3);
  assert.equal(h.counts().closed, 0); await h.controller.close();
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

test("query-capable services default to screenshot-only reads; other platforms keep AX", async () => {
  const fast = harness(); const state = await fast.call("get_app_state", { app });
  assert.equal(fast.calls[0]!.args.include_ax, false);
  const id = state.text.match(/observation_id: ([\w-]+)/)![1]!;
  await assert.rejects(fast.call("click", { app, observation_id: id, element_index: "1" }), /accessibility observation/);
  await fast.controller.close();
  const legacy = harness({ noQuery: true }); await legacy.call("get_app_state", { app });
  assert.equal(legacy.calls[0]!.args.include_ax, true); await legacy.controller.close();
});

test("targeted query indexes authorize only matched semantic actions", async () => {
  const h = harness(); const found = await h.call("query", { app, role: "AXTextField" });
  const id = found.text.match(/observation_id: ([\w-]+)/)![1]!;
  await assert.rejects(h.call("click", { app, observation_id: id, element_index: "999" }), /not returned by this query/);
  await assert.rejects(h.call("press_key", { app, observation_id: id, key: "Return" }), /matched element actions only/);
  await h.call("set_value", { app, observation_id: id, element_index: "1000001", value: "你好" });
  assert.equal(h.calls.filter(call => call.action === "get_app_state").length, 1);
  assert.equal(h.calls.at(-1)!.args.include_ax, false);
  await assert.rejects(h.call("click", { app, observation_id: id, element_index: "1000001" }), /latest observation_id/);
  await h.controller.close();
});

test("window-specific queries preserve native targeting and authorize text selection", async () => {
  const h = harness();
  for (const window_id of [0, -1, 1.5, 0x100000000, "456"]) await assert.rejects(h.call("query", { app, role: "AXTextField", window_id }), /window_id must/);
  assert.equal(h.calls.length, 0);
  const found = await h.call("query", { app, role: "AXTextField", window_id: 456 });
  assert.equal(h.calls[0]!.args.window_id, 456);
  assert.match(found.text, /"window_id":456/);
  const id = found.text.match(/observation_id: ([\w-]+)/)![1]!;
  await h.call("select_text", { app, observation_id: id, element_index: "1000001", text: "天气 🌦️", prefix: "北京", selection: "cursor_after" });
  assert.deepEqual(h.calls.find(c => c.action === "select_text")!.args, { app, element_index: "1000001", text: "天气 🌦️", prefix: "北京", selection: "cursor_after" });
  assert.equal(h.calls.at(-1)!.args.window_id, 456, "post-action screenshot must verify the queried window");
  await assert.rejects(h.call("select_text", { app, observation_id: id, element_index: "1000001", text: "天气" }), /latest observation_id/);
  await h.controller.close();
});

test("native paste requires observed focus and preserves its own receipt", async () => {
  const h = harness();
  const query = await h.call("query", { app, role: "AXTextField" });
  const queryID = query.text.match(/observation_id: ([\w-]+)/)![1]!;
  await assert.rejects(h.call("paste", { app, observation_id: queryID, text: "**北京**", format: "md" }), /matched element actions/);
  const id = await h.observe();
  const pasted = await h.call("paste", { app, observation_id: id, text: "**北京**", format: "md" });
  assert.equal(h.calls.find(c => c.action === "paste")!.args.format, "md");
  assert.match(pasted.text, /Post-action/); await h.controller.close();
});

test("lifecycle requests require explicit identities, then clear previous observations", async () => {
  const h = harness(); const state = await h.call("start_app", { app });
  const id = state.text.match(/observation_id: ([\w-]+)/)![1]!;
  assert.match(state.text, /Post-action/);
  await h.call("stop_app", { app });
  await assert.rejects(h.call("click", { app, observation_id: id, element_index: "1" }), /Observe this app/);
  await assert.rejects(h.call("start_app", { app: "com.github.chowyu12.aiclaw" }), /Refusing/);
  await h.controller.close();
});

test("portable launch observes only a verified running PID and never replays uncertain launch", { skip: process.platform === "darwin" }, async () => {
  const h = harness();
  await h.call("start_app", {app:"launch:fixture.desktop"});
  assert.equal(h.calls.find(item=>item.action==="get_app_state")!.args.app,app);
  await assert.rejects(h.call("query",{app:"launch:fixture.desktop",text:"input"}), /PID/);
  await assert.rejects(h.call("start_app",{app:"launch:../../fixture.desktop"}), /PID/);
  await h.controller.close();
  const uncertain = harness({launchUnverified:true});
  const receipt = await uncertain.call("start_app",{app:"launch:fixture.desktop"});
  assert.match(receipt.text,/not verified/);
  assert.deepEqual(uncertain.calls.map(item=>item.action),["start_app"]);
  await uncertain.controller.close();
});

test("unchanged screenshots are reported without pretending dispatch proves progress", async () => {
  const h = harness(); const id = await h.observe();
  const state = await h.call("click", { app, observation_id: id, element_index: "2", key: "shift", duration: 0.1 });
  assert.match(state.text, /Screenshot unchanged/);
  assert.equal(h.calls.find(c => c.action === "click")!.args.key, "shift");
  await assert.rejects(h.call("click", { app, observation_id: id, click_method: "sky_click" }), /Unsupported computer argument/);
  await h.controller.close();
});

test("service startup failure is retryable only on an explicit later observation", async () => {
  const h = harness({ startFails: true }); await assert.rejects(h.observe(), /startup failed/); await h.observe(); assert.equal(h.counts().created, 2); await h.controller.close();
});

test("runtime capabilities reject unsupported fields rather than silently dropping them", async () => {
  const h = harness({ missingField: true }); const id = await h.observe();
  await assert.rejects(h.call("click", { app, observation_id: id, x: 1, y: 2 }), /does not support click.x/); await h.controller.close();
});

async function peer(mode = "ok", timeout = 300, sourceIdentity?: string) {
  const directory = await mkdtemp(join(tmpdir(), "aiclaw-native-test-"));
  const path = process.platform === "win32" ? String.raw`\\.\pipe\aiclaw-native-${createHash("sha256").update(directory).digest("hex")}` : join(directory, "ipc.sock");
  const requests: any[] = []; const sockets = new Set<import("node:net").Socket>();
  const server = createServer(socket => {
    sockets.add(socket); socket.on("close", () => sockets.delete(socket)); let buffer = "";
    socket.on("data", chunk => {
      buffer += String(chunk); let end;
      while ((end = buffer.indexOf("\n")) >= 0) {
        const line = buffer.slice(0, end); buffer = buffer.slice(end + 1); const frame = JSON.parse(line); requests.push(frame);
        if (mode === "malformed") { socket.write("bad JSON\n"); continue; }
        if (frame.requestType === "ComputerUseIPCHandshakeRequest") {
          socket.write(JSON.stringify({ id: frame.id, result: { protocolVersion: mode === "version" ? "other" : COMPUTER_PROTOCOL, serverVersion: "1", serviceSourceSHA256: "portable-source", tools: [{ name: "list_apps", inputSchema: { properties: {} } }] } }) + "\n"); continue;
        }
        if (mode === "timeout") continue;
        if (mode === "native-error") { socket.write(JSON.stringify({ id: frame.id, error: { message: "native validation refused" } }) + "\n"); continue; }
        const reply = () => socket.write(JSON.stringify({ id: frame.id, result: result("native receipt") }) + "\n");
        if (mode === "slow" && frame.requestType === "ComputerUseIPCAppGetSkyshotRequest") setTimeout(reply, timeout * 1.5);
        else reply();
      }
    });
  });
  await new Promise<void>((resolve, reject) => { server.once("error", reject); server.listen(path, resolve); });
  const socket = createConnection(path); await new Promise<void>((resolve, reject) => { socket.once("connect", resolve); socket.once("error", reject); });
  const runtime = sourceIdentity ? new ComputerPortableRuntime(socket, sourceIdentity) : new ComputerNativeRuntime(socket, timeout);
  return { runtime, requests, async close() { await runtime.close(); for (const socket of sockets) socket.destroy(); await new Promise<void>(resolve => server.close(() => resolve())); await rm(directory, { recursive: true, force: true }); } };
}

test("startup permission setup uses private IPC without becoming an Agent action", async () => {
  const h = await peer();
  try {
    await h.runtime.initialize();
    await h.runtime.setupPermissions();
    assert.equal(h.requests[1].requestType, "ComputerUseIPCPermissionSetupRequest");
    assert.deepEqual(h.requests[1].request, {});
    assert.ok(!h.runtime.tools.some(tool => tool.name === "permission_setup"));
    await assert.rejects(h.runtime.call("permission_setup", {}), /Unsupported native operation/);
    assert.equal(h.requests.length, 2, "Agent calls cannot open the setup window");
  } finally { await h.close(); }
});

test("real IPC framing maps the nine actions to typed native requests", async () => {
  const h = await peer();
  try {
    await h.runtime.initialize();
    await h.runtime.call("click", { app, element_index: "12", key: "shift", duration: 0.2 });
    await h.runtime.call("scroll", { app, x: 10, y: 20, direction: "down", pages: 0.5 });
    await h.runtime.call("select_text", { app, element_index: "12", text: "天气", selection: "cursor_after" });
    await h.runtime.call("paste", { app, text: "**天气**", format: "md" });
    await h.runtime.call("start_app", { app });
    await h.runtime.call("set_value", { app, element_index: "12", value: "后台", interaction_mode: "background" });
    assert.deepEqual(h.requests[1].request, { app, action: { click: { elementID: "12", key: "shift", duration: 0.2 } } });
    assert.deepEqual(h.requests[2].request.action.scroll, { coordinate: [10, 20], direction: "down", pages: 0.5 });
    assert.equal(h.requests[3].request.action.selectText.selection, "cursorAfter");
    assert.deepEqual(h.requests[4].request.action.paste, { text: "**天气**", format: "md" });
    assert.equal(h.requests[5].requestType, "ComputerUseIPCAppStartRequest");
    assert.deepEqual(h.requests[6].request, { app, interactionMode: "background", action: { setValue: { elementID: "12", value: "后台" } } });
    assert.equal(h.requests[1].jsonrpc, undefined, "The native service is not MCP");
  } finally { await h.close(); }
});

test("wrong IPC version and malformed frames fail closed", async () => {
  for (const mode of ["version", "malformed"]) {
    const h = await peer(mode); try { await assert.rejects(h.runtime.initialize()); } finally { await h.close(); }
  }
});

test("native refusal is a healthy response; timeout poisons the connection without replay", async () => {
  const refused = await peer("native-error");
  try { await refused.runtime.initialize(); assert.equal((await refused.runtime.call("click", { app })).isError, true); assert.equal((await refused.runtime.call("query", { app })).isError, true); } finally { await refused.close(); }
  const timed = await peer("timeout", 50);
  try {
    await timed.runtime.initialize(); await assert.rejects(timed.runtime.call("click", { app }), /outcome is unknown/);
    await assert.rejects(timed.runtime.call("click", { app }), /outcome is unknown/);
    assert.equal(timed.requests.filter(r => r.requestType === "ComputerUseIPCAppPerformActionRequest").length, 1);
  } finally { await timed.close(); }
});

test("observation has a separate deadline from input", async () => {
  const h = await peer("slow", 100);
  try { await h.runtime.initialize(); assert.equal((await h.runtime.call("get_app_state", { app })).content[0]!.text, "native receipt"); } finally { await h.close(); }
});

test("different native generations use separate socket endpoints", () => {
  const dev = "/workspace/AIClaw Computer Use.app", installed = "/Applications/AIClaw.app/Contents/Resources/app/computer-use/AIClaw Computer Use.app";
  assert.match(computerExecutable(dev), /Contents.*MacOS.*AIClawComputerUse$/);
  assert.equal(computerNamespace(dev), computerNamespace(dev)); assert.notEqual(computerSocket(dev), computerSocket(installed));
});

test("packaging accepts only the built architecture and exact native binary identity", async () => {
  const directory = await mkdtemp(join(tmpdir(), "aiclaw-native-package-"));
  try {
    const root = join(directory, "apps/desktop/computer-use");
    const binary = join(root, "AIClaw Computer Use.app/Contents/MacOS/AIClawComputerUse");
    await mkdir(join(root, "AIClaw Computer Use.app/Contents/MacOS"), { recursive: true });
    const bytes = Buffer.from("owned native artifact"); await writeFile(binary, bytes);
    await writeFile(join(root, "AIClaw Computer Use.build.json"), JSON.stringify({ architectures: ["arm64"], executableSHA256: createHash("sha256").update(bytes).digest("hex"), protocolVersion: COMPUTER_PROTOCOL }));
    const destination = join(directory, "staged");
    await stageComputerRuntime({ repo: directory, destination, platform: "darwin", arch: "arm64" });
    assert.deepEqual(await readFile(join(destination, "computer-use/AIClaw Computer Use.app/Contents/MacOS/AIClawComputerUse")), bytes);
    await assert.rejects(stageComputerRuntime({ repo: directory, destination, platform: "darwin", arch: "x64" }), /architecture/);
    await writeFile(binary, "tampered");
    await assert.rejects(stageComputerRuntime({ repo: directory, destination, platform: "darwin", arch: "arm64" }), /build identity/);
  } finally { await rm(directory, { recursive: true, force: true }); }
});

test("deterministic intermediate actions defer captures and require fresh queries", async () => {
  const h = harness();
  const query = async () => {
    const state = await h.call("query", { app, text: "Fixture" });
    return state.text.match(/observation_id: ([\w-]+)/)![1]!;
  };
  const first = await query();
  const filled = await h.call("set_value", { app, observation_id: first, element_index: "1000001", value: "hello", verify_after: false });
  assert.equal(filled.imageBase64, undefined);
  assert.match(filled.text, /screenshot deferred/);
  await assert.rejects(h.call("click", { app, observation_id: first, element_index: "1000001", verify_after: false }), /latest observation_id/);
  const second = await query();
  await h.call("click", { app, observation_id: second, element_index: "1000001", verify_after: false });
  assert.deepEqual(h.calls.map(call => call.action), ["query", "set_value", "query", "click"]);
  assert.ok(h.calls.every(call => !("verify_after" in call.args)));
  const final = await h.call("get_app_state", { app });
  assert.equal(final.imageBase64, image);
  await h.controller.close();
});

test("verification flags are typed and failed captures stop scripted continuation", async () => {
  const h = harness({ postFails: true });
  const id = await h.observe();
  await assert.rejects(h.call("click", { app, observation_id: id, x: 1, y: 1, verify_after: "false" }), /must be a boolean/);
  const state = await h.call("click", { app, observation_id: id, x: 1, y: 1 });
  assert.equal(state.verificationFailed, true);
  await h.controller.close();
});

test("application strategies match exact identities and provide bounded guidance", async () => {
  const { computerAppPolicy } = await import("../apps/desktop/src/main/computer-policy.ts");
  for (const [id, expected] of [["com.apple.finder", "finder"], ["com.apple.Safari", "safari"], ["com.apple.TextEdit", "textedit"]]) {
    const policy = computerAppPolicy(id!);
    assert.equal(policy.policy_id, expected); assert.equal(policy.guidance_only, true);
    assert.ok(policy.instructions.length > 4); assert.ok(policy.sources.every(source => source.startsWith("https://support.apple.com/")));
    assert.ok(policy.unsupported_background.includes("keyboard"));
  }
  assert.equal(computerAppPolicy("com.apple.Safari.attacker").policy_id, "generic");
  assert.equal(computerAppPolicy("Safari").policy_id, "generic");
});

test("policy lookup does not launch a runtime and instructions attach once per turn", { skip: process.platform !== "darwin" }, async () => {
  const h = harness();
  const policy = JSON.parse((await h.call("app_policy", { app: "com.apple.Safari" })).text);
  assert.equal(policy.policy_id, "safari"); assert.equal(h.counts().created, 0);
  const first = await h.call("query", { app: "com.apple.Safari", text: "address" });
  assert.match(first.text, /app_specific_instructions:.*safari/);
  assert.ok(first.text.match(/observation_id: ([\w-]+)/));
  const next = await h.call("query", { app: "com.apple.Safari", text: "address" });
  assert.doesNotMatch(next.text, /app_specific_instructions/);
  const otherTurn = await h.call("query", { app: "com.apple.Safari", text: "address" }, "chat", "next-turn");
  assert.match(otherTurn.text, /app_specific_instructions/);
  await h.controller.close();
});

test("portable IPC verifies its loaded service source before exposing tools", async () => {
  const valid = await peer("ok", 300, "portable-source");
  try { await valid.runtime.initialize(); assert.equal((await valid.runtime.call("list_apps", {})).content[0]!.text, "native receipt"); }
  finally { await valid.close(); }
  for (const [mode, identity] of [["ok", "wrong-source"], ["version", "portable-source"]]) {
    const refused = await peer(mode, 300, identity);
    try { await assert.rejects(refused.runtime.initialize(), /identity or protocol/); assert.equal(refused.runtime.tools.length, 0); }
    finally { await refused.close(); }
  }
});
