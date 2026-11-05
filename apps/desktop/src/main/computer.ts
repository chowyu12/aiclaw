import { createHash, randomUUID } from "node:crypto";
import { basename } from "node:path";
import { setTimeout as pause } from "node:timers/promises";
import type { ComputerRequestParams, ComputerResult } from "@aiclaw/agent-client";
import { createComputerRuntime, type ComputerRuntime, type ComputerToolResult } from "./computer-runtime.js";
import { ComputerWatches } from "./computer-watch.js";
import { computerAppPolicy } from "./computer-policy.js";

const READS = new Set(["list_apps", "find_apps", "frontmost_window", "permission_status", "get_app_state", "query", "watch_start", "watch_next", "watch_stop", "app_policy"]);
const ARGUMENTS: Record<string, string[]> = {
  app_policy: ["app"],
  watch_start: ["app", "window_id", "interval_ms"], watch_next: ["watch_id", "after_sequence", "wait_ms"], watch_stop: ["watch_id"],
  find_apps: ["text", "limit"], start_app: ["app"], stop_app: ["app"], modify_app: ["app", "modification"], frontmost_window: [], permission_status: [],
  query: ["app", "text", "role", "exact", "limit", "max_nodes", "window_id"],
  select_text: ["app", "element_index", "text", "prefix", "suffix", "selection"],
  paste: ["app", "text", "format"],
  list_apps: [], get_app_state: ["app", "include_ax", "include_screenshot", "text_limit", "max_tree_nodes", "max_tree_depth", "window_id"],
  click: ["app", "element_index", "x", "y", "click_count", "mouse_button", "key", "duration"],
  drag: ["app", "from_x", "from_y", "to_x", "to_y"],
  press_key: ["app", "key", "duration"], type_text: ["app", "text"],
  scroll: ["app", "element_index", "x", "y", "direction", "pages"],
  set_value: ["app", "element_index", "value", "autosubmit_search_fields"], perform_secondary_action: ["app", "element_index", "action"],
};

type Observation = { id: string; pixels: boolean; ax: boolean; queryIndices?: Set<string>; screenshotHash?: string; windowID?: number };
class ComputerToolError extends Error {}
function screenshotHash(state: ComputerResult): string | undefined {
  return state.imageBase64 ? createHash("sha256").update(state.imageBase64).digest("hex") : undefined;
}
type Session = { runtime: ComputerRuntime; turn: string; observations: Map<string, Observation>; policies: Set<string> };

function observation(result: ComputerToolResult): ComputerResult {
  if (!Array.isArray(result?.content)) throw Error("AIClaw Computer Use returned an invalid result");
  const text = result.content.filter(item => item.type === "text").map(item => item.text ?? "").join("\n");
  if (result.isError) throw new ComputerToolError(text || "AIClaw Computer Use refused the operation");
  if (!text.trim()) throw Error("AIClaw Computer Use returned an empty result; outcome is unknown");
  const image = result.content.find(item => item.type === "image");
  if (image && (image.mimeType !== "image/png" || !image.data)) throw Error("AIClaw Computer Use returned an invalid PNG screenshot");
  return { text, ...(image ? { imageBase64: image.data } : {}) };
}

/** AIClaw owns approval and turn scope; the independent native app owns native execution,
 * permissions, target-window snapshots and element indexes. Each chat gets an
 * independent IPC connection; all desktop operations remain ordered.
 */
export class ComputerController {
  private sessions = new Map<string, Session>();
  private turns = new Map<string, boolean>();
  private queue: Promise<unknown> = Promise.resolve();
  private readonly createRuntime: () => Promise<ComputerRuntime>;
  private readonly watches: ComputerWatches;
  constructor(createRuntime: () => Promise<ComputerRuntime> = createComputerRuntime) { this.createRuntime = createRuntime; this.watches = new ComputerWatches(createRuntime); }

