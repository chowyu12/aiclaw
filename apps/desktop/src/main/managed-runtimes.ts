import { execFile } from "node:child_process";
import { createHash } from "node:crypto";
import { copyFile, mkdir, mkdtemp, open, readFile, rename, rm, writeFile } from "node:fs/promises";
import { delimiter, join } from "node:path";
import { promisify } from "node:util";
const RETRY_INTERVAL = 30 * 60 * 1000;

const execute = promisify(execFile);
export type RuntimeArtifact = { name: "node" | "python"; version: string; url: string; sha256: string; root: string; binary: string };
type Run = (file: string, args: string[], env: NodeJS.ProcessEnv, signal?: AbortSignal) => Promise<void>;
const run: Run = async (file, args, env, signal) => { await execute(file, args, { env, signal, windowsHide: true, timeout: 120_000, maxBuffer: 1024 * 1024 }); };

/** 固定发行版与上游校验值，随应用更新；不在用户机器上追踪 latest。 */
export function runtimeArtifacts(platform: NodeJS.Platform, arch: string): RuntimeArtifact[] {
  const nodeVersion = "24.22.2", pythonVersion = "3.12.16+20261026";
  const target = `${platform}-${arch}`;
  const hashes: Record<string, [string, string, string]> = {
    "darwin-arm64": ["aa9549349e47dffaa42dcf95d6e06ade8c4bdafc980979883b88767670df385a", "353d5f1b4fd2715e7053f12f068435d5bbad16b91e057006b5d01e0925709a7e", "aarch64-apple-darwin"],
    "darwin-x64": ["d5e336d588c45be0c37dbd52cc29895593009ca4997a77a1fd02815131ed0e52", "37c38fda76fec1fbf310adcbafb7f0d96af13b5511bf8ea954c3aa8eec185734", "x86_64-apple-darwin"],
    "win32-x64": ["ca1bff5d8ead13c9f2ea404a176db46bd430704d5b5113734049fa365e7799e2", "1f2ab53880fc67ea323398c6388e05af28addc99a8e389209f08a51fdff4b149", "x86_64-pc-windows-msvc"],
    "linux-x64": ["361718c444e49af2ca5a110c83ba6a8901143f23ab285ebbef0341f391f482a9", "ce4f6d0a6cfdb3bed6e44c2f9affd85943c76a42e25ad3a259002f31d6fb8a20", "x86_64-unknown-linux-gnu"],
  };
  const entry = hashes[target];
  if (!entry) return [];
  const [nodeHash, pythonHash, pythonTarget] = entry;
  const windows = platform === "win32";
  const nodeRoot = `node-v${nodeVersion}-${windows ? "win" : platform}-${arch}`;
  return [
    { name: "node", version: nodeVersion, root: nodeRoot, binary: windows ? "node.exe" : "bin/node",
      url: `https://nodejs.org/dist/v${nodeVersion}/${nodeRoot}.${windows ? "zip" : "tar.gz"}`, sha256: nodeHash },
    { name: "python", version: pythonVersion, root: "python", binary: windows ? "python.exe" : "bin/python3",
      url: `https://github.com/astral-sh/python-build-standalone/releases/download/20261026/cpython-${encodeURIComponent(pythonVersion)}-${pythonTarget}-install_only_stripped.tar.gz`, sha256: pythonHash },
  ];
}

type InstallOptions = { platform: NodeJS.Platform; env: NodeJS.ProcessEnv; fetch?: typeof fetch; run?: Run; signal?: AbortSignal };

