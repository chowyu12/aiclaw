/** Opt-in macOS desktop acceptance against a temporary, local-only AppKit fixture.
 * Build desktop first, then run: electron scripts/smoke-computer-live.cjs
 * Requires Xcode CLT and Accessibility/Screen Recording for this Electron host.
 */
const { app } = require("electron");
const { spawn, execFile } = require("node:child_process");
const { promisify } = require("node:util");
const { mkdtemp, readFile, rm } = require("node:fs/promises");
const { setTimeout: pause } = require("node:timers/promises");
const { tmpdir } = require("node:os");
const path = require("node:path");
const assert = require("node:assert/strict");
const { pathToFileURL } = require("node:url");
const run = promisify(execFile);
app.setName("aiclaw-computer-acceptance");
app.setPath("userData", path.join(tmpdir(), "aiclaw-computer-acceptance"));
const watchdog = setTimeout(() => { console.error("Desktop acceptance timed out"); app.exit(1); }, 90_000);
let child, controller, temporary;
app.whenReady().then(async () => {
  if (process.platform !== "darwin") throw Error("This fixture requires macOS");
  const sdk = await import("@trycua/cua-driver");
  const permissions = sdk.currentMacOsPermissionStatus();
  if (!permissions.accessibility || !permissions.screenRecording) throw Error("Grant this Electron host Accessibility and Screen Recording before running desktop acceptance");
  temporary = await mkdtemp(path.join(tmpdir(), "aiclaw-desktop-acceptance-"));
  const binary = path.join(temporary, "AIClawComputerFixture");
  const receiptFile = path.join(temporary, "submitted.json");
  await run("swiftc", [path.join(__dirname, "fixtures", "computer-macos.swift"), "-o", binary], { timeout: 30_000 });
  const { ComputerController } = await import(pathToFileURL(path.join(__dirname, "..", "apps", "desktop", "dist", "main", "computer.js")));
  let latest;
  controller = new ComputerController(async () => {
    const native = sdk.CuaDriver.create({ embedded: true, claudeCodeCompatibility: false });
    return {
      async callTool(name, raw, opts) {
        const result = await native.callTool(name, raw, opts);
        if (name === "get_window_state" && result.structuredJson) latest = JSON.parse(result.structuredJson);
        return result;
      },
      shutdown: () => native.shutdown(), uniffiDestroy: () => native.uniffiDestroy?.(),
    };
  });
  const call = (action, args) => controller.perform({ sessionId: "acceptance-fixture", turnId: "t1", action, arguments: args });
  child = spawn(binary, [receiptFile], { stdio: "ignore" });
  let window;
  for (let i = 0; i < 40 && !window; i++) {
    const response = await call("list_windows", { pid: child.pid });
    // structuredJson is the last JSON line in the controller result.
    const data = JSON.parse(response.text.slice(response.text.lastIndexOf("\n{") + 1));
    window = data.windows?.find(w => w.pid === child.pid && w.title === "AIClaw Computer Acceptance Fixture");
    if (!window) await pause(100);
  }
  assert.ok(window, "fixture window was not discovered");
  const target = { pid: child.pid, window_id: window.window_id };
  const observed = await call("get_window_state", target);
  assert.ok(observed.imageBase64, "real native screenshot missing");
  const token = label => {
    const row = latest.tree_markdown?.split("\n").find(line => line.includes(label));
    const index = row?.match(/\[(\d+)\]/)?.[1];
    assert.ok(index, `missing fixture control: ${label}`);
    return `${latest.snapshot_id}:${index}`;
  };
  const text = "北京天气 🌦️";
  const typed = await call("type_text", { ...target, element_token: token("Fixture input"), text });
  assert.ok(typed.imageBase64, "post-input screenshot missing");
  await call("click", { ...target, element_token: token("Submit fixture") });
  const submitted = JSON.parse(await readFile(receiptFile, "utf8"));
  assert.equal(submitted.value, text, "Chinese/Unicode input did not arrive intact");
  console.log("PASS: native window discovery, screenshot, AX click, Chinese/emoji input, submit and post-action observation (local AppKit fixture only).");
}).then(() => finish(0)).catch(async error => { console.error(error); await finish(1); });
async function finish(code) {
  clearTimeout(watchdog);
  try { if (controller) await controller.close(); }
  finally {
    child?.kill("SIGTERM");
    if (temporary) await rm(temporary, { recursive: true, force: true });
    app.exit(code);
  }
}