  perform(request: ComputerRequestParams): Promise<ComputerResult> {
    const key = JSON.stringify([request.sessionId, request.turnId]);
    if (!this.turns.has(key)) this.turns.set(key, true);
    // Waiting for a sampled change must not block desktop actions or stop requests.
    if (request.action === "watch_next") return this.execute(request);
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
    const lifecycle = ["start_app", "stop_app", "modify_app"].includes(action);
    const hostArguments = read ? [] : lifecycle ? ["observation_id", "verify_after"] : ["observation_id", "verify_after", "interaction_mode"];
    for (const key of Object.keys(input)) {
      if (!ARGUMENTS[action]!.includes(key) && !hostArguments.includes(key)) throw Error(`Unsupported computer argument: ${key}`);
    }
    if (input.verify_after !== undefined && typeof input.verify_after !== "boolean") throw Error("verify_after must be a boolean");
    if (input.interaction_mode !== undefined && !["foreground", "background"].includes(String(input.interaction_mode))) throw Error("interaction_mode must be foreground or background");
    const args = { ...input }; delete args.observation_id; delete args.verify_after;
    if (action === "watch_next") return this.watches.next(sessionId, turnId, args);
    if (action === "watch_stop") return this.watches.stop(sessionId, turnId, args);
    if (action === "query" && ![args.text, args.role].some(value => typeof value === "string" && value.trim())) throw Error("computer_query requires text or role");
    if (args.window_id !== undefined && (!Number.isSafeInteger(args.window_id) || Number(args.window_id) < 1 || Number(args.window_id) > 0xffffffff)) throw Error("window_id must be an integer from 1 to 4294967295");
    if (action === "paste" && (typeof args.text !== "string" || args.text.length > 2_000_000 || !["text", "md", "html"].includes(String(args.format ?? "text")))) throw Error("paste requires text up to 2000000 characters and format text, md or html");
    let app = ["list_apps", "find_apps", "frontmost_window", "permission_status"].includes(action) ? "" : this.target(args.app, action === "start_app");
    if (app) args.app = app;
    if (action === "app_policy") return { text: JSON.stringify(computerAppPolicy(app)) };
    if (action === "watch_start") return this.watches.start(sessionId, turnId, args, () => this.turns.get(key) === true);
    let session = this.sessions.get(sessionId);
    if (session && session.turn !== turnId) {
      this.sessions.delete(sessionId); await session.runtime.close(); session = undefined;
    }
    // Do not start a service for an unobserved input, including after a crash.
    const previous = session?.observations.get(app);
    if (!read && !lifecycle) {
      const latest = session?.observations.get(app);
      if (!latest || input.observation_id !== latest.id) throw Error("Observe this app with computer_screenshot or computer_query in this turn and use its latest observation_id");
      if (latest.queryIndices && (args.element_index === undefined || !["click", "set_value", "select_text", "scroll", "perform_secondary_action"].includes(action))) throw Error("Query observations authorize matched element actions only; take a screenshot before keyboard or coordinate actions");
      if (latest.queryIndices && !latest.queryIndices.has(String(args.element_index))) throw Error("Element index was not returned by this query; query the control again");
      if (args.element_index !== undefined && !latest.ax) throw Error("Element actions require an accessibility observation");
      if ((args.x !== undefined || action === "drag") && !latest.pixels) throw Error("Coordinate actions require a fresh screenshot");
    }
    if (!session) {
      const runtime = await this.createRuntime();
      session = { runtime, turn: turnId, observations: new Map(), policies: new Set() };
      this.sessions.set(sessionId, session);
    }
    if (!this.turns.get(key)) throw Error("Computer Use turn ended during startup; no operation was dispatched");
    const targetedQueries = session.runtime.tools.some(tool => tool.name === "query");
    if (action === "get_app_state" && args.include_ax === undefined) args.include_ax = !targetedQueries;
    const native = session.runtime.tools.find(tool => tool.name === action);
    if (!native) throw Error(`AIClaw Computer Use does not support ${action} on ${process.platform}`);
    for (const key of Object.keys(args)) {
      if (!Object.hasOwn(native.inputSchema.properties, key)) throw Error(`AIClaw Computer Use does not support ${action}.${key}`);
    }
    for (const key of native.inputSchema.required ?? []) {
      if (args[key] === undefined || args[key] === null) throw Error(`Missing computer argument: ${key}`);
    }
    // The native connection owns one current window/element table. Switching
    // apps invalidates the prior app too, even within this chat and turn.
    if (app) session.observations.clear();
    let output: ComputerResult;
    try { output = observation(await session.runtime.call(action, args)); }
    catch (error) {
      // A timed-out proxy can still have dispatched input. Drop the connection
      // and every observation; reconnect only for an explicit later observation.
      // A native tool-error response is still a healthy IPC connection.
      if (!(error instanceof ComputerToolError)) { this.sessions.delete(sessionId); await session.runtime.close(); }
      throw Error(`${String(error)}${read ? "" : ". Outcome may be unknown; query the intended semantic control again with computer_query, or take a fresh screenshot. Do not automatically repeat input or submission."}`);
    }
    let launchUnverified = false;
    if (action === "start_app" && process.platform !== "darwin") {
      try {
        const receipt = JSON.parse(output.text);
        if (receipt.verified === true && typeof receipt.app === "string" && /^[1-9][0-9]*$/.test(receipt.app)) app = this.target(receipt.app);
        else launchUnverified = true;
      } catch { launchUnverified = true; }
      if (launchUnverified) output.text += "\nLaunch was requested but its running window is not verified. Discover the app and observe it explicitly; do not replay launch automatically.";
    }
    const remember = (state: ComputerResult, pixels: boolean, ax: boolean) => {
      if (pixels && !state.imageBase64) throw Error("AIClaw Computer Use returned no app screenshot. Grant Accessibility and Screen Recording to AIClaw Computer Use.app, then observe again. Do not guess coordinates.");
      let windowID: number | undefined;
      try { const meta = JSON.parse(state.text.split("\n")[0]!); if (Number.isSafeInteger(meta.window_id)) windowID = meta.window_id; } catch {}
      const id = randomUUID(); session.observations.set(app, { id, pixels: !!state.imageBase64, ax, screenshotHash: screenshotHash(state), windowID });
      state.text += `\nobservation_id: ${id}`;
    };
    if (action === "query") {
      let data;
      try { data = JSON.parse(output.text); } catch { throw Error("AIClaw Computer Use returned invalid query JSON"); }
      if (!data || !Array.isArray(data.matches) || data.matches.length > 100 || data.matches.some((match: { index: unknown }) => !match || !Number.isSafeInteger(match.index) || Number(match.index) < 0)) throw Error("AIClaw Computer Use returned invalid query matches");
      const id = randomUUID();
      session.observations.set(app, { id, pixels: false, ax: true, queryIndices: new Set(data.matches.map((match: { index: number }) => String(match.index))), screenshotHash: previous?.screenshotHash, windowID: Number.isSafeInteger(data.window_id) ? data.window_id : undefined });
      output.text += `\nobservation_id: ${id}\nUse only indexes returned by this query. Truncated searches do not establish that missing controls are absent.`;
    }
    if (action === "get_app_state") remember(output, args.include_screenshot !== false, args.include_ax !== false);
    if (!read && input.verify_after === false) {
      output.text += "\nPost-action screenshot deferred. Dispatch is not proof of completion. Old observations and indexes are invalid; query or observe again before the next input, and verify the intended effect at decision points and completion.";
    }
    if (!read && action !== "stop_app" && input.verify_after !== false && !launchUnverified) {
      try {
        let state: ComputerResult;
        for (let attempt = 0; ; attempt++) {
          try {
            state = observation(await session.runtime.call("get_app_state", { app, include_ax: !targetedQueries, ...(previous?.windowID && !lifecycle ? { window_id: previous.windowID } : {}) }));
            break;
          } catch (error) {
            // Event delivery is asynchronous. Retry only transient read-back,
            // never the input, while the target window settles.
            if (!(error instanceof ComputerToolError) || attempt >= 2 || !/no accessible windows|moved during capture|no longer capturable/.test(error.message)) throw error;
            await pause(100);
          }
        }
        remember(state, true, !targetedQueries);
        if (previous?.screenshotHash && previous.screenshotHash === screenshotHash(state)) state.text += "\nScreenshot unchanged from the previous observation. Dispatch is not proof of progress; query the intended control before deciding whether another input is appropriate.";
        if (targetedQueries) state.text += "\nThis is a screenshot-only verification. Use computer_query for the next semantic control; old element indexes are invalid.";
        output.text += `\nPost-action app observation (verify the intended change before continuing):\n${state.text}`;
        output.imageBase64 = state.imageBase64;
      } catch (error) {
        session.observations.delete(app);
        if (!(error instanceof ComputerToolError)) { this.sessions.delete(sessionId); await session.runtime.close(); }
        output.text += `\nInput was dispatched, but verification failed: ${String(error)}. Outcome is unknown; observe again. Do not automatically replay input.`;
        output.verificationFailed = true;
      }
    }
    if (["query", "get_app_state"].includes(action) && !session.policies.has(app)) {
      session.policies.add(app);
      const policy = computerAppPolicy(app);
      if (policy.policy_id !== "generic") output.text += `\napp_specific_instructions: ${JSON.stringify(policy)}`;
    }
    return output;
  }

