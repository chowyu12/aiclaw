import { randomUUID } from "node:crypto";
import { basename } from "node:path";
import type { ComputerRequestParams, ComputerResult } from "@aiclaw/agent-client";
import { ComputerMcpRuntime, type ComputerRuntime, type ComputerToolResult } from "./computer-runtime.js";

const READS = new Set(["list_apps", "get_app_state"]);
const ARGUMENTS: Record<string, string[]> = {
  list_apps: [], get_app_state: ["app", "include_ax", "include_screenshot", "text_limit", "max_tree_nodes", "max_tree_depth"],
  click: ["app", "element_index", "x", "y", "click_count", "mouse_button"],
  drag: ["app", "from_x", "from_y", "to_x", "to_y"],
  press_key: ["app", "key"], type_text: ["app", "text"],
  scroll: ["app", "element_index", "direction", "pages"],
  set_value: ["app", "element_index", "value"], perform_secondary_action: ["app", "element_index", "action"],
};

type Observation = { id: string; pixels: boolean; ax: boolean };
type Session = { runtime: ComputerRuntime; turn: string; observations: Map<string, Observation> };

function observation(result: ComputerToolResult): ComputerResult {
  if (!Array.isArray(result?.content)) throw Error("Open Computer Use returned an invalid result");
  const text = result.content.filter(item => item.type === "text").map(item => item.text ?? "").join("\n");
  if (result.isError) throw Error(text || "Open Computer Use refused the operation");
  if (!text.trim()) throw Error("Open Computer Use returned an empty result; outcome is unknown");
  const image = result.content.find(item => item.type === "image");
  if (image && (image.mimeType !== "image/png" || !image.data)) throw Error("Open Computer Use returned an invalid PNG screenshot");
  return { text, ...(image ? { imageBase64: image.data } : {}) };
}

/** AIClaw owns approval and turn scope; the signed OCU app owns native execution,
 * permissions, target-window snapshots and element indexes. Each chat gets an
 * independent MCP connection; all desktop operations remain ordered.
 */
export class ComputerController {
  private sessions = new Map<string, Session>();
  private turns = new Map<string, boolean>();
  private queue: Promise<unknown> = Promise.resolve();
  private readonly createRuntime: () => Promise<ComputerRuntime>;
  constructor(createRuntime: () => Promise<ComputerRuntime> = () => ComputerMcpRuntime.create()) { this.createRuntime = createRuntime; }

  perform(request: ComputerRequestParams): Promise<ComputerResult> {
    const key = JSON.stringify([request.sessionId, request.turnId]);
    if (!this.turns.has(key)) this.turns.set(key, true);
    const operation = this.queue.then(() => this.execute(request));
    this.queue = operation.catch(() => {});
    return operation;
  }

