import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createHash } from "node:crypto";
import type { ToolResult } from "@trycua/cua-driver";
import { ComputerController } from "../apps/desktop/src/main/computer.ts";
import { driverTarget, verifyIntegrity } from "./stage-computer-driver.mjs";

const target = { pid: 123, window_id: 456 };
const snapshotID = "s00000001";
const imageBase64 = "iVBORw0KGgo=";
function result(text = "receipt", data?: unknown, image = false): ToolResult {
  return { text, images: image ? [{ mimeType: "image/png", dataBase64: imageBase64 }] : [],
    structuredJson: data ? JSON.stringify(data) : undefined, isError: false, degraded: false, rawJson: "{}" };
}
function harness(options: { fail?: string; postFails?: boolean; self?: boolean; noImage?: boolean; factoryFails?: boolean } = {}) {
  const calls: { action: string; args: Record<string, any> }[] = [];
  let reads = 0, closed = 0, destroyed = 0, created = 0;
  const controller = new ComputerController(async () => {
    created++;
    if (options.factoryFails && created === 1) throw Error("native library unavailable");
    return {
      async callTool(action: string, raw: string) {
        const args = JSON.parse(raw); calls.push({ action, args });
        if (options.fail === action) return { ...result("permission_missing: enable Accessibility for AIClaw"), isError: true, errorCode: "permission_missing" };
        if (action === "list_windows") return result("windows", { windows: [{ ...target, pid: args.pid, app_name: options.self ? "AIClaw" : "Fixture" }] });
        if (action === "get_window_state") {
          reads++;
          if (reads > 1 && options.postFails) throw Error("capture_timeout");
          return result("window tree", { pid: args.pid, window_id: args.window_id, snapshot_id: reads === 1 ? snapshotID : "s00000002", capture_id: `capture-${reads}`, truncated: true }, !options.noImage);
        }
        return result("input dispatched; effect is unverifiable", { effect: "unverifiable" });
      },
      async shutdown() { closed++; }, uniffiDestroy() { destroyed++; },
    };
  });
  const perform = (action: string, args: Record<string, unknown> = {}, sessionId = "chat-1") => controller.perform({ sessionId, turnId: "turn-1", action, arguments: args });
  const observe = (session = "chat-1") => perform("get_window_state", target, session);
  return { controller, calls, perform, observe, counts: () => ({ closed, destroyed, created }) };
}

test("driver is lazy; discovery preserves structured output and host-owned session", async () => {
  const h = harness();
  assert.equal(h.counts().created, 0);
  await h.perform("list_windows", { app: "Fixture" });
  assert.deepEqual(h.calls[0], { action: "list_windows", args: { app: "Fixture", session: "chat-1" } });
  await h.controller.close();
  assert.deepEqual(h.counts(), { closed: 1, destroyed: 1, created: 1 });
});

test("unknown tools and scope/session/file routing overrides never reach the driver", async () => {
  const h = harness();
  for (const action of ["run_script", "page", "clipboard_read", "screenshot"]) await assert.rejects(h.perform(action), /Unsupported computer operation/);
  for (const key of ["scope", "target", "session", "screenshot_out_file", "debug_image_out", "from_zoom"]) {
    await assert.rejects(h.perform("get_window_state", { ...target, [key]: "override" }), /Unsupported computer argument/);
  }
  assert.equal(h.calls.length, 0);
});

test("input requires exact valid target and a prior observation in the same chat", async () => {
  const h = harness();
  for (const args of [{}, { pid: 0, window_id: 456 }, { ...target, window_id: 1.5 }]) {
    await assert.rejects(h.perform("click", args), /pid and window_id/);
  }
  await assert.rejects(h.perform("click", target), /Observe this exact window/);
  await h.observe();
  await assert.rejects(h.perform("click", target, "other-chat"), /Observe this exact window/);
  await assert.rejects(h.perform("click", { ...target, window_id: 999 }), /Observe this exact window/);
});

test("Unicode text uses native type_text with exact window; returns receipt and fresh screenshot", async () => {
  const h = harness(); await h.observe();
  const text = '北京天气 🌦️\n"你好"';
  const output = await h.perform("type_text", { ...target, element_token: `${snapshotID}:4`, text });
  const input = h.calls.find(c => c.action === "type_text")!;
  assert.equal(input.args.text, text);
  assert.equal(input.args.pid, target.pid);
  assert.equal(input.args.session, "chat-1");
  assert.equal(output.imageBase64, imageBase64);
  assert.match(output.text, /unverifiable/);
  assert.match(output.text, /Post-action window observation/);
  assert.match(output.text, /capture-2/);
  assert.match(output.text, /truncated/);
  await assert.rejects(h.perform("click", { ...target, element_token: `${snapshotID}:4` }), /latest observation/);
});

test("cross-window or forged element tokens are refused before dispatch", async () => {
  const h = harness(); await h.observe();
  for (const token of ["s000000ff:1", `${snapshotID}:1.trailing`, "bad:1"]) {
    await assert.rejects(h.perform("click", { ...target, element_token: token }), /latest observation/);
  }
  assert.equal(h.calls.filter(c => c.action === "click").length, 0);
});

