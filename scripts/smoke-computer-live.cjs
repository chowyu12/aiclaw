/** Opt-in native acceptance. Requires Accessibility and Screen Recording for
 * AIClaw Computer Use.app, not Electron. Operates only its temporary fixture app.
 */
const { app, clipboard } = require("electron");
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
  const { ComputerNativeRuntime } = await import(pathToFileURL(path.join(__dirname, "../apps/desktop/dist/main/computer-runtime.js")));
  const probe = await ComputerNativeRuntime.create();
  try {
    const response = await probe.call("permission_status", {});
    const status = JSON.parse(response.content[0].text);
    if (!status.accessibilityTrusted || !status.screenCaptureGranted) {
      await probe.call("get_app_state", { app: "com.aiclaw.computerfixture" });
      throw Error("Grant Accessibility and Screen Recording to AIClaw Computer Use.app before live acceptance. Its permission setup window is open; no fixture input was dispatched.");
    }
  } finally { await probe.close(); }
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
  const call = async (action, args) => {
    try { return await controller.perform({ sessionId: "acceptance-fixture", turnId: "t1", action, arguments: { app: target, ...args } }); }
    catch (error) { throw Error(`Fixture ${action}: ${String(error)}`); }
  };
  child = spawn(binary, [receiptFile], { stdio: "ignore" });
  let observed;
  for (let i = 0; i < 40 && !observed; i++) {
    try { observed = await call("get_app_state", {}); } catch { await pause(100); }
  }
  assert.ok(observed?.imageBase64, "fixture native screenshot missing");
  const observationID = state => state.text.match(/observation_id: ([\w-]+)/)?.[1];
  const element = state => {
    const data = JSON.parse(state.text.split("\nobservation_id:")[0]);
    assert.equal(data.matches.length, 1, "fixture query must resolve exactly one control");
    return String(data.matches[0].index);
  };
  const query = (text, role) => call("query", { text, role, exact: true });
  const submit = async () => {
    const button = await query("Submit fixture", "AXButton");
    try { await call("click", { observation_id: observationID(button), element_index: element(button) }); }
    catch (error) {
      await call("get_app_state", {});
      console.log(`Submit returned an uncertain result; verifying fixture receipt without replay: ${String(error)}`);
    }
    return JSON.parse(await readFile(receiptFile, "utf8"));
  };
  const started = Date.now();
  const focusable = await query("Fixture input", "AXTextField");
  await call("click", { observation_id: observationID(focusable), element_index: element(focusable) });
  const input = await query("Fixture input", "AXTextField");
  const text = "北京天气 🌦️";
  const typed = await call("set_value", { observation_id: observationID(input), element_index: element(input), value: text });
  assert.ok(typed.imageBase64, "post-input screenshot missing");
  const selectable = await query("Fixture input", "AXTextField");
  const selected = await call("select_text", { observation_id: observationID(selectable), element_index: element(selectable), text, selection: "text" });
  assert.ok(selected.imageBase64, "post-selection screenshot missing");
  const priorClipboard = await clipboard.readText();
  const pastedText = "粘贴验证 📋";
  const pasted = await call("paste", { observation_id: observationID(selected), text: pastedText, format: "text" });
  if (pasted.imageBase64 && process.env.AICLAW_COMPUTER_EVIDENCE_DIR) {
    await mkdir(process.env.AICLAW_COMPUTER_EVIDENCE_DIR, { recursive: true });
    await writeFile(path.join(process.env.AICLAW_COMPUTER_EVIDENCE_DIR, "native-fixture-paste.png"), Buffer.from(pasted.imageBase64, "base64"));
  }
  assert.ok(pasted.imageBase64, `post-paste screenshot missing: ${pasted.text}`);
  assert.equal(await clipboard.readText(), priorClipboard, "paste must restore the clipboard");
  const valueCheck = JSON.parse((await query("Fixture input", "AXTextField")).text.split("\nobservation_id:")[0]);
  assert.equal(valueCheck.matches[0].value, pastedText, "paste must update the focused editor before submit");
  const keyboard = await call("get_app_state", {});
  const selectAll = await call("press_key", { observation_id: observationID(keyboard), key: "super+a", duration: 0.01 });
  const typedText = "原生输入 🌦️";
  const direct = await call("type_text", { observation_id: observationID(selectAll), text: typedText });
  await call("press_key", { observation_id: observationID(direct), key: "Return" });
  const typedCheck = JSON.parse((await query("Fixture input", "AXTextField")).text.split("\nobservation_id:")[0]);
  assert.equal(typedCheck.matches[0].value, typedText, "native Unicode keyboard input was not delivered");

  for (const [format, source, expected] of [["md", "**Markdown粗体**", "Markdown粗体"], ["html", "<b>HTML富文本</b>", "HTML富文本"]]) {
    console.log(`Verifying native ${format} rich-text paste`);
    const editor = await query("Fixture rich editor", "AXTextArea");
    const focused = await call("click", { observation_id: observationID(editor), element_index: element(editor), duration: 0.01 });
    const all = await call("press_key", { observation_id: observationID(focused), key: "super+a" });
    await call("paste", { observation_id: observationID(all), text: source, format });
    const receipt = await submit();
    assert.equal(receipt.rich.trim(), expected, `${format} must arrive as rendered rich text`);
    assert.equal(receipt.rich_bold, true, `${format} bold formatting must survive the native paste`);
    assert.equal(await clipboard.readText(), priorClipboard, `${format} paste must restore the clipboard`);
  }

  const padQuery = await query("Fixture gesture pad", "AXGroup");
  const pad = JSON.parse(padQuery.text.split("\nobservation_id:")[0]).matches[0];
  assert.ok(pad?.frame, "gesture pad must expose geometry");
  const geometryState = await call("get_app_state", {});
  const geometry = JSON.parse(geometryState.text.split("\n")[0]);
  const x = (pad.frame.x + pad.frame.width / 2 - geometry.window_frame.x) / geometry.window_frame.width * geometry.image_width;
  const y = (pad.frame.y + pad.frame.height / 2 - geometry.window_frame.y) / geometry.window_frame.height * geometry.image_height;
  const dragged = await call("drag", { observation_id: observationID(geometryState), from_x: x - 20, from_y: y, to_x: x + 20, to_y: y + 10 });
  await call("scroll", { observation_id: observationID(dragged), x, y, direction: "down", pages: 0.25 });

  const stepperQuery = await query("Fixture stepper", "AXIncrementor");
  const stepper = JSON.parse(stepperQuery.text.split("\nobservation_id:")[0]).matches[0];
  assert.ok(stepper?.actions.includes("AXIncrement"), "stepper must expose AXIncrement");
  try { await call("perform_secondary_action", { observation_id: observationID(stepperQuery), element_index: String(stepper.index), action: "AXIncrement" }); }
  catch { await call("get_app_state", {}); }
  const submitted = await submit();
  assert.equal(submitted.value, typedText, "Chinese/Unicode input did not arrive intact");
  assert.ok(submitted.drags > 0, "drag must reach the fixture");
  assert.ok(submitted.scrolls > 0, "coordinate scroll must reach the fixture");
  assert.equal(submitted.stepper, 1, "secondary AX action must increment exactly once");
  if (process.env.AICLAW_COMPUTER_EVIDENCE_DIR) {
    const verified = await call("get_app_state", {});
    await writeFile(path.join(process.env.AICLAW_COMPUTER_EVIDENCE_DIR, "native-fixture-verified.png"), Buffer.from(verified.imageBase64, "base64"));
    await writeFile(path.join(process.env.AICLAW_COMPUTER_EVIDENCE_DIR, "native-live-summary.json"), JSON.stringify({ passed: true, action_categories: 9, rich_formats: ["md", "html"], ...submitted }, null, 2) + "\n");
  }
  console.log(`Fixture fast-path execution: ${Date.now() - started} ms`);
  const stopped = await call("stop_app", {});
  assert.equal(JSON.parse(stopped.text).verified, true, "normal fixture quit must be verified");
  console.log("PASS: all nine native action categories, targeted query, Unicode text, clipboard restoration, screenshots, fixture receipt and normal quit (temporary AppKit fixture only).");
}).then(() => finish(0)).catch(async error => { console.error(error); await finish(1); });
async function finish(code) {
  clearTimeout(watchdog);
  try { if (controller) await controller.close(); }
  finally { child?.kill("SIGTERM"); if (temporary) await rm(temporary, { recursive: true, force: true }); app.exit(code); }
}