  private async execute(request: ComputerRequestParams): Promise<ComputerResult> {
    const { action, sessionId, turnId } = request;
    const key = JSON.stringify([sessionId, turnId]);
    if (!this.turns.get(key)) throw Error("Computer Use turn has ended; no queued operation was dispatched");
    if (!Object.hasOwn(ARGUMENTS, action)) throw Error(`Unsupported computer operation: ${action}`);
    if (!sessionId || !turnId) throw Error("Computer session and turn are required");
    const input = request.arguments;
    if (!input || typeof input !== "object" || Array.isArray(input)) throw Error("Computer arguments must be an object");
    const read = READS.has(action);
    for (const key of Object.keys(input)) {
      if (!ARGUMENTS[action]!.includes(key) && !(key === "observation_id" && !read)) throw Error(`Unsupported computer argument: ${key}`);
    }
    const args = { ...input }; delete args.observation_id;
    const app = action === "list_apps" ? "" : this.target(args.app);
    if (app) args.app = app;
    let session = this.sessions.get(sessionId);
    if (session && session.turn !== turnId) {
      this.sessions.delete(sessionId); await session.runtime.close(); session = undefined;
    }
    // Do not start a service for an unobserved input, including after a crash.
    if (!read) {
      const latest = session?.observations.get(app);
      if (!latest || input.observation_id !== latest.id) throw Error("Observe this app with computer_screenshot in this turn and use its latest observation_id");
      if (args.element_index !== undefined && !latest.ax) throw Error("Element actions require an accessibility observation");
      if ((args.x !== undefined || action === "drag") && !latest.pixels) throw Error("Coordinate actions require a fresh screenshot");
    }
    if (!session) {
      const runtime = await this.createRuntime();
      session = { runtime, turn: turnId, observations: new Map() };
      this.sessions.set(sessionId, session);
    }
    if (!this.turns.get(key)) throw Error("Computer Use turn ended during startup; no operation was dispatched");
    const native = session.runtime.tools.find(tool => tool.name === action);
    if (!native) throw Error(`Open Computer Use does not support ${action} on ${process.platform}`);
    for (const key of Object.keys(args)) {
      if (!Object.hasOwn(native.inputSchema.properties, key)) throw Error(`Open Computer Use does not support ${action}.${key}`);
    }
    for (const key of native.inputSchema.required ?? []) {
      if (args[key] === undefined || args[key] === null) throw Error(`Missing computer argument: ${key}`);
    }
    if (app) session.observations.delete(app);
    let output: ComputerResult;
    try { output = observation(await session.runtime.call(action, args)); }
    catch (error) {
      // A timed-out proxy can still have dispatched input. Drop the connection
      // and every observation; reconnect only for an explicit later observation.
      this.sessions.delete(sessionId); await session.runtime.close();
      throw Error(`${String(error)}${read ? "" : ". Outcome may be unknown; observe again. Do not automatically repeat input or submission."}`);
    }
    const remember = (state: ComputerResult, pixels: boolean, ax: boolean) => {
      if (pixels && !state.imageBase64) throw Error("Open Computer Use returned no app screenshot. Grant Accessibility and Screen Recording to Open Computer Use.app, then observe again. Do not guess coordinates.");
      const id = randomUUID(); session.observations.set(app, { id, pixels: !!state.imageBase64, ax });
      state.text += `\nobservation_id: ${id}`;
    };
    if (action === "get_app_state") remember(output, args.include_screenshot !== false, args.include_ax !== false);
    if (!read) {
      try {
        const state = observation(await session.runtime.call("get_app_state", { app }));
        remember(state, true, true);
        output.text += `\nPost-action app observation (verify the intended change before continuing):\n${state.text}`;
        output.imageBase64 = state.imageBase64;
      } catch (error) {
        this.sessions.delete(sessionId); await session.runtime.close();
        output.text += `\nInput was dispatched, but verification failed: ${String(error)}. Outcome is unknown; observe again. Do not automatically replay input.`;
      }
    }
    return output;
  }

  private target(value: unknown): string {
    if (typeof value !== "string" || !value.trim()) throw Error("An explicit app identifier is required");
    const app = value.trim();
    if (app === String(process.pid)) throw Error("Refusing to operate AIClaw or its approval controls");
    const leaf = basename(app.replaceAll("\\", "/")).toLowerCase();
    if (/^(aiclaw|electron|opencomputeruse|open computer use)(\.app|\.exe)?$/.test(leaf) || /^(com\.github\.chowyu12\.aiclaw|com\.ifuryst\.opencomputeruse|com\.electron|com\.github\.electron)(\.|$)/i.test(app)) {
      throw Error("Refusing to operate AIClaw, its helper or approval controls");
    }
    if (process.platform === "darwin" && !/^[a-z0-9-]+(?:\.[a-z0-9-]+)+$/i.test(app)) throw Error("Use the exact bundle identifier from computer_list_apps; fuzzy names and paths are not accepted");
    if (process.platform !== "darwin" && (!/^[1-9][0-9]*$/.test(app) || !Number.isSafeInteger(Number(app)))) throw Error("Use the exact app PID from computer_list_apps; fuzzy names and paths are not accepted");
    return app;
  }

  async finishTurn(sessionId: string, turnId: string): Promise<void> {
    const key = JSON.stringify([sessionId, turnId]);
    if (this.turns.has(key)) this.turns.set(key, false);
    const operation = this.queue.then(async () => {
      const session = this.sessions.get(sessionId);
      if (session?.turn === turnId) { this.sessions.delete(sessionId); await session.runtime.close(); }
    });
    this.queue = operation.catch(() => {}); await operation;
  }

  async releaseSession(sessionId: string): Promise<void> {
    const operation = this.queue.then(async () => {
      const session = this.sessions.get(sessionId); this.sessions.delete(sessionId);
      for (const key of this.turns.keys()) if (JSON.parse(key)[0] === sessionId) this.turns.delete(key);
      await session?.runtime.close();
    });
    this.queue = operation.catch(() => {}); await operation;
  }

  async close(): Promise<void> {
    await this.queue;
    const sessions = [...this.sessions.values()]; this.sessions.clear();
    await Promise.all(sessions.map(session => session.runtime.close()));
    this.turns.clear();
  }
}