test("self-input refuses by process identity and independently resolved window name", async () => {
  for (const self of [false, true]) {
    const h = harness({ self });
    const args = { ...target, pid: self ? target.pid : process.pid };
    await h.perform("get_window_state", args);
    await assert.rejects(h.perform("press_key", { ...args, key: "Return" }), /AIClaw's own windows/);
    assert.equal(h.calls.filter(c => c.action === "press_key").length, 0);
  }
});

test("upstream permission failure remains an error, never successful empty output", async () => {
  const h = harness({ fail: "get_window_state" });
  await assert.rejects(h.observe(), /permission_missing.*Accessibility/);
  await assert.rejects(h.perform("click", target), /Observe this exact window/);
});

test("post-action capture failure keeps the receipt, invalidates the observation and never retries input", async () => {
  const h = harness({ postFails: true }); await h.observe();
  const output = await h.perform("press_key", { ...target, key: "Return" });
  assert.match(output.text, /Input was dispatched.*verification failed/);
  assert.match(output.text, /Do not automatically retry/);
  assert.equal(h.calls.filter(c => c.action === "press_key").length, 1);
  await assert.rejects(h.perform("press_key", { ...target, key: "Return" }), /Observe this exact window/);
});


test("a failed new observation invalidates the previous cached snapshot", async () => {
  const h = harness({ postFails: true }); await h.observe();
  await assert.rejects(h.observe(), /capture_timeout/);
  await assert.rejects(h.perform("press_key", { ...target, key: "Return" }), /Observe this exact window/);
});

test("background refusal does not automatically escalate to foreground or replay", async () => {
  const h = harness({ fail: "type_text" }); await h.observe();
  await assert.rejects(h.perform("type_text", { ...target, text: "hello" }), /permission_missing/);
  assert.equal(h.calls.filter(c => c.action === "type_text").length, 1);
  await assert.rejects(h.perform("type_text", { ...target, text: "hello" }), /Observe this exact window/);
});

test("missing screenshot is an error; intentional tree-only read cannot ground pixel clicks", async () => {
  const h = harness({ noImage: true });
  await assert.rejects(h.observe(), /no window screenshot/);
  await assert.rejects(h.perform("click", target), /Observe this exact window/);
  await h.perform("get_window_state", { ...target, include_screenshot: false });
  await assert.rejects(h.perform("click", { ...target, x: 1, y: 2 }), /require a fresh window screenshot/);
});

test("release destroys the chat's snapshots; shutdown and handle disposal run once", async () => {
  const h = harness(); await h.observe();
  await h.controller.releaseSession("chat-1");
  assert.equal(h.calls.at(-1)?.action, "end_session");
  await assert.rejects(h.perform("click", target), /Observe this exact window/);
  await h.controller.close(); await h.controller.close();
  assert.equal(h.counts().closed, 1); assert.equal(h.counts().destroyed, 1);
});

test("failed runtime initialization can be retried without caching a rejected promise", async () => {
  const h = harness({ factoryFails: true });
  await assert.rejects(h.observe(), /native library unavailable/);
  await h.observe();
  assert.equal(h.counts().created, 2);
});

test("host and agent expose the same pinned, window-scoped subset", async () => {
  const specs = JSON.parse(readFileSync(new URL("../tools/claw-agent/internal/agent/computer-tools.json", import.meta.url), "utf8"));
  for (const spec of specs) {
    const h = harness();
    if (!["list_apps", "list_windows", "launch_app"].includes(spec.action)) {
      assert.ok(spec.schema.required.includes("pid")); assert.ok(spec.schema.required.includes("window_id"));
    }
    for (const property of Object.keys(spec.schema.properties)) {
      // Check admission only; fixture driver accepts its action-specific inputs.
      try { await h.perform(spec.action, { ...(!["list_apps", "list_windows", "launch_app"].includes(spec.action) ? target : {}), [property]: "fixture" }); }
      catch (error) { assert.doesNotMatch(String(error), /Unsupported computer (operation|argument)/); }
    }
  }
});

test("cross-package selects the target library and verifies exact SHA-512 bytes", () => {
  assert.equal(driverTarget("darwin", "arm64"), "@trycua/cua-driver-darwin-arm64");
  assert.equal(driverTarget("win32", "x64"), "@trycua/cua-driver-win32-x64-msvc");
  assert.equal(driverTarget("linux", "x64"), "@trycua/cua-driver-linux-x64-gnu");
  assert.throws(() => driverTarget("freebsd", "x64"), /Unsupported/);
  const bytes = Buffer.from("native fixture");
  const integrity = `sha512-${createHash("sha512").update(bytes).digest("base64")}`;
  verifyIntegrity(bytes, integrity);
  assert.throws(() => verifyIntegrity(Buffer.from("changed"), integrity), /integrity mismatch/);
  assert.throws(() => verifyIntegrity(bytes, ""), /Missing SHA-512/);
});
