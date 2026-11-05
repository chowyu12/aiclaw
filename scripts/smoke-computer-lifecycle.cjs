/** Real native IPC lifecycle acceptance against an owned temporary AppKit fixture.
 * No Accessibility/Screen Recording grants, screenshots or input are required.
 */
const { app } = require("electron");
const { execFile } = require("node:child_process");
const { promisify } = require("node:util");
const { mkdtemp, mkdir, rm, writeFile } = require("node:fs/promises");
const { tmpdir } = require("node:os");
const path = require("node:path");
const { pathToFileURL } = require("node:url");
const assert = require("node:assert/strict");
const run = promisify(execFile);
app.setName("aiclaw-native-lifecycle-acceptance");
app.setPath("userData", path.join(tmpdir(), "aiclaw-native-lifecycle-acceptance"));
let runtime, temporary, target;
const watchdog = setTimeout(() => { console.error("Native lifecycle acceptance timed out"); app.exit(1); }, 60_000);
app.whenReady().then(async () => {
  if (process.platform !== "darwin") return;
  const { ComputerNativeRuntime } = await import(pathToFileURL(path.join(__dirname, "../apps/desktop/dist/main/computer-runtime.js")));
  runtime = await ComputerNativeRuntime.create();
  temporary = await mkdtemp(path.join(tmpdir(), "aiclaw-native-lifecycle-"));
  const bundle = path.join(temporary, "AIClaw Lifecycle Fixture.app");
  const contents = path.join(bundle, "Contents");
  await mkdir(path.join(contents, "MacOS"), { recursive: true });
  target = `com.aiclaw.lifecyclefixture.t${process.pid}`;
  await writeFile(path.join(contents, "Info.plist"), `<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>${target}</string><key>CFBundleExecutable</key><string>Fixture</string><key>CFBundleName</key><string>AIClaw Lifecycle Fixture</string><key>CFBundlePackageType</key><string>APPL</string><key>LSUIElement</key><true/></dict></plist>`);
  await run("swiftc", [path.join(__dirname, "fixtures/computer-macos.swift"), "-o", path.join(contents, "MacOS/Fixture")], { timeout: 30_000 });
  await run("/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister", ["-f", bundle]);
  await run("/usr/bin/open", ["-g", bundle, "--args", path.join(temporary, "receipt.json")]);
  const call = async (name, args) => {
    const result = await runtime.call(name, args);
    if (result.isError) throw Error(result.content[0].text);
    return JSON.parse(result.content[0].text);
  };
  const found = await call("find_apps", { text: target });
  assert.ok(found.apps.some(item => item.id === target), "native discovery did not find the registered fixture");
  const stopped = await call("stop_app", { app: target });
  assert.equal(stopped.verified, true, "normal quit was not verified");
  const started = await call("start_app", { app: target });
  assert.equal(started.verified, true, "LaunchServices start was not verified");
  const modified = await call("modify_app", { app: target, modification: "activate" });
  assert.equal(modified.accepted, true);
  assert.equal((await call("stop_app", { app: target })).verified, true);
  console.log("PASS: native discovery, normal quit, LaunchServices restart and activation (temporary fixture only; no screenshots or input).");
}).then(() => finish(0)).catch(async error => { console.error(error); await finish(1); });
async function finish(code) {
  clearTimeout(watchdog);
  try {
    if (runtime && target) await runtime.call("stop_app", { app: target }).catch(() => {});
    if (runtime) await runtime.close();
    if (temporary) {
      await run("/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister", ["-u", path.join(temporary, "AIClaw Lifecycle Fixture.app")]).catch(() => {});
      await rm(temporary, { recursive: true, force: true });
    }
  } finally { app.exit(code); }
}
