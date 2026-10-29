import { strict as assert } from "node:assert";
import { createHash } from "node:crypto";
import { existsSync } from "node:fs";
import { mkdir, mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { spawnSync } from "node:child_process";
import { after, beforeEach, mock, test } from "node:test";

const originalPlatform = Object.getOwnPropertyDescriptor(process, "platform")!;
const originalFetch = globalThis.fetch;
const downloads = await mkdtemp(join(tmpdir(), "aiclaw-updater-test-"));
const installerName = "AIClaw-v999.0.0-win-x64-setup.exe";
const installerBytes = Buffer.from("MZ installer fixture");
const digest = createHash("sha256").update(installerBytes).digest("hex");
let files: Map<string, string | Buffer>, latest: string, opened: string[], revealed: string[], pages: string[], quits: number;
let launchError = "", packaged = true;
let replacements = 0, replacementScript = "", executable = join(downloads, "AIClaw.app/Contents/MacOS/AIClaw");
const executeShell = spawnSync;
mock.module("node:child_process", { namedExports: { spawn: (_command: string, args: string[]) => { replacements++; replacementScript = args[1]!; return { unref() {} }; } } });
mock.module("electron", { namedExports: {
  app: { getVersion: () => "3.7.14", get isPackaged() { return packaged; }, getPath: (name: string) => name === "exe" ? executable : downloads, quit: () => { quits++; } },
  shell: {
    openPath: async (path: string) => { opened.push(path); assert.equal(quits, 0); return launchError; },
    openExternal: async (url: string) => { pages.push(url); },
    showItemInFolder: (path: string) => { revealed.push(path); },
  },
} });
const { Updater } = await import("../apps/desktop/src/main/updater.ts");

beforeEach(() => {
  Object.defineProperty(process, "platform", { value: "win32" });
  executable = join(downloads, "AIClaw.app/Contents/MacOS/AIClaw");
  latest = "v999.0.0"; opened = []; revealed = []; pages = []; quits = 0; replacements = 0; launchError = ""; packaged = true;
  files = new Map([[installerName, installerBytes], ["SHA256SUMS.txt", `${digest}  ./${installerName}\n`]]);
  globalThis.fetch = async (input) => {
    const url = String(input);
    if (url.endsWith("/releases/latest")) return Response.json({ tag_name: latest, assets: [...files.keys()].map(name => ({ name, browser_download_url: `https://example.test/${name}` })) });
    const value = files.get(url.split("/").at(-1)!);
    return value === undefined ? new Response("Not found", { status: 404 }) : new Response(value, { headers: { "content-length": String(Buffer.byteLength(value)) } });
  };
});
after(async () => {
  Object.defineProperty(process, "platform", originalPlatform);
  globalThis.fetch = originalFetch;
  await rm(downloads, { recursive: true, force: true });
});

test("Windows downloads a verified installer and exits only after handing it to the OS", async () => {
  const updater = new Updater();
  assert.match((await updater.check()).installLabel, /安装|installer/i);
  const result = await updater.install();
  assert.equal(result.started, true);
  assert.equal(opened.length, 1);
  assert.equal(quits, 1);
  assert.deepEqual(await readFile(opened[0]!), installerBytes);
  assert.notEqual(dirname(opened[0]!), tmpdir(), "installer must use a private temporary directory");
  assert.deepEqual(pages, []);
  await rm(dirname(opened[0]!), { recursive: true, force: true });
});

test("bad or missing checksums never launch the installer or quit the app", async () => {
  for (const sums of ["0".repeat(64) + `  ${installerName}`, `${digest}  other.exe`, "missing"]) {
    if (sums === "missing") files.delete("SHA256SUMS.txt"); else files.set("SHA256SUMS.txt", sums);
    const result = await new Updater().install();
    assert.equal(result.started, false);
    assert.deepEqual(opened, []);
    assert.equal(quits, 0);
  }
  assert.equal(pages.length, 3);
});

test("a failed installer launch keeps the app running and removes the downloaded executable", async () => {
  launchError = "OS refused to launch";
  assert.equal((await new Updater().install()).started, false);
  assert.equal(quits, 0);
  assert.equal(opened.length, 1);
  assert.equal(existsSync(opened[0]!), false);
});

test("a ZIP-only release still downloads the portable package without executing or quitting", async () => {
  const zip = "AIClaw-v999.0.0-win-x64.zip";
  files = new Map([[zip, "portable fixture"]]);
  const updater = new Updater();
  assert.doesNotMatch((await updater.check()).installLabel, /installer|安装/);
  assert.equal((await updater.install()).started, false);
  assert.equal(await readFile(join(downloads, zip), "utf8"), "portable fixture");
  assert.deepEqual(revealed, [join(downloads, zip)]);
  assert.deepEqual(opened, []);
  assert.equal(quits, 0);
  await rm(join(tmpdir(), zip), { force: true });
});

test("no update, development mode and invalid tags cannot start an installer", async () => {
  latest = "v3.7.14";
  assert.equal((await new Updater().install()).started, false);
  latest = "v999.0.0/../../run";
  assert.equal((await new Updater().install()).started, false);
  packaged = false;
  latest = "v999.0.0";
  assert.equal((await new Updater().install()).started, false);
  assert.deepEqual(opened, []);
  assert.equal(quits, 0);
});

test("a release API failure keeps the app running", async () => {
  globalThis.fetch = async () => new Response("unavailable", { status: 503 });
  assert.equal((await new Updater().install()).started, false);
  assert.equal(quits, 0);
  assert.deepEqual(opened, []);
});

const macName = `AIClaw-v999.0.0-mac-${process.arch === "arm64" ? "arm64" : "x64"}.zip`;

test("macOS foreground install joins background download and duplicate installs share one operation", async () => {
  Object.defineProperty(process, "platform", { value: "darwin" });
  files = new Map([[macName, "zip fixture"]]);
  const fetchFixture = globalThis.fetch;
  let requests = 0, release!: () => void, receiving!: () => void;
  const ready = new Promise<void>(resolve => { receiving = resolve; });
  globalThis.fetch = async (input, options) => {
    if (String(input).endsWith("/releases/latest")) return fetchFixture(input, options);
    requests++;
    return new Response(new ReadableStream({ start(controller) {
      release = () => { controller.enqueue(new TextEncoder().encode("zip fixture")); controller.close(); };
    } }), { headers: { "content-length": "11" } });
  };
  const updater = new Updater();
  const background = updater.prepare((_received, total) => { if (total === 11) receiving(); });
  await ready;
  const phases: string[] = [];
  const install = updater.install((_received, _total, phase) => { if (phase) phases.push(phase); });
  assert.equal(updater.install(), install);
  // Allow release lookup to finish while the shared stream remains blocked.
  await new Promise(resolve => setImmediate(resolve));
  release();
  const prepared = await background;
  assert.equal((await install).started, true);
  assert.equal(requests, 1);
  assert.equal(replacements, 1);
  assert.equal(quits, 1);
  assert.ok(phases.includes("downloading"));
  assert.equal(phases.at(-1), "installing");
  assert.equal(await readFile(prepared.detail, "utf8"), "zip fixture");
  await rm(dirname(prepared.detail), { recursive: true, force: true });
});

test("stalled response headers time out and a later attempt can succeed", async t => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  files = new Map([[installerName, installerBytes], ["SHA256SUMS.txt", `${digest}  ${installerName}`]]);
  const fetchFixture = globalThis.fetch;
  let waiting!: () => void;
  const started = new Promise<void>(resolve => { waiting = resolve; });
  let signal: AbortSignal | null | undefined;
  globalThis.fetch = async (input, options) => {
    if (String(input).endsWith("/releases/latest")) return fetchFixture(input, options);
    signal = options?.signal;
    return new Promise((_resolve, reject) => {
      signal!.addEventListener("abort", () => reject(signal!.reason), { once: true });
      waiting();
    });
  };
  const logs: string[] = [];
  const updater = new Updater(line => logs.push(line));
  const attempt = updater.install();
  await started;
  t.mock.timers.tick(30_001);
  const result = await attempt;
  assert.equal(result.started, false);
  assert.match(result.detail, /30/);
  assert.equal(signal?.aborted, true);
  assert.equal(quits, 0);
  assert.ok(logs.some(line => line.includes("Download failed")));
  globalThis.fetch = fetchFixture;
  assert.equal((await updater.install()).started, true, "failed operation must release its lock for retry");
  await rm(dirname(opened[0]!), { recursive: true, force: true });
});