/** 下载只写临时目录，校验和可执行检查通过后才发布。失败不留下半份可用环境。 */
export async function installRuntime(artifact: RuntimeArtifact, target: string, options: InstallOptions): Promise<void> {
  const runner = options.run ?? run;
  try {
    const saved = JSON.parse(await readFile(join(target, ".aiclaw-runtime.json"), "utf8"));
    if (saved.sha256 === artifact.sha256 && saved.version === artifact.version) {
      await runner(join(target, artifact.binary), ["--version"], options.env, options.signal);
      return;
    }
  } catch { /* 缺失或损坏的应用托管副本重新下载；不修改系统环境。 */ }
  const parent = join(target, "..");
  await mkdir(parent, { recursive: true });
  const staging = await mkdtemp(join(parent, ".download-"));
  try {
    const archive = join(staging, artifact.url.endsWith(".zip") ? "runtime.zip" : "runtime.tar.gz");
    const response = await (options.fetch ?? fetch)(artifact.url, { signal: AbortSignal.any([AbortSignal.timeout(300_000), ...(options.signal ? [options.signal] : [])]) });
    if (!response.ok || !response.body) throw Error("运行时下载失败");
    const file = await open(archive, "wx", 0o600), hash = createHash("sha256");
    const reader = response.body.getReader();
    let size = 0;
    try {
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        size += value.length;
        if (size > 192 * 1024 * 1024) throw Error("运行时下载超出大小限制");
        hash.update(value);
        await file.writeFile(value);
      }
    } finally { await Promise.allSettled([reader.cancel(), file.close()]); }
    if (hash.digest("hex") !== artifact.sha256) throw Error("运行时校验失败");
    const extracted = join(staging, "extracted");
    await mkdir(extracted);
    if (options.platform === "win32" && archive.endsWith(".zip")) {
      const quote = (value: string) => `'${value.replaceAll("'", "''")}'`;
      await runner(join(options.env.SystemRoot || "C:\\Windows", "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), ["-NoProfile", "-NonInteractive", "-Command", `Expand-Archive -LiteralPath ${quote(archive)} -DestinationPath ${quote(extracted)} -ErrorAction Stop`], options.env, options.signal);
    } else {
      const tar = options.platform === "win32" ? join(options.env.SystemRoot || "C:\\Windows", "System32", "tar.exe") : "/usr/bin/tar";
      await runner(tar, ["-xzf", archive, "-C", extracted], options.env, options.signal);
    }
    const payload = join(extracted, artifact.root);
    await runner(join(payload, artifact.binary), ["--version"], options.env, options.signal);
    await writeFile(join(payload, ".aiclaw-runtime.json"), JSON.stringify({ sha256: artifact.sha256, version: artifact.version }));
    options.signal?.throwIfAborted();
    // 仅移除校验失败的应用托管目录；系统安装位置从不进入这条路径。
    await rm(target, { recursive: true, force: true });
    await rename(payload, target);
  } finally { await rm(staging, { recursive: true, force: true }); }
}

/** PATH 在内核出生前就加入固定目录，下载完成后已运行的内核也能找到它们。 */
export class ManagedRuntimes {
  private readonly baseEnv: NodeJS.ProcessEnv;
  private readonly platform: NodeJS.Platform;
  private readonly artifacts: RuntimeArtifact[];
  private readonly root: string;
  private preparing?: Promise<void>;
  private timer?: ReturnType<typeof setInterval>;
  private controller?: AbortController;
  private ready = new Set<string>();
  private readonly report: (message: string) => void;
  private readonly installer: typeof installRuntime;
  private readonly runner: Run;

  constructor(options: { root: string; platform?: NodeJS.Platform; arch?: string; env?: NodeJS.ProcessEnv; report?: (message: string) => void; install?: typeof installRuntime; run?: Run }) {
    this.baseEnv = { ...(options.env ?? process.env) };
    this.platform = options.platform ?? process.platform;
    this.artifacts = runtimeArtifacts(this.platform, options.arch ?? process.arch);
    this.root = options.root;
    this.report = options.report ?? (() => {});
    this.installer = options.install ?? installRuntime;
    this.runner = options.run ?? run;
  }

  /** 只改变传给工具子进程的副本，保留登录 shell 的 PATH 和系统配置。 */
  environment(base: NodeJS.ProcessEnv = this.baseEnv): NodeJS.ProcessEnv {
    const env = { ...base };
    if (!this.artifacts.length) return env;
    const windows = this.platform === "win32";
    const paths = this.artifacts.map(artifact => artifact.name === "node" ? join(this.target(artifact), windows ? "" : "bin") : join(this.pythonEnv(artifact), windows ? "Scripts" : "bin"));
    const key = Object.keys(env).find(key => key.toLowerCase() === "path") ?? "PATH";
    const separator = windows ? ";" : delimiter;
    const npmPrefix = join(this.root, "npm");
    env[key] = [...paths, windows ? npmPrefix : join(npmPrefix, "bin"), env[key] ?? ""].join(separator);
    // Windows 的子进程环境变量不区分大小写，避免继承的大写 npm 配置抢先匹配。
    for (const key of Object.keys(env)) {
      if (["npm_config_prefix", "npm_config_cache"].includes(key.toLowerCase())) delete env[key];
    }
    env.npm_config_prefix = npmPrefix;
    env.npm_config_cache = join(this.root, "cache", "npm");
    env.PIP_CACHE_DIR = join(this.root, "cache", "pip");
    env.PYTHONUTF8 = "1";
    env.PYTHONNOUSERSITE = "1";
    const python = this.artifacts.find(artifact => artifact.name === "python")!;
    env.VIRTUAL_ENV = this.pythonEnv(python);
    delete env.PYTHONHOME;
    delete env.PYTHONPATH;
    return env;
  }

  private target(artifact: RuntimeArtifact): string { return join(this.root, `${artifact.name}-${artifact.version}`); }
  private pythonEnv(artifact: RuntimeArtifact): string { return join(this.root, `python-env-${artifact.version}`); }

  start(): void {
    if (this.timer !== undefined) return;
    void this.prepare();
    this.timer = setInterval(() => { void this.prepare(); }, RETRY_INTERVAL);
    this.timer.unref?.();
  }

  stop(): void {
    if (this.timer !== undefined) clearInterval(this.timer);
    this.timer = undefined;
    this.controller?.abort();
  }

  prepare(): Promise<void> {
    if (this.preparing) return this.preparing;
    this.controller = new AbortController();
    const signal = this.controller.signal;
    const env = this.environment();
    this.preparing = Promise.all(this.artifacts.map(async artifact => {
      if (this.ready.has(artifact.name)) return;
      this.report(`${artifact.name}：正在后台准备运行环境`);
      try {
        await this.installer(artifact, this.target(artifact), { platform: this.platform, env, signal });
        signal.throwIfAborted();
        if (artifact.name === "python") {
          const venv = this.pythonEnv(artifact), bin = join(venv, this.platform === "win32" ? "Scripts" : "bin");
          const python = join(bin, this.platform === "win32" ? "python.exe" : "python");
          try { await this.runner(python, ["-m", "pip", "--version"], env, signal); }
          catch {
            await this.runner(join(this.target(artifact), artifact.binary), ["-m", "venv", venv], env, signal);
            await this.runner(python, ["-m", "pip", "--version"], env, signal);
          }
          if (this.platform === "win32") {
            const alias = join(bin, "python3.exe");
            try { await this.runner(alias, ["--version"], env, signal); }
            catch { await copyFile(python, alias); await this.runner(alias, ["--version"], env, signal); }
          }
        }
        signal.throwIfAborted();
        this.ready.add(artifact.name);
        this.report(`${artifact.name}：运行环境已就绪`);
      } catch { this.report(`${artifact.name}：后台准备未完成，后续自动重试`); }
    })).then(() => {}).finally(() => { this.preparing = undefined; });
    return this.preparing;
  }
}
