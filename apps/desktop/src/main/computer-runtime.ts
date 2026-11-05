import { execFile, spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { createRequire } from "node:module";
import { createHash } from "node:crypto";
import { mkdtemp, readFile, realpath, rm, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { tmpdir } from "node:os";
import { promisify } from "node:util";
const run = promisify(execFile);

export const COMPUTER_USE_VERSION = "1.1.3";

export function computerNamespace(executable: string): string {
  // Upstream replaces an agent when its bundle path differs. Development,
  // installed apps and package acceptance must never share that agent socket.
  return `aiclaw-${createHash("sha256").update(`${COMPUTER_USE_VERSION}\0${executable}`).digest("hex").slice(0, 24)}`;
}
export interface ComputerTool { name: string; inputSchema: { properties: Record<string, unknown>; required?: string[] } }
export interface ComputerToolResult {
  content: Array<{ type: string; text?: string; data?: string; mimeType?: string }>;
  isError?: boolean;
}
export interface ComputerRuntime {
  tools: ComputerTool[];
  call(name: string, args: Record<string, unknown>): Promise<ComputerToolResult>;
  close(): Promise<void>;
}

export function computerExecutable(root: string, platform = process.platform, arch = process.arch): string {
  if (!["arm64", "x64"].includes(arch)) throw Error(`Unsupported computer architecture: ${arch}`);
  if (platform === "darwin") return join(root, "dist", "Open Computer Use.app", "Contents", "MacOS", "OpenComputerUse");
  if (platform === "linux" || platform === "win32") {
    return join(root, "dist", platform === "win32" ? "windows" : "linux", arch === "x64" ? "amd64" : arch, platform === "win32" ? "open-computer-use.exe" : "open-computer-use");
  }
  throw Error(`Unsupported computer platform: ${platform}`);
}

/** A connection owns one upstream MCP session; the macOS proxy starts its signed
 * App through LaunchServices, so TCC grants belong to Open Computer Use.app.
 * No shell, global install, generated code, or arbitrary MCP tools are exposed.
 */
export class ComputerMcpRuntime implements ComputerRuntime {
  tools: ComputerTool[] = [];
  private nextId = 1;
  private buffer = "";
  private diagnostic = "";
  private failure?: Error;
  private pending = new Map<number, { resolve: (v: any) => void; reject: (e: Error) => void; timer: ReturnType<typeof setTimeout> }>();

  private readonly child: ChildProcessWithoutNullStreams;
  private readonly cleanup: () => Promise<void>;
  private readonly timeoutMs: number;
  private prepareObservation?: (args: Record<string, unknown>) => Promise<void>;

  constructor(child: ChildProcessWithoutNullStreams, cleanup: () => Promise<void> = async () => {}, timeoutMs = 30_000, prepareObservation?: (args: Record<string, unknown>) => Promise<void>) {
    this.prepareObservation = prepareObservation;
    this.child = child; this.cleanup = cleanup; this.timeoutMs = timeoutMs;
    child.stdout.setEncoding("utf8");
    child.stdout.on("data", (chunk: string) => {
      this.buffer += chunk;
      // Screenshots are bounded by the configured 1280px long edge.
      if (this.buffer.length > 16 * 1024 * 1024) return this.fail(new Error("Computer Use response exceeds 16 MiB"));
      let end: number;
      while ((end = this.buffer.indexOf("\n")) >= 0) {
        const line = this.buffer.slice(0, end); this.buffer = this.buffer.slice(end + 1);
        if (!line.trim()) continue;
        try {
          const frame = JSON.parse(line);
          if (frame.jsonrpc !== "2.0") throw Error("Invalid Computer Use JSON-RPC response");
          const pending = this.pending.get(frame.id);
          if (!pending) continue;
          if (("result" in frame) === ("error" in frame)) throw Error("Invalid Computer Use response envelope");
          this.pending.delete(frame.id); clearTimeout(pending.timer);
          if (frame.error) pending.reject(new Error(`Computer Use RPC ${frame.error.code}: ${frame.error.message}`));
          else pending.resolve(frame.result);
        } catch (error) { this.fail(error instanceof Error ? error : new Error(String(error))); }
      }
    });
    child.stderr.on("data", chunk => { this.diagnostic = (this.diagnostic + String(chunk)).slice(-2000); });
    child.stdin.on("error", error => this.fail(error));
    child.on("error", error => this.fail(error));
    child.on("close", (code, signal) => this.fail(new Error(`Computer Use connection closed (${code ?? signal}). ${this.diagnostic}`)));
  }

  static async create(): Promise<ComputerMcpRuntime> {
    const root = dirname(createRequire(import.meta.url).resolve("open-computer-use/package.json"));
    const manifest = JSON.parse(await readFile(join(root, "package.json"), "utf8"));
    if (manifest.version !== COMPUTER_USE_VERSION) throw Error(`Computer Use version mismatch: expected ${COMPUTER_USE_VERSION}, found ${manifest.version}`);
    const directory = await mkdtemp(join(tmpdir(), "aiclaw-computer-"));
    try {
      const config = join(directory, "config.json");
      await writeFile(config, "{}\n", { mode: 0o600 });
      const executable = await realpath(computerExecutable(root));
      // Own configuration: inherited OCU settings must not silently enable
      // global pointer input, virtual displays or privileged locked-use services.
      const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith("OPEN_COMPUTER_USE_")));
      Object.assign(env, {
        OPEN_COMPUTER_USE_CONFIG_FILE: config,
        OPEN_COMPUTER_USE_AGENT_SOCKET_NAMESPACE: computerNamespace(executable),
        OPEN_COMPUTER_USE_ALLOW_GLOBAL_POINTER_FALLBACKS: "0",
        OPEN_COMPUTER_USE_ACTION_READ_BACK: "0",
        OPEN_COMPUTER_USE_IMAGE_FORMAT: "png",
        OPEN_COMPUTER_USE_IMAGE_MAX_DIMENSION: "1280",
      });
      const child = spawn(executable, ["mcp"], { stdio: ["pipe", "pipe", "pipe"], env, windowsHide: true });
      const prepare = process.platform === "darwin" ? async (args: Record<string, unknown>) => {
        const status = await run(executable, ["doctor", "--json"], { env, timeout: 10_000 });
        const permissions = JSON.parse(status.stdout);
        if (!permissions.accessibilityTrusted || (args.include_screenshot !== false && !permissions.screenCaptureGranted)) {
          // No arguments opens only the upstream permission onboarding. Do not
          // grant permissions, install locked-use components or replay the call.
          await run(executable, [], { env, timeout: 10_000 });
          throw Error("Open Computer Use.app needs Accessibility and Screen Recording. Its permission setup window is open; finish setup there, then observe again. No input was dispatched.");
        }
      } : undefined;
      const runtime = new ComputerMcpRuntime(child, () => rm(directory, { recursive: true, force: true }), 30_000, prepare);
      try { await runtime.initialize(); return runtime; }
      catch (error) { await runtime.close(); throw error; }
    } catch (error) { await rm(directory, { recursive: true, force: true }); throw error; }
  }

  async initialize(): Promise<void> {
    const initialized = await this.request("initialize", { protocolVersion: "2025-03-26", capabilities: {}, clientInfo: { name: "aiclaw", version: "1" } });
    if (initialized?.serverInfo?.name !== "open-computer-use" || initialized.serverInfo.version !== COMPUTER_USE_VERSION || initialized.protocolVersion !== "2025-03-26") {
      throw Error("Incompatible Open Computer Use service version or MCP protocol");
    }
    this.child.stdin.write(JSON.stringify({ jsonrpc: "2.0", method: "notifications/initialized" }) + "\n");
    const manifest = await this.request("tools/list", {});
    if (!Array.isArray(manifest?.tools)) throw Error("Computer Use returned no tool manifest");
    this.tools = manifest.tools;
  }

  async call(name: string, args: Record<string, unknown>): Promise<ComputerToolResult> {
    if (name === "get_app_state" && this.prepareObservation) {
      await this.prepareObservation(args);
      if (args.include_screenshot !== false) this.prepareObservation = undefined;
    }
    // Real application AX snapshots took 22-28s in the reported session. Give
    // observation its own budget; keep input bounded and never replay it.
    return this.request("tools/call", { name, arguments: args }, name === "get_app_state" ? this.timeoutMs * 2 : this.timeoutMs);
  }

  private request(method: string, params: unknown, timeoutMs = this.timeoutMs): Promise<any> {
    if (this.failure) return Promise.reject(this.failure);
    const id = this.nextId++;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => this.fail(new Error(`Computer Use ${method} timed out; outcome is unknown. Observe again; never automatically replay input.`)), timeoutMs);
      this.pending.set(id, { resolve, reject, timer });
      this.child.stdin.write(JSON.stringify({ jsonrpc: "2.0", id, method, params }) + "\n", error => { if (error) this.fail(error); });
    });
  }

  private fail(error: Error): void {
    this.failure ??= error;
    for (const pending of this.pending.values()) { clearTimeout(pending.timer); pending.reject(this.failure); }
    this.pending.clear();
    this.child.kill();
  }

  async close(): Promise<void> {
    if (!this.failure) this.child.stdin.end(JSON.stringify({ jsonrpc: "2.0", method: "notifications/turn-ended" }) + "\n");
    if (this.child.exitCode === null && this.child.signalCode === null) {
      await new Promise<void>(resolve => {
        const kill = setTimeout(() => { this.child.kill("SIGKILL"); resolve(); }, 1500);
        this.child.once("close", () => { clearTimeout(kill); resolve(); });
      });
    }
    this.fail(new Error("Computer Use session closed"));
    await this.cleanup();
  }
}
