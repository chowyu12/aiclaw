import { strict as assert } from "node:assert";
import { createHash } from "node:crypto";
import { existsSync } from "node:fs";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { after, beforeEach, mock, test } from "node:test";

const originalPlatform = Object.getOwnPropertyDescriptor(process, "platform")!;
const originalFetch = globalThis.fetch;
const downloads = await mkdtemp(join(tmpdir(), "aiclaw-updater-test-"));
const installerName = "AIClaw-v999.0.0-win-x64-setup.exe";
const installerBytes = Buffer.from("MZ installer fixture");
const digest = createHash("sha256").update(installerBytes).digest("hex");
let files: Map<string, string | Buffer>, latest: string, opened: string[], revealed: string[], pages: string[], quits: number;
let launchError = "", packaged = true;
let replacements = 0;
mock.module("node:child_process", { namedExports: { spawn: () => { replacements++; return { unref() {} }; } } });
mock.module("electron", { namedExports: {
  app: { getVersion: () => "3.7.14", get isPackaged() { return packaged; }, getPath: () => downloads, quit: () => { quits++; } },
  shell: {
    openPath: async (path: string) => { opened.push(path); assert.equal(quits, 0); return launchError; },
    openExternal: async (url: string) => { pages.push(url); },
    showItemInFolder: (path: string) => { revealed.push(path); },
  },
} });
const { Updater } = await import("../apps/desktop/src/main/updater.ts");

beforeEach(() => {
  Object.defineProperty(process, "platform", { value: "win32" });
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