test("a mid-stream stall aborts, while unknown-length downloads still report received bytes", async t => {
  Object.defineProperty(process, "platform", { value: "darwin" });
  files = new Map([[macName, "fixture"]]);
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const fetchFixture = globalThis.fetch;
  let receiving!: () => void;
  const started = new Promise<void>(resolve => { receiving = resolve; });
  globalThis.fetch = async (input, options) => {
    if (String(input).endsWith("/releases/latest")) return fetchFixture(input, options);
    return new Response(new ReadableStream({ start(controller) {
      controller.enqueue(new TextEncoder().encode("first chunk"));
      options!.signal!.addEventListener("abort", () => controller.error(options!.signal!.reason), { once: true });
    } }));
  };
  const updater = new Updater();
  const pending = updater.prepare((received, total) => { if (received > 0) { assert.equal(total, 0); receiving(); } });
  await started;
  t.mock.timers.tick(29_999);
  t.mock.timers.tick(2);
  const result = await pending;
  assert.equal(result.ready, false);
  assert.match(result.detail, /30/);
  assert.equal(quits, 0);
  globalThis.fetch = fetchFixture;
  const retry = await updater.prepare();
  assert.equal(retry.ready, true);
  assert.equal(await readFile(retry.detail, "utf8"), "fixture");
  await rm(dirname(retry.detail), { recursive: true, force: true });
});

