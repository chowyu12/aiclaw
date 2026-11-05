/** Opt-in native acceptance. Requires Accessibility and Screen Recording for
 * Open Computer Use.app, not Electron. Operates only its temporary fixture app.
 */
const { app } = require("electron");
const { spawn, execFile } = require("node:child_process");
const { promisify } = require("node:util");
const { mkdtemp, readFile, rm, mkdir, writeFile } = require("node:fs/promises");
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
  const { computerExecutable } = await import(pathToFileURL(path.join(__dirname, "../apps/desktop/dist/main/computer-runtime.js")));
  const runtimeRoot = path.dirname(require.resolve("open-computer-use/package.json"));
  const checked = await run(computerExecutable(runtimeRoot), ["doctor", "--json"], { timeout: 15_000 }).catch(error => ({ stdout: error.stdout }));
  if (!JSON.parse(checked.stdout).allGranted) throw Error("Grant Open Computer Use.app Accessibility and Screen Recording before native acceptance; no fixture input was dispatched");
  temporary = await mkdtemp(path.join(tmpdir(), "aiclaw-desktop-acceptance-"));
  const contents = path.join(temporary, "AIClaw Computer Fixture.app", "Contents");
  await mkdir(path.join(contents, "MacOS"), { recursive: true });
  const binary = path.join(contents, "MacOS", "Fixture");
  await writeFile(path.join(contents, "Info.plist"), `<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.aiclaw.computerfixture</string><key>CFBundleExecutable</key><string>Fixture</string><key>CFBundleName</key><string>AIClaw Computer Fixture</string><key>CFBundlePackageType</key><string>APPL</string><key>LSUIElement</key><true/></dict></plist>`);
  const receiptFile = path.join(temporary, "submitted.json");
  await run("swiftc", [path.join(__dirname, "fixtures/computer-macos.swift"), "-o", binary], { timeout: 30_000 });
  const { ComputerController } = await import(pathToFileURL(path.join(__dirname, "../apps/desktop/dist/main/computer.js")));
  controller = new ComputerController();
  const target = "com.aiclaw.computerfixture";
  const call = (action, args) => controller.perform({ sessionId: "acceptance-fixture", turnId: "t1", action, arguments: { app: target, ...args } });
  child = spawn(binary, [receiptFile], { stdio: "ignore" });
  let observed;
  for (let i = 0; i < 40 && !observed; i++) {
    try { observed = await call("get_app_state", {}); } catch { await pause(100); }
  }
  assert.ok(observed?.imageBase64, "fixture native screenshot missing");
  const observationID = state => state.text.match(/observation_id: ([\w-]+)/)?.[1];
  const element = (state, label) => {
    const row = state.text.split("\n").find(line => line.includes(label));
    const index = row?.match(/^\s*(\d+)\s/)?.[1];
    assert.ok(index, `missing fixture control ${label}: ${state.text}`); return index;
  };
  const text = "北京天气 🌦️";
  const typed = await call("set_value", { observation_id: observationID(observed), element_index: element(observed, "Fixture input"), value: text });
  assert.ok(typed.imageBase64, "post-input screenshot missing");
  await call("click", { observation_id: observationID(typed), element_index: element(typed, "Submit fixture") });
  const submitted = JSON.parse(await readFile(receiptFile, "utf8"));
  assert.equal(submitted.value, text, "Chinese/Unicode input did not arrive intact");
  console.log("PASS: independent native app screenshot, AX set_value/click, Chinese/emoji input, submit receipt and post-action observation (temporary AppKit fixture only).");
}).then(() => finish(0)).catch(async error => { console.error(error); await finish(1); });
async function finish(code) {
  clearTimeout(watchdog);
  try { if (controller) await controller.close(); }
  finally { child?.kill("SIGTERM"); if (temporary) await rm(temporary, { recursive: true, force: true }); app.exit(code); }
}
