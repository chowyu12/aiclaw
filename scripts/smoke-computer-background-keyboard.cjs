/** Opt-in public PID keyboard acceptance. Only two disposable fixture apps
 * receive events. No real app/document/message, clipboard write or fallback.
 */
const { app, clipboard, screen } = require("electron");
const assert = require("node:assert/strict");
const { spawn, execFile } = require("node:child_process");
const { promisify } = require("node:util");
const { mkdtemp, mkdir, writeFile, readFile, copyFile, rename, rm } = require("node:fs/promises");
const { tmpdir } = require("node:os");
const path = require("node:path");
const { pathToFileURL } = require("node:url");
const { createHash } = require("node:crypto");
const { setTimeout: pause } = require("node:timers/promises");
const run = promisify(execFile);
app.setName("aiclaw-background-keyboard-acceptance");
app.setPath("userData", path.join(tmpdir(), "aiclaw-background-keyboard-acceptance"));
let runtime, temporary;
const children = [];
const watchdog = setTimeout(() => { console.error("Background PID keyboard acceptance timed out"); void finish(1); }, 120_000);
const parsed = result => {
  const text = result.content.find(item => item.type === "text")?.text ?? "";
  if (result.isError) throw Error(text);
  return {metadata: JSON.parse(text.split("\n")[0]), text};
};
app.whenReady().then(async () => {
  assert.equal(process.platform, "darwin");
  const { ComputerNativeRuntime } = await import(pathToFileURL(path.join(__dirname, "../apps/desktop/dist/main/computer-runtime.js")));
  runtime = await ComputerNativeRuntime.create();
  const permissions = parsed(await runtime.call("permission_status", {})).metadata;
  assert.ok(permissions.accessibilityTrusted && permissions.screenCaptureGranted, "Previously granted helper permissions required; no input sent");
  temporary = await mkdtemp(path.join(tmpdir(), "aiclaw-pid-keyboard-"));
  const files = role => ({receipt: path.join(temporary, `${role}.json`), commands: path.join(temporary, `${role}-command.json`)});
  const electronTarget = process.env.AICLAW_BACKGROUND_KEYBOARD_TARGET === "electron";
  const electronField = process.env.AICLAW_BACKGROUND_KEYBOARD_ELECTRON_FIELD || "input";
  const ids = {target: electronTarget ? "com.aiclaw.backgroundkeyboard.electron" : "com.aiclaw.backgroundkeyboard.target", sentinel: "com.aiclaw.backgroundkeyboard.sentinel"};
  const read = async role => JSON.parse(await readFile(files(role).receipt, "utf8"));
  const until = async (predicate, label, timeout = 4000) => {
    const end = Date.now() + timeout;
    let last;
    do {
      try { const value = await read("target"); last = value; if (await predicate(value)) return value; } catch (error) { if (error.code !== "ENOENT") throw error; }
      await pause(20);
    } while (Date.now() < end);
    throw Error(`Fixture did not confirm ${label}: ${JSON.stringify(last)}`);
  };
  let built;
  for (const role of ["target", "sentinel"]) {
    if (role === "target" && electronTarget) {
      const fixture = path.join(temporary, "target.app");
      // Native copy preserves framework symlinks and does not treat .asar as
      // Electron's virtual filesystem while copying the independent runtime.
      await run("/usr/bin/ditto", [path.resolve(path.dirname(process.execPath), "../.."), fixture]);
      const info = path.join(fixture, "Contents/Info.plist");
      await run("/usr/libexec/PlistBuddy", ["-c", `Set :CFBundleIdentifier ${ids.target}`, info]);
      const { loadMacSigning, verifyMacBundle } = await import(pathToFileURL(path.join(__dirname, "mac-signing.mjs")));
      const signing = loadMacSigning();
      await run("/usr/bin/codesign", ["--force", "--sign", signing.identity, ...(signing.keychain ? ["--keychain", signing.keychain] : []), "--identifier", ids.target, "--timestamp=none", "--entitlements", path.join(__dirname, "../tools/computer-use/electron-entitlements.plist"), fixture]);
      verifyMacBundle(fixture, ids.target, signing);
      const electronChild = spawn(path.join(fixture, "Contents/MacOS/Electron"), [path.join(__dirname, "fixtures/computer-background-keyboard-electron.cjs"), files(role).receipt, files(role).commands, electronField], {stdio: ["ignore", "ignore", "pipe"]});
      children.push(electronChild);
      let stderr = "";
      electronChild.stderr.on("data", data => { stderr = (stderr + String(data)).slice(-8192); });
      for (let attempt = 0; attempt < 80; attempt++) {
        if (electronChild.exitCode !== null) throw Error(`Independent Electron fixture exited (${electronChild.exitCode}): ${stderr}`);
        try { if ((await read(role)).window_id > 0) break; } catch {}
        await pause(50);
      }
      try { await read(role); } catch { throw Error(`Independent Electron fixture did not become ready: ${stderr}`); }
      continue;
    }
    const contents = path.join(temporary, `${role}.app`, "Contents"), binary = path.join(contents, "MacOS", "Fixture");
    await mkdir(path.dirname(binary), {recursive: true});
    await writeFile(path.join(contents, "Info.plist"), `<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>${ids[role]}</string><key>CFBundleExecutable</key><string>Fixture</string><key>CFBundleName</key><string>AIClaw Background Keyboard ${role}</string><key>CFBundlePackageType</key><string>APPL</string><key>LSUIElement</key><true/></dict></plist>`);
    if (built) await copyFile(built, binary);
    else { await run("xcrun", ["swiftc", path.join(__dirname, "fixtures/computer-background-keyboard-macos.swift"), "-o", binary], {timeout: 60_000}); built = binary; }
    children.push(spawn(binary, [role, files(role).receipt, files(role).commands], {stdio: "ignore"}));
    for (let attempt = 0; attempt < 50; attempt++) {
      try { if ((await read(role)).window_id > 0) break; } catch {}
      await pause(50);
    }
  }
  await pause(200);
  const target = await read("target"), sentinel = await read("sentinel");
  assert.equal(target.editor_is_first_responder, true, "The target fixture must establish its own editable focus before becoming background");
  const foreground = async () => parsed(await runtime.call("frontmost_window", {})).metadata.pid;
  assert.equal(await foreground(), sentinel.pid, "Sentinel must remain the foreground app");
  const clipboardSnapshot = async () => Promise.all((await clipboard.read()).map(async item => Promise.all(item.types.map(async type => {
    const value = await item.getType(type);
    const bytes = typeof value.arrayBuffer === "function" ? Buffer.from(await value.arrayBuffer()) : Buffer.from(JSON.stringify(value));
    return [type, createHash("sha256").update(bytes).digest("hex")];
  }))));
  const pointer = screen.getCursorScreenPoint(), savedClipboard = await clipboardSnapshot();
  const unchanged = async (stage = "context") => {
    assert.equal(await foreground(), sentinel.pid, "Background delivery must not activate the target");
    assert.deepEqual(screen.getCursorScreenPoint(), pointer);
    assert.deepEqual(await clipboardSnapshot(), savedClipboard);
    const untouched = await read("sentinel");
    if (untouched.text !== sentinel.text || JSON.stringify(untouched.selection) !== JSON.stringify(sentinel.selection) || untouched.events_down.length !== sentinel.events_down.length) console.error(JSON.stringify({stage,sentinel_before: sentinel, sentinel_after: untouched, target_after: await read("target")}));
    assert.equal(untouched.text, sentinel.text); assert.equal(untouched.events_down.length, sentinel.events_down.length);
    assert.deepEqual(untouched.selection, sentinel.selection, "PID/AX routing must not move the foreground editor selection");
    assert.equal(untouched.has_marked_text, sentinel.has_marked_text);
    assert.deepEqual(untouched.marked_range, sentinel.marked_range);
  };
  const observe = async () => parsed(await runtime.call("get_app_state", {app: ids.target, window_id: target.window_id, include_ax: true}));
  const action = (name, args) => runtime.call(name, {app: ids.target, interaction_mode: "background", ...args});
  let nonce = 0;
  const commandFor = async (role, value) => {
    const file = files(role).commands;
    await writeFile(`${file}.tmp`, JSON.stringify({command:value,nonce:String(++nonce)})); await rename(`${file}.tmp`,file); await pause(150);
  };
  const command = value => commandFor("target",value);
  const first = await observe();
  if (!first.metadata.background_keyboard_ready) console.error(JSON.stringify({fixture: target, observation: first.text.slice(0, 8000)}));
  assert.equal(first.metadata.background_keyboard_ready, true, `Focused editable proof was not established: ${JSON.stringify(first.metadata)}`);
  if (process.env.AICLAW_BACKGROUND_KEYBOARD_KEY_PROBE === "1") {
    const cases=[];
    for (const key of ["a","Shift+a","Left","Right","Up","Down","Home","End","Backspace","ForwardDelete","Return","Tab"]) {
      await commandFor("target","reset-editor"); await commandFor("sentinel","reset-editor"); await commandFor("sentinel","activate");
      const before=await read("sentinel"), targetBefore=await read("target");
      await observe();
      const contextBefore={foreground:await foreground(),pointer:screen.getCursorScreenPoint(),clipboard:await clipboardSnapshot()};
      assert.equal(contextBefore.foreground,sentinel.pid,"Fixture must own the foreground immediately before the key probe");
      const result=await action("press_key",{key,duration:0.01}); await pause(200);
      const after=await read("sentinel"), targetAfter=await read("target");
      const same=before.text===after.text&&JSON.stringify(before.selection)===JSON.stringify(after.selection)&&before.has_marked_text===after.has_marked_text&&JSON.stringify(before.marked_range)===JSON.stringify(after.marked_range)&&before.events_down.length===after.events_down.length&&before.events_up.length===after.events_up.length;
      let foregroundPID=null,foregroundError=null;
      try { foregroundPID=await foreground(); } catch(error) { foregroundError=String(error); }
      const contextAfter={foreground:foregroundPID,pointer:screen.getCursorScreenPoint(),clipboard:await clipboardSnapshot()};
      const safe=same&&foregroundPID===sentinel.pid&&JSON.stringify(contextBefore.pointer)===JSON.stringify(contextAfter.pointer)&&JSON.stringify(contextBefore.clipboard)===JSON.stringify(contextAfter.clipboard);
      cases.push({key,safe,foreground_pid:foregroundPID,foreground_error:foregroundError,context_before:contextBefore,context_after:contextAfter,native_error:result.isError?result.content[0]?.text:null,sentinel_before:before,sentinel_after:after,target_before:targetBefore,target_after:targetAfter});
      await writeFile("/tmp/aiclaw-background-keyboard-key-probe-progress.json",JSON.stringify({target_framework:electronTarget?"electron":"appkit",cases}));
    }
    const passed=cases.every(value=>value.safe);
    console.log(JSON.stringify({passed,experiment:"public-pid-key-side-effects",fixture_reset_between_cases:true,cases}));
    assert.equal(passed,true,"Some PID editing keys interfered with the foreground fixture; no general support may be claimed");
    return;
  }
  const unicode = "中文 👩🏽‍💻 e\u0301 跨分块输入Z";
  if (electronTarget && electronField === "textarea-seeded") {
    const before = await read("target"), refusal = await action("type_text", {text: unicode});
    assert.equal(refusal.isError, true); assert.match(refusal.content[0]?.text ?? "", /nonempty rich text area/);
    await pause(100); const after = await read("target");
    assert.equal(after.text, before.text); assert.equal(after.input_events.length, before.input_events.length); assert.equal(after.events_down.length, before.events_down.length);
    await unchanged();
    console.log(JSON.stringify({passed: true, target_framework: "electron-html-textarea-seeded", nonempty_rich_area_preserved: true, refused_before_key_or_ax_write: true, native_input_events: 0, trusted_dom_input_events: 0}));
    return;
  }
  const typed = await action("type_text", {text: unicode});
  if (typed.isError) console.error(JSON.stringify({native_error: typed.content[0]?.text, target: await read("target"), sentinel: await read("sentinel"), foreground: await foreground(), pointer_before: pointer, pointer_after: screen.getCursorScreenPoint(), clipboard_unchanged: JSON.stringify(await clipboardSnapshot()) === JSON.stringify(savedClipboard)}));
  assert.ok(!typed.isError, typed.content[0]?.text);
  const expectedText = target.text.slice(0, target.selection[0]) + unicode + target.text.slice(target.selection[0] + target.selection[1]);
  const inserted = await until(value => value.text === expectedText && (!electronTarget || (value.business_value === expectedText && value.input_events.some(event => event.id === "editor" && event.trusted && event.value === expectedText))), "Unicode and trusted business-input consumption");
  await unchanged("literal insertion");
  if (process.env.AICLAW_BACKGROUND_KEYBOARD_LITERAL_ONLY === "1") {
    console.log(JSON.stringify({passed:true,target_framework:electronTarget?`electron-html-${electronField}`:"appkit",literal_text_route:/AX literal/.test(typed.content[0]?.text??"")?"ax-focused-literal-insertion":"pid-unicode-keyboard",literal_value:inserted.text,literal_selection:inserted.selection,business_value:inserted.business_value,trusted_dom_input:electronTarget,surrounding_text_preserved:true,foreground_text_selection_marked_state_preserved:true,foreground_pointer_clipboard_preserved:true,sentinel_no_input:true}));
    return;
  }
  await observe();
  const left = await action("press_key", {key: "Left"}); assert.ok(!left.isError, left.content[0]?.text);
  await until(value => value.selection[0] === inserted.selection[0] - 1 && value.events_up.includes(123), "actual Left key and up");
  await unchanged("Left key");
  const refused = async (label, prepare, beforeAction = async () => {}, operation = "type_text") => {
    await prepare(); await beforeAction(); const before = await read("target");
    const result = await action(operation, operation === "type_text" ? {text: "MUST_NOT_DISPATCH"} : {key: "a"});
    assert.equal(result.isError, true, `${label} must be refused before input`);
    await pause(100); const after = await read("target");
    assert.equal(after.text, before.text); assert.equal(after.events_down.length, before.events_down.length);
    assert.equal(after.events_up.length, before.events_up.length); assert.deepEqual(after.selection, before.selection);
    assert.equal(after.other_text, before.other_text); assert.equal(after.input_events?.length ?? 0, before.input_events?.length ?? 0);
    assert.equal(after.secure_unchanged, true); return {label, refused: true, native_input_events: 0};
  };
  const negative = [];
  negative.push(await refused("query has no screenshot focus proof", () => runtime.call("query", {app: ids.target, window_id: target.window_id, text: "Background keyboard target editor", role: "AXTextArea"})));
  for (const operation of ["type_text", "press_key"]) {
    for (const mutation of ["change-value", "change-caret"]) {
      await command("reset-editor");
      negative.push(await refused(`same field ${mutation} ${operation}`, observe, () => command(mutation), operation));
    }
  }
  negative.push(await refused("focused field changed", observe, () => command("focus-other")));
  await command("focus-editor");
  negative.push(await refused("window moved", observe, () => command("move")));
  negative.push(await refused("secure field", async () => { await command("secure"); const data = await observe(); assert.equal(data.metadata.background_keyboard_ready, false); }));
  await command("focus-editor");
  await observe(); const beforeHold = await read("target");
  const held = action("press_key", {key: "Left", duration: 10});
  const heldOutcome = Promise.allSettled([held]);
  await until(value => value.events_down.length > beforeHold.events_down.length, "ten-second hold key-down");
  const stopAt = Date.now(); await runtime.close(); runtime = undefined;
  const released = await until(value => value.events_up.length > beforeHold.events_up.length, "cancelled hold key-up", 2000);
  const releaseElapsedMs = Date.now() - stopAt;
  assert.ok(releaseElapsedMs < 2000); assert.equal((await heldOutcome)[0].status, "rejected");
  runtime = await ComputerNativeRuntime.create(); await unchanged();
  negative.push(await refused("focused window changed", observe, () => command("second-window")));
  console.log(JSON.stringify({passed: true, target_framework: electronTarget ? `electron-html-${electronField}` : "appkit-nstextview", public_pid_key_route: true, literal_text_route: /AX literal/.test(typed.content[0]?.text ?? "") ? "ax-focused-literal-insertion" : "pid-unicode-keyboard", unicode_consumed: true, literal_value: inserted.text, literal_selection: inserted.selection, surrounding_text_preserved: true, trusted_dom_input: electronTarget, dom_business_value: inserted.business_value, ordinary_key_consumed: true, foreground_pointer_clipboard_preserved: true, sentinel_no_input: true, requested_hold_ms: 10000, cancellation_release_ms: releaseElapsedMs, key_down: released.events_down, key_up: released.events_up, negative}));
}).then(() => finish(0)).catch(error => { console.error(error); return finish(1); });

async function finish(code) {
  clearTimeout(watchdog);
  try { await runtime?.close(); } catch {}
  for (const child of children) if (child.exitCode === null) child.kill();
  if (temporary) {
    const previous = process.noAsar; process.noAsar = true;
    try { await rm(temporary, {recursive: true, force: true, maxRetries: 3, retryDelay: 100}); }
    finally { process.noAsar = previous; }
  }
  app.exit(code);
}