test("a short download is rejected before replacement or application exit", async () => {
  Object.defineProperty(process, "platform", { value: "darwin" });
  files = new Map([[macName, "fixture"]]);
  const fetchFixture = globalThis.fetch;
  globalThis.fetch = async (input, options) => String(input).endsWith("/releases/latest") ? fetchFixture(input, options)
    : new Response("partial", { headers: { "content-length": "100" } });
  assert.equal((await new Updater().install()).started, false);
  assert.equal(replacements, 0);
  assert.equal(quits, 0);
});


test("macOS replacement preserves the previous application across installation failures", { skip: originalPlatform.value === "win32" }, async t => {
  for (const failure of ["success", "extract", "validate", "stage", "backup", "install", "launch", "rollback"]) {
    await t.test(failure, async () => {
      const root = await mkdtemp(join(tmpdir(), "aiclaw-replacement-test-"));
      try {
        const parent = join(root, "Apps ' personal"), bundle = join(parent, "AIClaw.app"), bin = join(root, "bin"), source = join(root, "source.app");
        executable = join(bundle, "Contents/MacOS/AIClaw");
        await mkdir(dirname(executable), { recursive: true });
        await mkdir(join(source, "Contents/MacOS"), { recursive: true });
        await mkdir(bin);
        await writeFile(join(bundle, "OLD_VERSION"), "old fixture");
        await writeFile(executable, "old executable", { mode: 0o700 });
        await writeFile(join(source, "NEW_VERSION"), "new fixture");
        await writeFile(join(source, "Contents/Info.plist"), "fixture plist");
        if (failure !== "validate") await writeFile(join(source, "Contents/MacOS/AIClaw"), "new executable", { mode: 0o700 });
        const commands = {
          ditto: 'if [ "$1" = "-x" ]; then [ "$FAILURE" != "extract" ] || exit 72; cp -R "$FIXTURE_SOURCE" "$4/AIClaw.app"; else [ "$FAILURE" != "stage" ] || exit 71; cp -R "$1" "$2"; fi',
          mv: 'case "$2" in */previous.app) [ "$FAILURE" != "backup" ] || exit 74;; esac; case "$1" in */new.app) [ "$FAILURE" != "install" ] && [ "$FAILURE" != "rollback" ] || exit 73;; */previous.app) [ "$FAILURE" != "rollback" ] || exit 75;; esac; /bin/mv "$@"',
          xattr: 'exit 0',
          open: '[ "$FAILURE" != "launch" ] || [ ! -f "$1/NEW_VERSION" ] || exit 76; printf "%s\\n" "$1" >> "$OPEN_LOG"',
        };
        for (const [name, body] of Object.entries(commands)) await writeFile(join(bin, name), "#!/bin/sh\n" + body + "\n", { mode: 0o700 });
        (new Updater() as unknown as { replaceAndRelaunch(zip: string): void }).replaceAndRelaunch(join(root, "update's.zip"));
        assert.ok(replacementScript.includes("trap cleanup EXIT"));
        const script = replacementScript.split("\n").filter(line => !line.startsWith("while kill -0 ")).join("\n");
        const result = executeShell("/bin/bash", ["-c", script], { encoding: "utf8", env: { ...process.env, PATH: bin + ":/usr/bin:/bin", FAILURE: failure, FIXTURE_SOURCE: source, OPEN_LOG: join(root, "opened") } });
        const stages = (await readdir(parent)).filter(name => name.startsWith(".aiclaw-update-"));
        if (failure === "success") {
          assert.equal(result.status, 0, result.stderr);
          assert.equal(existsSync(join(bundle, "NEW_VERSION")), true);
          assert.equal(existsSync(join(bundle, "OLD_VERSION")), false);
          assert.deepEqual(stages, []);
        } else if (failure === "rollback") {
          assert.notEqual(result.status, 0);
          assert.equal(stages.length, 1);
          assert.equal(existsSync(join(parent, stages[0]!, "previous.app/OLD_VERSION")), true, "a failed recovery must keep the backup");
          assert.match(result.stdout, /previous application preserved/);
        } else {
          assert.notEqual(result.status, 0);
          assert.equal(existsSync(join(bundle, "OLD_VERSION")), true, result.stdout + result.stderr);
          assert.equal(existsSync(join(bundle, "NEW_VERSION")), false);
          assert.deepEqual(stages, []);
        }
        if (failure !== "rollback") assert.equal((await readFile(join(root, "opened"), "utf8")).trim(), bundle);
      } finally { await rm(root, { recursive: true, force: true }); }
    });
  }
});
