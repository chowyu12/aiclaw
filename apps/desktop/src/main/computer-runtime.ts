import { execFile } from "node:child_process";
import { createHash } from "node:crypto";
import { readFile, realpath, stat } from "node:fs/promises";
import { createConnection, type Socket } from "node:net";
import { homedir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { setTimeout as pause } from "node:timers/promises";
import { installComputerBundle, installedComputerBundle } from "./computer-install.js";
const run = promisify(execFile);
export const COMPUTER_USE_VERSION = "1";
export const COMPUTER_PROTOCOL = "AIClawComputerUseIPC-1";
export interface ComputerTool { name: string; inputSchema: { properties: Record<string, unknown>; required?: string[] } }
export interface ComputerToolResult { content: Array<{ type: string; text?: string; data?: string; mimeType?: string }>; isError?: boolean }
export interface ComputerRuntime { tools: ComputerTool[]; call(name: string, args: Record<string, unknown>): Promise<ComputerToolResult>; close(): Promise<void> }
export function computerExecutable(bundle: string): string { return join(bundle, "Contents", "MacOS", "AIClawComputerUse"); }
export function computerNamespace(bundle: string): string { return createHash("sha256").update(bundle).digest("hex").slice(0, 20); }
export function computerSocket(bundle: string, identity = ""): string { return join(homedir(), ".aiclaw", "computer-use", `${computerNamespace(`${bundle}\0${identity}`)}.sock`); }
export function computerBundle(): string {
  return installedComputerBundle();
}
export function bundledComputerBundle(): string { return resolve(dirname(fileURLToPath(import.meta.url)), "../../computer-use/AIClaw Computer Use.app"); }
class NativeRequestError extends Error {}

/** One authenticated Unix connection per chat/turn. The LaunchServices app owns
 * TCC permissions and native state; Electron never loads a native driver.
 */
export class ComputerNativeRuntime implements ComputerRuntime {
  tools: ComputerTool[] = [];
  private nextId = 1;
  private buffer = "";
  private failure?: Error;
  private readonly socket: Socket;
  private readonly timeoutMs: number;
  private readonly expectedIdentity?: string;
  private pending = new Map<number, { resolve: (value: any) => void; reject: (error: Error) => void; timer: ReturnType<typeof setTimeout> }>();
  constructor(socket: Socket, timeoutMs = 30_000, expectedIdentity?: string) {
    this.socket = socket; this.timeoutMs = timeoutMs; this.expectedIdentity = expectedIdentity;
    socket.setEncoding("utf8");
    socket.on("data", chunk => {
      this.buffer += String(chunk);
      if (this.buffer.length > 16 * 1024 * 1024) return this.fail(Error("Native Computer Use response exceeds 16 MiB"));
      let end: number;
      while ((end = this.buffer.indexOf("\n")) >= 0) {
        const line = this.buffer.slice(0, end); this.buffer = this.buffer.slice(end + 1);
        if (!line.trim()) continue;
        try {
          const frame = JSON.parse(line);
          const pending = this.pending.get(frame.id);
          if (!pending) continue;
          if (("result" in frame) === ("error" in frame)) throw Error("Invalid native Computer Use response");
          this.pending.delete(frame.id); clearTimeout(pending.timer);
          if (frame.error) pending.reject(new NativeRequestError(String(frame.error.message)));
          else pending.resolve(frame.result);
        } catch (error) { this.fail(error instanceof Error ? error : Error(String(error))); }
      }
    });
    socket.on("error", error => this.fail(error));
    socket.on("close", () => this.fail(Error("Native Computer Use connection closed; outcome may be unknown")));
  }
  static async create(): Promise<ComputerNativeRuntime> {
    if (process.platform !== "darwin") throw Error("AIClaw's native Computer Use currently requires macOS 14.4 or later");
    const source = await realpath(bundledComputerBundle()).catch(() => { throw Error("AIClaw Computer Use.app is missing; run make build-computer or reinstall AIClaw"); });
    const bundle = await realpath(await installComputerBundle(source));
    await stat(computerExecutable(bundle));
    const identity = createHash("sha256").update(await readFile(computerExecutable(bundle))).digest("hex");
    const path = computerSocket(bundle, identity);
    const connect = () => new Promise<Socket>((resolve, reject) => {
      const socket = createConnection(path); const timer = setTimeout(() => socket.destroy(Error("Computer Use connection timed out")), 1000);
      socket.once("connect", () => { clearTimeout(timer); socket.removeListener("error", reject); resolve(socket); });
      socket.once("error", error => { clearTimeout(timer); reject(error); });
    });
    let socket: Socket;
    try { socket = await connect(); }
    catch {
      // Stable app identity; no daemon installation, unsigned dynamic helper or
      // user MCP configuration. Different executable generations use distinct sockets at the stable user path.
      await run("/usr/bin/open", ["-n", "-g", "-a", bundle, "--args", "--socket", path], { timeout: 10_000 });
      let connected: Socket | undefined;
      for (let attempt = 0; attempt < 50 && !connected; attempt++) {
        try { connected = await connect(); } catch { await pause(100); }
      }
      if (!connected) throw Error("AIClaw Computer Use.app did not open its local IPC socket");
      socket = connected;
    }
    const runtime = new ComputerNativeRuntime(socket, 30_000, identity);
    try { await runtime.initialize(); return runtime; } catch (error) { await runtime.close(); throw error; }
  }
  async initialize(): Promise<void> {
    const response = await this.request("ComputerUseIPCHandshakeRequest", {});
    if (response?.protocolVersion !== COMPUTER_PROTOCOL || response.serverVersion !== COMPUTER_USE_VERSION || !Array.isArray(response.tools)) throw Error("Incompatible AIClaw Computer Use app or IPC protocol");
    if (this.expectedIdentity && response.processExecutableSHA256 !== this.expectedIdentity) throw Error("The resident Computer Use app does not match this build; reconnect to the current helper");
    this.tools = response.tools;
  }
  async call(name: string, args: Record<string, unknown>): Promise<ComputerToolResult> {
    const direct: Record<string, string> = {
      list_apps: "ComputerUseIPCListAppsRequest", find_apps: "ComputerUseIPCFindAppsRequest",
      start_app: "ComputerUseIPCAppStartRequest", stop_app: "ComputerUseIPCAppStopRequest",
      modify_app: "ComputerUseIPCAppModifyRequest", frontmost_window: "ComputerUseIPCFrontmostWindowRequest",
      get_app_state: "ComputerUseIPCAppGetSkyshotRequest", query: "ComputerUseIPCAppQueryRequest",
      permission_status: "ComputerUseIPCPermissionStatusRequest",
    };
    const { app, element_index, x, y, ...rest } = args;
    const point = element_index !== undefined ? { elementID: element_index } : x !== undefined ? { coordinate: [x, y] } : {};
    const actions: Record<string, unknown> = {
      click: { ...point, clickCount: rest.click_count, mouseButton: rest.mouse_button, key: rest.key, duration: rest.duration },
      drag: { from: [rest.from_x, rest.from_y], to: [rest.to_x, rest.to_y] },
      press_key: { key: rest.key, duration: rest.duration }, type_text: { text: rest.text },
      scroll: { ...point, direction: rest.direction, pages: rest.pages },
      set_value: { elementID: element_index, value: rest.value, autosubmitSearchFields: rest.autosubmit_search_fields },
      select_text: { elementID: element_index, text: rest.text, prefix: rest.prefix, suffix: rest.suffix, selection: rest.selection === "cursor_before" ? "cursorBefore" : rest.selection === "cursor_after" ? "cursorAfter" : rest.selection },
      perform_secondary_action: { elementID: element_index, action: rest.action }, paste: { text: rest.text, format: rest.format },
    };
    const actionNames: Record<string, string> = { press_key: "pressKey", type_text: "type", set_value: "setValue", select_text: "selectText", perform_secondary_action: "performSecondaryAction" };
    if (!direct[name] && !Object.hasOwn(actions, name)) throw Error(`Unsupported native operation: ${name}`);
    try {
      return await this.request(direct[name] ?? "ComputerUseIPCAppPerformActionRequest", direct[name] ? args : { app, interactionMode: rest.interaction_mode, action: { [actionNames[name] ?? name]: actions[name] } }, name === "get_app_state" ? this.timeoutMs * 2 : this.timeoutMs);
    } catch (error) {
      if (error instanceof NativeRequestError) return { isError: true, content: [{ type: "text", text: error.message }] };
      throw error;
    }
  }
  private request(requestType: string, request: unknown, timeoutMs = this.timeoutMs): Promise<any> {
    if (this.failure) return Promise.reject(this.failure);
    const id = this.nextId++;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => this.fail(Error("Native Computer Use timed out; outcome is unknown. Observe again; never automatically replay input.")), timeoutMs);
      this.pending.set(id, { resolve, reject, timer });
      this.socket.write(JSON.stringify({ id, requestType, request }) + "\n", error => { if (error) this.fail(error); });
    });
  }
  private fail(error: Error): void {
    this.failure ??= error;
    for (const pending of this.pending.values()) { clearTimeout(pending.timer); pending.reject(this.failure); }
    this.pending.clear(); this.socket.destroy();
  }
  async close(): Promise<void> { this.fail(Error("Native Computer Use session closed")); }
}
