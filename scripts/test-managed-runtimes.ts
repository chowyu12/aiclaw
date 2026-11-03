import test from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { access, mkdir, mkdtemp, readdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { installRuntime, ManagedRuntimes, runtimeArtifacts, type RuntimeArtifact } from "../apps/desktop/src/main/managed-runtimes.ts";

const archive = Buffer.from("verified archive fixture");
const spec: RuntimeArtifact = { name: "node", version: "fixture", url: "https://nodejs.org/dist/fixture.tar.gz", sha256: createHash("sha256").update(archive).digest("hex"), root: "fixture", binary: "bin/node" };
const fetcher = (async () => new Response(archive)) as typeof fetch;

test("四个平台固定公开发行版和 SHA256，启动不追踪 latest", () => {
  for (const [platform, arch] of [["darwin", "arm64"], ["darwin", "x64"], ["win32", "x64"], ["linux", "x64"]] as const) {
    const artifacts = runtimeArtifacts(platform, arch);
    assert.deepEqual(artifacts.map(item => item.name), ["node", "python"]);
    assert.equal(new URL(artifacts[0]!.url).origin, "https://nodejs.org");
    assert.equal(new URL(artifacts[1]!.url).origin, "https://github.com");
    for (const item of artifacts) {
      assert.match(item.sha256, /^[a-f0-9]{64}$/);
      assert.ok(!item.url.includes("latest"));
    }
  }
  assert.deepEqual(runtimeArtifacts("linux", "arm64"), []);
});

test("校验并试运行后发布，下一次离线复用而不重新下载", async () => {
  const home = await mkdtemp(join(tmpdir(), "aiclaw-runtime-")), target = join(home, "node");
  let downloads = 0, extracts = 0;
  const options = { platform: "darwin" as const, env: {}, fetch: (async () => { downloads++; return fetcher(""); }) as typeof fetch,
    run: async (file: string, args: string[]) => {
      if (file === "/usr/bin/tar") {
        extracts++;
        assert.deepEqual(await readFile(args[1]!), archive);
        const bin = join(args[3]!, spec.root, "bin");
        await mkdir(bin, { recursive: true }); await writeFile(join(bin, "node"), "fixture");
      } else { assert.deepEqual(args, ["--version"]); await access(file); }
    } };
  try {
    await installRuntime(spec, target, options);
    assert.equal(JSON.parse(await readFile(join(target, ".aiclaw-runtime.json"), "utf8")).sha256, spec.sha256);
    await installRuntime(spec, target, options);
    assert.equal(downloads, 1); assert.equal(extracts, 1);
    assert.deepEqual(await readdir(home), ["node"]);
  } finally { await rm(home, { recursive: true, force: true }); }
});

for (const failure of ["checksum", "download", "extract", "executable"] as const) test(`${failure} 失败不留下半份可用环境或临时文件`, async () => {
  const home = await mkdtemp(join(tmpdir(), "aiclaw-runtime-bad-")), target = join(home, "node");
  let extracted = false;
  try {
    await assert.rejects(installRuntime(spec, target, { platform: "darwin", env: {},
      fetch: (async () => failure === "download" ? new Response("", { status: 503 }) : new Response(failure === "checksum" ? "corrupt" : archive)) as typeof fetch,
      run: async (file, args) => {
        if (file === "/usr/bin/tar") {
          extracted = true;
          if (failure === "extract") throw Error("failed extraction");
          await mkdir(join(args[3]!, spec.root), { recursive: true });
        } else throw Error("not executable");
      },
    }));
    if (failure === "checksum" || failure === "download") assert.equal(extracted, false);
    assert.deepEqual(await readdir(home), []);
  } finally { await rm(home, { recursive: true, force: true }); }
});

test("退出中止下载，不落盘未完成安装", async () => {
  const home = await mkdtemp(join(tmpdir(), "aiclaw-runtime-abort-"));
  const controller = new AbortController();
  let downloading!: () => void;
  const entered = new Promise<void>(resolve => { downloading = resolve; });
  try {
    const preparing = installRuntime(spec, join(home, "node"), { platform: "darwin", env: {}, signal: controller.signal,
      fetch: (async (_url, options) => new Response(new ReadableStream({ start(stream) {
        stream.enqueue(new Uint8Array([1])); downloading();
        options?.signal?.addEventListener("abort", () => stream.error(Error("aborted")), { once: true });
      } }))) as typeof fetch });
    await entered; controller.abort();
    await assert.rejects(preparing);
    assert.deepEqual(await readdir(home), []);
  } finally { await rm(home, { recursive: true, force: true }); }
});

test("Windows ZIP 解压路径按 PowerShell 字符串转义，文件不走安装程序", async () => {
  const home = await mkdtemp(join(tmpdir(), "aiclaw-runtime-'quote-"));
  const windowsSpec = { ...spec, url: "https://nodejs.org/dist/fixture.zip", binary: "node.exe" };
  let command = "";
  try {
    await installRuntime(windowsSpec, join(home, "node"), { platform: "win32", env: { SystemRoot: "C:\\Windows" }, fetch: fetcher,
      run: async (file, args) => {
        if (file.endsWith("powershell.exe")) {
          command = args.at(-1)!;
          const staging = (await readdir(home)).find(name => name.startsWith(".download-"))!;
          const root = join(home, staging, "extracted", spec.root);
          await mkdir(root, { recursive: true }); await writeFile(join(root, "node.exe"), "fixture");
        } else await access(file);
      } });
    assert.ok(command.includes("''quote-"));
    assert.ok(command.includes("-LiteralPath"));
  } finally { await rm(home, { recursive: true, force: true }); }
});

test("后台准备合并并发、独立失败重试、保留用户环境和已装依赖", async () => {
  const home = await mkdtemp(join(tmpdir(), "aiclaw-runtime-manager-"));
  const env = { PATH: "/user/bin", EXISTING: "keep" } as NodeJS.ProcessEnv;
  const calls: string[] = [], reports: string[] = [];
  let release!: () => void, entered!: () => void, failNode = true, venvReady = false;
  const gate = new Promise<void>(resolve => { release = resolve; });
  const downloading = new Promise<void>(resolve => { entered = resolve; });
  const manager = new ManagedRuntimes({ root: join(home, "runtimes"), platform: "darwin", arch: "arm64", env, report: message => reports.push(message),
    install: async artifact => { calls.push(artifact.name); entered(); await gate; if (artifact.name === "node" && failNode) throw Error("network"); },
    run: async (_file, args) => {
      if (args.includes("venv")) venvReady = true;
      else if (!venvReady) throw Error("missing venv");
    },
  });
  try {
    const managedEnv = manager.environment();
    const inheritedPath = managedEnv.PATH;
    const first = manager.prepare();
    assert.equal(manager.prepare(), first);
    await downloading;
    assert.equal(manager.environment().PATH, inheritedPath, "PATH 在下载之前准备，内核无需重启");
    assert.equal(env.PATH, "/user/bin", "不修改父进程环境");
    assert.ok(managedEnv.PATH?.includes("python-env-")); assert.ok(managedEnv.PATH?.includes("node-"));
    assert.equal(env.EXISTING, "keep"); assert.ok(managedEnv.PATH?.endsWith("/user/bin"));
    assert.equal(managedEnv.npm_config_prefix, join(home, "runtimes", "npm"));
    release(); await first;
    failNode = false; await manager.prepare(); await manager.prepare();
    assert.deepEqual(calls, ["node", "python", "node"]);
    assert.ok(reports.some(report => report.includes("自动重试")));
    assert.ok(venvReady);
  } finally { release(); manager.stop(); await rm(home, { recursive: true, force: true }); }
});

test("Windows 提供 python 和 python3，重启后保留虚拟环境的额外依赖", async () => {
  const home = await mkdtemp(join(tmpdir(), "aiclaw-runtime-win-"));
  const env = { Path: "C:\\UserTools", NPM_CONFIG_PREFIX: "C:\\SystemNpm" } as NodeJS.ProcessEnv;
  let creations = 0;
  const options = { root: join(home, "runtimes"), platform: "win32" as const, arch: "x64", env,
    install: async () => {},
    run: async (file: string, args: string[]) => {
      if (args.includes("venv")) {
        creations++;
        const bin = join(args.at(-1)!, "Scripts");
        await mkdir(bin, { recursive: true }); await writeFile(join(bin, "python.exe"), "venv launcher");
        await writeFile(join(args.at(-1)!, "user-dependency"), "keep");
      } else await access(file);
    },
  };
  try {
    const manager = new ManagedRuntimes(options);
    await manager.prepare();
    const managedEnv = manager.environment();
    const bin = managedEnv.Path!.split(";").find(path => path.includes("python-env-"))!;
    assert.equal(await readFile(join(bin, "python3.exe"), "utf8"), "venv launcher");
    await new ManagedRuntimes({ ...options, env: { Path: "C:\\UserTools" } }).prepare();
    assert.equal(creations, 1);
    assert.equal(await readFile(join(bin, "..", "user-dependency"), "utf8"), "keep");
    assert.ok(managedEnv.Path!.endsWith(";C:\\UserTools"));
    assert.equal(managedEnv.PATH, undefined, "保留 Windows 已有的 Path 键名");
    assert.equal(managedEnv.NPM_CONFIG_PREFIX, undefined);
    assert.equal(managedEnv.npm_config_prefix, join(home, "runtimes", "npm"));
  } finally { await rm(home, { recursive: true, force: true }); }
});

test("半小时仅重试未完成的环境，退出清理定时器并中止准备", async () => {
  const home = await mkdtemp(join(tmpdir(), "aiclaw-runtime-timer-"));
  const set = globalThis.setInterval, clear = globalThis.clearInterval;
  const timer = {} as ReturnType<typeof setInterval>;
  let scheduled = 0, cleared = false, aborted = false;
  globalThis.setInterval = ((_callback: () => void, delay: number) => { scheduled++; assert.equal(delay, 30 * 60 * 1000); return timer; }) as typeof setInterval;
  globalThis.clearInterval = ((id: ReturnType<typeof setInterval>) => { cleared = id === timer; }) as typeof clearInterval;
  const manager = new ManagedRuntimes({ root: join(home, "runtimes"), platform: "darwin", arch: "arm64", env: {},
    install: async (_artifact, _target, options) => new Promise((_resolve, reject) => {
      options.signal!.addEventListener("abort", () => { aborted = true; reject(Error("aborted")); }, { once: true });
    }),
  });
  try {
    manager.start(); manager.start(); const pending = manager.prepare();
    manager.stop(); await pending;
    assert.equal(scheduled, 1); assert.ok(cleared); assert.ok(aborted);
  } finally { manager.stop(); globalThis.setInterval = set; globalThis.clearInterval = clear; await rm(home, { recursive: true, force: true }); }
});