  private target(value: unknown, launching = false): string {
    if (typeof value !== "string" || !value.trim()) throw Error("An explicit app identifier is required");
    const app = value.trim();
    if (launching && process.platform !== "darwin" && /^launch:[a-z0-9_.-]+$/i.test(app)) return app;
    if (app === String(process.pid)) throw Error("Refusing to operate AIClaw or its approval controls");
    const leaf = basename(app.replaceAll("\\", "/")).toLowerCase();
    if (/^(aiclaw|electron|aiclawcomputeruse|aiclaw computer use)(\.app|\.exe)?$/.test(leaf) || /^(com\.github\.chowyu12\.aiclaw|com\.github\.chowyu12\.aiclaw\.computer-use|com\.electron|com\.github\.electron)(\.|$)/i.test(app)) {
      throw Error("Refusing to operate AIClaw, its helper or approval controls");
    }
    if (process.platform === "darwin" && !/^[a-z0-9-]+(?:\.[a-z0-9-]+)+$/i.test(app)) throw Error("Use the exact bundle identifier from computer_list_apps; fuzzy names and paths are not accepted");
    if (process.platform !== "darwin" && (!/^[1-9][0-9]*$/.test(app) || !Number.isSafeInteger(Number(app)))) throw Error("Use the exact app PID from computer_list_apps; fuzzy names and paths are not accepted");
    return app;
  }

