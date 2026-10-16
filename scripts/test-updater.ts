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
  latest = "v999.0.0"; opened = []; revealed = []; pages = []; quits = 0; launchError = ""; packaged = true;
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
