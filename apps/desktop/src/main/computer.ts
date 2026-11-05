import type { CuaDriverLike, ToolResult } from "@trycua/cua-driver";
import type { ComputerRequestParams, ComputerResult } from "@aiclaw/agent-client";

// Only native window operations are exposed. In particular, no arbitrary script,
// desktop input, browser profile attachment, clipboard or driver configuration.
const READS = new Set(["list_apps", "list_windows", "get_window_state"]);
const INPUTS = new Set(["click", "double_click", "right_click", "type_text", "press_key", "hotkey", "scroll", "drag"]);
const ACTIONS = new Set([...READS, ...INPUTS, "launch_app"]);
const WINDOW_ARGS = ["pid", "window_id", "element_token", "delivery_mode"];
const ARGUMENTS: Record<string, string[]> = {
  list_apps: [], list_windows: ["app", "pid", "on_screen_only"], launch_app: ["bundle_id", "name"],
  get_window_state: ["pid", "window_id", "query", "max_elements", "max_depth", "timeout_ms", "include_screenshot"],
  click: [...WINDOW_ARGS, "x", "y", "capture_id", "button", "count", "action"],
  double_click: [...WINDOW_ARGS, "x", "y"], right_click: [...WINDOW_ARGS, "x", "y"],
  type_text: [...WINDOW_ARGS, "text", "x", "y"], press_key: [...WINDOW_ARGS, "key", "modifiers"],
  hotkey: [...WINDOW_ARGS, "keys"], scroll: [...WINDOW_ARGS, "x", "y", "direction", "amount", "by"],
  drag: [...WINDOW_ARGS, "from_x", "from_y", "to_x", "to_y", "duration_ms"],
};

type Driver = Pick<CuaDriverLike, "callTool" | "shutdown" | "listToolsJson"> & { uniffiDestroy?: () => void };
type DriverFactory = () => Promise<Driver>;

async function loadDriver(): Promise<Driver> {
  // Never connect to an independently installed daemon: permissions, sessions and
  // shutdown belong to this AIClaw process. Loading is lazy when the feature is used.
  const { CuaDriver } = await import("@trycua/cua-driver");
  return CuaDriver.create({ embedded: true, claudeCodeCompatibility: false });
}

function checked(result: ToolResult): ToolResult {
  if (result.isError) throw new Error(`[${result.errorCode ?? "computer_error"}] ${resultText(result) || "Cua Driver refused the operation"}`);
  if (!result.text.trim() && !result.structuredJson) throw new Error("Cua Driver returned an empty result; outcome is unknown");
  return result;
}

function resultText(result: ToolResult): string {
  // structuredJson preserves capture IDs, verification receipts and partial-tree
  // flags even when the human-readable rendering leaves those fields out.
  return [result.text, result.structuredJson].filter(Boolean).join("\n");
}

function observation(result: ToolResult): ComputerResult {
  const image = result.images[0];
  if (image && image.mimeType !== "image/png") throw new Error(`Unsupported computer screenshot format: ${image.mimeType}`);
  return { text: resultText(result), ...(image ? { imageBase64: image.dataBase64 } : {}) };
}

/** Host-owned Cua runtime, with ordered operations and isolated per-chat sessions. */
export class ComputerController {
  private driver?: Promise<Driver>;
  private nativeArguments = new Map<string, Set<string>>();
  private queue: Promise<unknown> = Promise.resolve();
  private readonly snapshots = new Map<string, { id: string; pixels: boolean }>();

  private readonly createDriver: DriverFactory;

  constructor(createDriver: DriverFactory = loadDriver) { this.createDriver = createDriver; }

  perform(request: ComputerRequestParams): Promise<ComputerResult> {
    const operation = this.queue.then(() => this.execute(request));
    this.queue = operation.catch(() => {});
    return operation;
  }

