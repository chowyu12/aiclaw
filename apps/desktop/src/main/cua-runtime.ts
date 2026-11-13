import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { randomUUID } from "node:crypto";
import { access, readFile } from "node:fs/promises";
import { join, resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import type {
  EmbeddedCuaDriverHost,
  EmbeddedDriverConnection,
} from "@trycua/cua-driver/embedded";
import type {
  ComputerRuntime,
  ComputerTool,
  ComputerToolResult,
} from "./computer-runtime.js";
export const CUA_VERSION = "0.49.0";
const source = resolve(
  dirname(fileURLToPath(import.meta.url)),
  "../../cua-driver",
);
/** The driver is a bundled child executable, not an installed independent App. */
export async function bundledCuaExecutable(): Promise<string> {
  const manifest = JSON.parse(
    await readFile(join(source, "runtime.json"), "utf8"),
  );
  if (manifest.version !== CUA_VERSION)
    throw Error("Cua Driver version mismatch; rebuild AIClaw");
  const binary = join(
    source,
    process.platform === "win32" ? "cua-driver.exe" : "cua-driver",
  );
  await access(binary);
  return binary;
}
type Daemon = {
  host: Pick<EmbeddedCuaDriverHost, "start" | "stop" | "uniffiDestroy">;
  connection: EmbeddedDriverConnection;
  leases: number;
};
let daemon: Promise<Daemon> | undefined;
async function acquireDaemon(binary: string): Promise<Daemon> {
  const current = (daemon ??= (async () => {
    const { EmbeddedCuaDriverHost } = await import(
      "@trycua/cua-driver/embedded"
    );
    const { app } = await import("electron");
    const host = EmbeddedCuaDriverHost.withOptions({
      binaryPath: binary,
      hostBundleId: app.isPackaged
        ? "com.github.chowyu12.aiclaw"
        : "com.github.Electron",
      environment: [
        { name: "CUA_TELEMETRY_ENABLED", value: "0" },
        { name: "CUA_DRIVER_RS_TELEMETRY_ENABLED", value: "0" },
      ],
      inheritStderr: false,
      approveCapabilityManifest: false,
      approveSessionPolicy: false,
      dangerouslyBypassApprovals: false,
      noOverlay: false,
      captureStderr: false,
    }) as EmbeddedCuaDriverHost; // Generated factory returns a narrowed interface; the object owns its destructor.
    try {
      const connection = await host.start();
      if (connection.driverVersion !== CUA_VERSION)
        throw Error("Embedded Cua Driver version mismatch");
      return { host, connection, leases: 0 };
    } catch (error) {
      await host.stop().catch(() => {});
      host.uniffiDestroy();
      throw error;
    }
  })());
  try {
    const value = await current;
    value.leases++;
    return value;
  } catch (error) {
    if (daemon === current) daemon = undefined;
    throw error;
  }
}
async function releaseDaemon(value: Daemon): Promise<void> {
  if (--value.leases > 0) return;
  daemon = undefined;
  try {
    await value.host.stop();
  } finally {
    value.host.uniffiDestroy();
  }
}

/** One stdio lease and cursor per turn. EOF revokes only this lease, never other clients. */
export class CuaRuntime implements ComputerRuntime {
  tools: ComputerTool[] = [];
  private id = 0;
  private buffer = "";
  private failure?: Error;
  private closed = false;
  private pending = new Map<
    number,
    {
      resolve: (value: any) => void;
      reject: (error: Error) => void;
      timer: ReturnType<typeof setTimeout>;
    }
  >();
  readonly session = `AIClaw-${randomUUID().slice(0, 8)}`;
  private child: ChildProcessWithoutNullStreams;
  private release: () => Promise<void>;
  constructor(
    child: ChildProcessWithoutNullStreams,
    release: () => Promise<void> = async () => {},
  ) {
    this.child = child;
    this.release = release;
    child.stdout.setEncoding("utf8");
    child.stdout.on("data", (chunk) => {
      this.buffer += chunk;
      if (this.buffer.length > 32 * 1024 * 1024)
        return this.fail(Error("Cua response exceeds 32 MiB"));
      let end: number;
      while ((end = this.buffer.indexOf("\n")) >= 0) {
        const line = this.buffer.slice(0, end);
        this.buffer = this.buffer.slice(end + 1);
        if (!line.trim()) continue;
        try {
          const frame = JSON.parse(line),
            pending = this.pending.get(frame.id);
          if (!pending) continue;
          this.pending.delete(frame.id);
          clearTimeout(pending.timer);
          if (frame.error) pending.reject(Error(String(frame.error.message)));
          else pending.resolve(frame.result);
        } catch {
          this.fail(Error("Invalid Cua MCP response"));
        }
      }
    });
    child.stderr.resume(); // Never log window contents or unbounded vendor diagnostics.
    child.on("error", (error) => this.fail(error));
    child.on("exit", () =>
      this.fail(
        Error(
          "Cua connection ended; input outcome may be unknown. Observe before deciding what to do next.",
        ),
      ),
    );
    child.stdin.on("error", (error) => this.fail(error));
  }
  static async create(): Promise<CuaRuntime> {
    const binary = await bundledCuaExecutable();
    const host = await acquireDaemon(binary);
    const mcp = host.connection.mcp;
    const env = {
      ...process.env,
      ...Object.fromEntries(
        mcp.environment.map((item) => [item.name, item.value]),
      ),
      CUA_TELEMETRY_ENABLED: "0",
      CUA_DRIVER_RS_TELEMETRY_ENABLED: "0",
    };
    const runtime = new CuaRuntime(
      spawn(mcp.command, mcp.args, { stdio: ["pipe", "pipe", "pipe"], env }),
      () => releaseDaemon(host),
    );
    try {
      await runtime.initialize();
      if (process.platform === "darwin") {
        const permissions = await runtime.call("check_permissions", { prompt: false });
        const owner = permissions.structuredContent?.source as Record<string, unknown> | undefined;
        if (owner?.attribution !== "host" || owner.embedded !== true)
          throw Error("Cua did not inherit AIClaw's permission identity; refusing standalone execution");
        const health = await runtime.call("health_report", { include: ["bundle_identity"] });
        const checks = health.structuredContent?.checks as Array<{name:string;status:string;data?:{parent_process_id?:number}}> | undefined;
        const identity = checks?.find(check => check.name === "bundle_identity");
        if (identity?.status !== "pass" || identity.data?.parent_process_id !== process.pid)
          throw Error("Cua is not directly hosted by this AIClaw process");
      }
      return runtime;
    } catch (error) {
      await runtime.close();
      throw error;
    }
  }
  async initialize(): Promise<void> {
    const hello = await this.request("initialize", {
      protocolVersion: "2025-06-18",
      capabilities: {},
      clientInfo: { name: "AIClaw", version: "1" },
    });
    if (hello.protocolVersion !== "2025-06-18")
      throw Error("Incompatible Cua MCP protocol");
    this.notify("notifications/initialized", {});
    const list = await this.request("tools/list", {});
    if (!Array.isArray(list.tools)) throw Error("Invalid Cua tool manifest");
    this.tools = list.tools;
    const started = await this.call("start_session", {
      cursor_motion: { timing: "native" },
    });
    if (started.isError)
      throw Error(started.content.map((item) => item.text ?? "").join("\n"));
    const cursor = await this.call("set_agent_cursor_enabled", {
      enabled: true,
    });
    if (cursor.isError)
      throw Error("Cua could not enable the AIClaw execution cursor");
  }
  private notify(method: string, params: unknown): void {
    if (!this.child.stdin.destroyed)
      this.child.stdin.write(
        JSON.stringify({ jsonrpc: "2.0", method, params }) + "\n",
      );
  }
  private request(method: string, params: unknown): Promise<any> {
    if (this.failure || this.closed)
      return Promise.reject(this.failure ?? Error("Cua session closed"));
    const id = ++this.id;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.notify("notifications/cancelled", {
          requestId: id,
          reason: "AIClaw timeout",
        });
        this.fail(
          Error(
            "Cua request timed out; outcome unknown. Never replay input automatically.",
          ),
        );
        void this.close();
      }, 75_000);
      this.pending.set(id, { resolve, reject, timer });
      this.child.stdin.write(
        JSON.stringify({ jsonrpc: "2.0", id, method, params }) + "\n",
      );
    });
  }
  call(
    name: string,
    args: Record<string, unknown>,
  ): Promise<ComputerToolResult> {
    return this.request("tools/call", {
      name,
      arguments: { ...args, session: this.session },
    });
  }
  private fail(error: Error): void {
    this.failure ??= error;
    for (const pending of this.pending.values()) {
      clearTimeout(pending.timer);
      pending.reject(this.failure);
    }
    this.pending.clear();
  }
  async close(): Promise<void> {
    if (this.closed) return;
    // Revocation and EOF must bypass the active batch, not queue behind it.
    for (const id of this.pending.keys())
      this.notify("notifications/cancelled", {
        requestId: id,
        reason: "AIClaw stopped",
      });
    if (!this.failure) void this.call("end_session", {}).catch(() => {});
    this.closed = true;
    this.fail(Error("Cua session closed"));
    this.child.stdin.end();
    const child = this.child;
    const timer = setTimeout(() => {
      child.kill();
    }, 1000);
    timer.unref();
    child.once("exit", () => clearTimeout(timer));
    await this.release();
  }
}