  async finishTurn(sessionId: string, turnId: string): Promise<void> {
    const key = JSON.stringify([sessionId, turnId]);
    if (this.turns.has(key)) this.turns.set(key, false);
    await this.watches.stop(sessionId, turnId);
    const operation = this.queue.then(async () => {
      const session = this.sessions.get(sessionId);
      if (session?.turn === turnId) { this.sessions.delete(sessionId); await session.runtime.close(); }
    });
    this.queue = operation.catch(() => {}); await operation;
  }

  async releaseSession(sessionId: string): Promise<void> {
    for (const key of this.turns.keys()) if (JSON.parse(key)[0] === sessionId) this.turns.set(key, false);
    await this.watches.releaseSession(sessionId);
    const operation = this.queue.then(async () => {
      const session = this.sessions.get(sessionId); this.sessions.delete(sessionId);
      for (const key of this.turns.keys()) if (JSON.parse(key)[0] === sessionId) this.turns.delete(key);
      await session?.runtime.close();
    });
    this.queue = operation.catch(() => {}); await operation;
  }

  async close(): Promise<void> {
    for (const key of this.turns.keys()) this.turns.set(key, false);
    await this.watches.close();
    await this.queue;
    const sessions = [...this.sessions.values()]; this.sessions.clear();
    await Promise.all(sessions.map(session => session.runtime.close()));
    this.turns.clear();
  }
}