  private async execute(request: ComputerRequestParams): Promise<ComputerResult> {
    const action = request.action;
    if (!ACTIONS.has(action)) throw new Error(`Unsupported computer operation: ${action}. Discover the app/window and use the window-scoped tools.`);
    const args = request.arguments;
    if (!args || typeof args !== "object" || Array.isArray(args)) throw new Error("Computer arguments must be an object");
    for (const key of Object.keys(args)) {
      if (!ARGUMENTS[action]!.includes(key)) throw new Error(`Unsupported computer argument: ${key}`);
    }
    if (action === "launch_app" && ![args.bundle_id, args.name].some(value => typeof value === "string" && value.trim())) {
      throw new Error("launch_app requires bundle_id or name");
    }
    const targeted = !["list_apps", "list_windows", "launch_app"].includes(action);
    if (targeted && (!Number.isSafeInteger(args.pid) || Number(args.pid) <= 0 || !Number.isSafeInteger(args.window_id) || Number(args.window_id) <= 0)) {
      throw new Error("An exact positive pid and window_id from list_windows are required");
    }
    const session = request.sessionId;
    if (!session) throw new Error("Computer session is missing");
    const driver = await (this.driver ??= this.createDriver().then(async driver => {
      try {
        const manifest = JSON.parse(await driver.listToolsJson());
        this.nativeArguments = new Map(manifest.tools.map((tool: { name: string; inputSchema: { properties: Record<string, unknown> } }) =>
          [tool.name, new Set(Object.keys(tool.inputSchema.properties ?? {}))]));
        return driver;
      } catch (error) {
        try { await driver.shutdown(); } finally { driver.uniffiDestroy?.(); }
        throw error;
      }
    }).catch(error => { this.driver = undefined; throw error; }));
    const nativeArguments = this.nativeArguments.get(action);
    if (!nativeArguments) throw new Error(`Native runtime does not support ${action} on ${process.platform}`);
    for (const field of Object.keys(args)) {
      if (!nativeArguments.has(field)) throw new Error(`Native runtime does not support ${action}.${field} on ${process.platform}`);
    }
    const call = async (name: string, params: Record<string, unknown>) => checked(await driver.callTool(name, JSON.stringify({ ...params, session }), { signal: AbortSignal.timeout(name === "list_windows" ? 5_000 : 20_000) }));
    const snapshotKey = `${session}:${args.pid}:${args.window_id}`;
    if (INPUTS.has(action)) {
      const snapshot = this.snapshots.get(snapshotKey);
      if (!snapshot) throw new Error("Observe this exact window with computer_screenshot before acting");
      if (args.element_token !== undefined && (typeof args.element_token !== "string" || !args.element_token.startsWith(`${snapshot.id}:`) || !/^s[0-9a-f]{8}:[0-9]+$/.test(args.element_token))) {
        throw new Error("Element token does not belong to this window's latest observation; observe again");
      }
      if ((args.x !== undefined || args.from_x !== undefined) && !snapshot.pixels) {
        throw new Error("Pixel actions require a fresh window screenshot");
      }
      // Resolve ownership independently of model-provided names. Unknown windows
      // fail closed; a token can never redirect input into AIClaw's approval UI.
      const windows = checked(await call("list_windows", { pid: args.pid }));
      const data = JSON.parse(windows.structuredJson || windows.text);
      const window = data.windows?.find((w: Record<string, unknown>) => w.window_id === args.window_id && w.pid === args.pid);
      if (!window) throw new Error("Target window no longer exists; list windows and observe again");
      if (args.pid === process.pid || /^(aiclaw|electron)(\.exe)?$/i.test(window.app_name ?? "")) {
        throw new Error("Refusing to operate AIClaw's own windows or approval controls");
      }
      // Bind the token to the caller's exact window by requiring a snapshot made
      // in this session. The driver itself validates token/capture freshness.
      if (args.delivery_mode !== undefined && !["background", "foreground"].includes(String(args.delivery_mode))) {
        throw new Error("Invalid delivery_mode");
      }
    }
    if (INPUTS.has(action) || action === "get_window_state") this.snapshots.delete(snapshotKey);
    let result: ToolResult;
    try {
      result = await call(action, args);
    } catch (error) {
      if (INPUTS.has(action)) {
        throw new Error(`${String(error)}. The action may have been dispatched before failure. Observe again before deciding what to do; do not automatically repeat input or submission.`);
      }
      throw error;
    }
    const output = observation(result);
    const remember = (state: ToolResult) => {
      const data = JSON.parse(state.structuredJson || "{}");
      if (data.pid !== args.pid || data.window_id !== args.window_id || !/^s[0-9a-f]{8}$/.test(data.snapshot_id ?? "")) {
        throw new Error("Window observation has no matching identity or snapshot token");
      }
      this.snapshots.set(snapshotKey, { id: data.snapshot_id, pixels: state.images.length > 0 });
    };
    if (action === "get_window_state" && args.include_screenshot !== false && !output.imageBase64) {
      throw new Error("Cua Driver returned no window screenshot; do not act on guessed coordinates");
    }
    if (action === "get_window_state") remember(result);
    if (INPUTS.has(action)) {
      try {
        const observed = await call("get_window_state", { pid: args.pid, window_id: args.window_id });
        const state = observation(observed);
        remember(observed);
        output.text += `\nPost-action window observation (verify the intended change before continuing):\n${state.text}`;
        output.imageBase64 = state.imageBase64;
        if (!state.imageBase64) output.text += "\nPost-action screenshot unavailable; use the tree cautiously and request a fresh observation.";
      } catch (error) {
        // The input has already been dispatched. Keep its receipt and never
        // replay it just because the subsequent observation failed.
        output.text += `\nInput was dispatched, but post-action verification failed: ${String(error)}. Outcome is unknown; observe again. Do not automatically retry the input.`;
      }
    }
    return output;
  }

  async releaseSession(session: string): Promise<void> {
    const operation = this.queue.then(async () => {
      for (const key of this.snapshots.keys()) if (key.startsWith(`${session}:`)) this.snapshots.delete(key);
      if (this.driver) await (await this.driver).callTool("end_session", JSON.stringify({ session }));
    });
    this.queue = operation.catch(() => {});
    await operation;
  }

  async close(): Promise<void> {
    await this.queue;
    const pending = this.driver;
    this.driver = undefined;
    this.snapshots.clear();
    this.nativeArguments.clear();
    if (pending) {
      const driver = await pending;
      try { await driver.shutdown(); } finally { driver.uniffiDestroy?.(); }
    }
  }
}
