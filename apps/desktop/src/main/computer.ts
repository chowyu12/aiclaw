import type {
  ComputerRequestParams,
  ComputerResult,
} from "@aiclaw/agent-client";
import {
  createComputerRuntime,
  type ComputerRuntime,
  type ComputerToolResult,
} from "./computer-runtime.js";
import { setTimeout as pause } from "node:timers/promises";
import { createHash } from "node:crypto";
import { backgroundCuaArguments, cuaToolSpecs } from "./cua-policy.js";
import type {
  ComputerControlAction,
  ComputerControlActivity,
  ComputerControlState,
  ComputerDiagnostics,
} from "../shared/computer-control.js";
type Snapshot = { scope:string; fingerprint:string };
type Session = { runtime: ComputerRuntime; turn: string; observed: Map<string,Snapshot>; lastAction?: { key:string; target:string; baseline?:Snapshot; changed:boolean } };
class ComputerRefusal extends Error {}
class ComputerDriverError extends Error {}
async function beforeDeadline<T>(promise:Promise<T>, remainingMs:number):Promise<T> {
  let timer:ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([promise,new Promise<never>((_,reject)=>{
      timer=setTimeout(()=>reject(Error("Window discovery timed out; the driver request was cancelled.")),Math.max(1,remainingMs));
    })]);
  } finally { if(timer) clearTimeout(timer); }
}
function stateFingerprint(data: Record<string,unknown>): string {
  const volatile=new Set(["snapshot_id","element_token","invalidated_snapshot_ids","since_status","capture_id","screenshot","screenshot_base64"]);
  function semantic(value:unknown):unknown {
    if(Array.isArray(value)) return value.map(semantic);
    if(value && typeof value === "object") return Object.fromEntries(Object.entries(value).filter(([key])=>!volatile.has(key)).sort(([a],[b])=>a.localeCompare(b)).map(([key,item])=>[key,semantic(item)]));
    return value;
  }
  return createHash("sha256").update(JSON.stringify(semantic(data))).digest("hex");
}
function stableAction(value:unknown):unknown {
  if(Array.isArray(value)) return value.map(stableAction);
  if(value && typeof value === "object") return Object.fromEntries(Object.entries(value).sort(([a],[b])=>a.localeCompare(b)).map(([key,item])=>[key,stableAction(item)]));
  return value;
}
function resultText(action:string, result:ComputerToolResult, output:ComputerResult):ComputerResult {
  if (!["list_windows","get_window_state","wait_new_window","launch_app"].includes(action) || !result.structuredContent) return output;
  const data={...result.structuredContent};
  for(const key of ["screenshot","screenshot_base64","image","image_base64"]) delete data[key];
  // The original readable text is retained; callers can parse identifiers and tokens as JSON.
  return {...output,text:JSON.stringify({text:output.text,data})};
}
function backgroundRefused(result: ComputerToolResult): boolean {
  const text = result.content?.filter(item => item.type === "text").map(item => item.text ?? "").join("\n") ?? "";
  const detail = result.structuredContent as {escalation?:{recommended?:string};refusal?:{escalation?:{recommended?:string}}} | undefined;
  return !!result.isError && (detail?.escalation?.recommended === "foreground" || detail?.refusal?.escalation?.recommended === "foreground" || /disabled while the app is in the background|background_unavailable|Retry (?:this action )?with delivery_mode:"foreground"/i.test(text));
}
function observation(result: ComputerToolResult): ComputerResult {
  if (!Array.isArray(result?.content)) throw Error("Invalid Cua result");
  const text = result.content
    .filter((item) => item.type === "text")
    .map((item) => item.text ?? "")
    .join("\n");
  if (result.isError) {
    throw new ComputerDriverError(text || "Cua refused the operation");
  }
  if (!text.trim()) throw Error("Empty Cua result; outcome unknown");
  const image = result.content.find((item) => item.type === "image");
  if (image && (image.mimeType !== "image/png" || !image.data))
    throw Error("Invalid Cua PNG screenshot");
  return { text, ...(image ? { imageBase64: image.data } : {}) };
}
/** Turn leases own Driver snapshots and independent AIClaw cursors. Host stop
 * revokes the lease immediately; it never queues behind a pending batch. */
export class ComputerController {
  private sessions = new Map<string, Session>();
  private lastActions = new Map<string, NonNullable<Session["lastAction"]>>();
  private turns = new Map<string, boolean>();
  private blockedTurns = new Map<string, string>();
  private emptyReads = new Map<string, number>();
  private connections = new Set<ComputerRuntime>();
  private listeners = new Set<(state: ComputerControlState) => void>();

  private async callWithForegroundFallback(runtime:ComputerRuntime, action:string, args:Record<string,unknown>, check:()=>void):Promise<ComputerToolResult> {
    const result=await runtime.call(action,args);
    const text=result.content?.filter(item=>item.type==="text").map(item=>item.text??"").join("\n") ?? "";
    const refused=backgroundRefused(result);
    if (/screen_locked/i.test(text)) return result;
    if(!refused || action==="run_steps" || args.delivery_mode!=="background") return result;
    check();
    if(this.active) {this.active.foreground=true;this.publish();}
    const mark=(response:ComputerToolResult):ComputerToolResult => ({...response,content:[{type:"text",text:"Temporary foreground execution was used."},...response.content]});
    if(action!=="invoke_menu") return mark(await runtime.call(action,{...args,delivery_mode:"foreground"}));

    // macOS menu actions require the application itself to be foreground.
    // Resolve the user's current window before changing focus; restore only if
    // the target still has focus, so a later user switch is never overridden.
    let activePid:number|undefined, priorWindow:number|undefined, warning="";
    try {
      const apps=await runtime.call("list_apps",{});
      observation(apps);
      const active=(apps.structuredContent?.apps as Array<Record<string,unknown>> | undefined)?.find(app=>app.active===true);
      if(Number.isSafeInteger(active?.pid)) activePid=Number(active?.pid);
      if(activePid && activePid!==args.pid) {
        const windows=await runtime.call("list_windows",{pid:activePid});
        observation(windows);
        const candidates=(windows.structuredContent?.windows as Array<Record<string,unknown>> | undefined)?.filter(w=>w.layer===0&&w.is_on_screen===true&&Number.isSafeInteger(w.window_id)&&typeof w.z_index==="number") ?? [];
        candidates.sort((a,b)=>Number(b.z_index)-Number(a.z_index));
        priorWindow=candidates[0]?.window_id as number | undefined;
      }
      if(!activePid || activePid!==args.pid && !priorWindow) warning=" The previous foreground window could not be identified and may not be restored.";
    } catch {
      warning=" The previous foreground window could not be identified and may not be restored.";
    }
    check();
    let response:ComputerToolResult;
    try {
      const activated=await runtime.call("bring_to_front",{pid:args.pid,window_id:args.window_id});
      observation(activated);
      check();
      response=await runtime.call(action,{...args,delivery_mode:"foreground"});
      // AppKit may add an ellipsis to a menu label when the app becomes
      // active. Retry only a unique matching label after the driver confirms
      // the original path was not found, before any menu item was invoked.
      const message=response.content?.filter(item=>item.type==="text").map(item=>item.text??"").join("\n") ?? "";
      if(response.isError && /path segment .* was not found under /i.test(message) && Array.isArray(args.path)) {
        const items=message.split("Items there:")[1]?.split("\n")[0] ?? "";
        const labels=[...items.matchAll(/"([^"\n]+)"/g)].map(match=>match[1]!);
        const normalize=(label:string)=>label.trim().replace(/(?:…|\.\.\.)$/,"");
        const path=args.path as string[], original=path[path.length-1];
        const matches=labels.filter(label=>original && label!==original && normalize(label)===normalize(original));
        if(matches.length===1) {
          check();
          response=await runtime.call(action,{...args,path:[...path.slice(0,-1),matches[0]],delivery_mode:"foreground"});
        }
      }
    } finally {
      if(priorWindow && activePid) {
        const restore=async (connection:ComputerRuntime) => {
          const now=await connection.call("list_apps",{});
          observation(now);
          const current=(now.structuredContent?.apps as Array<Record<string,unknown>> | undefined)?.find(app=>app.active===true);
          if(current?.pid===args.pid) {
            const targetWindows=await connection.call("list_windows",{pid:args.pid});
            observation(targetWindows);
            const visible=(targetWindows.structuredContent?.windows as Array<Record<string,unknown>> | undefined)?.filter(w=>w.layer===0&&w.is_on_screen===true&&Number.isSafeInteger(w.window_id)&&typeof w.z_index==="number") ?? [];
            visible.sort((a,b)=>Number(b.z_index)-Number(a.z_index));
            if(visible[0]?.window_id===args.window_id) observation(await connection.call("bring_to_front",{pid:activePid,window_id:priorWindow}));
            else warning=" The active window changed during execution; the previous window was not restored.";
          }
          else if(!current) warning=" The previous foreground window could not be verified and was not restored.";
        };
        try { await restore(runtime); }
        catch {
          try {
            const recovery=await this.createRuntime();
            try { await restore(recovery); } finally { await recovery.close(); }
          } catch { warning=" The previous foreground window could not be restored."; }
        }
      }
    }
    const output=mark(response!);
    if(warning) output.content.unshift({type:"text",text:warning.trim()});
    return output;
  }
  private queue: Promise<unknown> = Promise.resolve();
  private generation = 0;
  private mode: ComputerControlState["mode"] = "ready";
  private active: ComputerControlActivity | null = null;
  private readonly createRuntime: () => Promise<ComputerRuntime>;
  private readonly permissionSetup: () => Promise<void>;
  constructor(
    createRuntime: () => Promise<ComputerRuntime> = createComputerRuntime,
    permissionSetup: () => Promise<void> = async () => {
      if (process.platform === "darwin") {
        const { requestMacOSPermissions } = await import(
          "@trycua/cua-driver/electron"
        );
        requestMacOSPermissions();
      }
    },
  ) {
    this.createRuntime = createRuntime;
    this.permissionSetup = permissionSetup;
  }
  getStatus(): ComputerControlState {
    return {
      mode: this.mode,
      active: this.active ? { ...this.active } : null,
      watching: 0,
    };
  }
  subscribe(listener: (state: ComputerControlState) => void): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }
  private publish(): void {
    for (const listener of this.listeners) listener(this.getStatus());
  }
  affectedSessions(): string[] {
    return [
      ...new Set(
        [...this.turns]
          .filter(([, active]) => active)
          .map(([key]) => JSON.parse(key)[0] as string),
      ),
    ];
  }
  private check(generation: number, key?: string): void {
    if (this.mode !== "ready" || generation !== this.generation)
      throw Error(
        "Computer Use was paused or stopped; resume explicitly and observe again. No input was replayed.",
      );
    if (key && !this.turns.get(key))
      throw Error(
        "Computer Use turn has ended; no queued operation was dispatched",
      );
  }
  private async connect(
    generation: number,
    key?: string,
  ): Promise<ComputerRuntime> {
    const runtime = await this.createRuntime();
    try {
      this.check(generation, key);
    } catch (error) {
      await runtime.close();
      throw error;
    }
    this.connections.add(runtime);
    const close = runtime.close.bind(runtime);
    let closing: Promise<void> | undefined;
    runtime.close = () => {
      this.connections.delete(runtime);
      return (closing ??= close());
    };
    return runtime;
  }
  async setControl(
    action: ComputerControlAction,
  ): Promise<ComputerControlState> {
    if (!["pause", "resume", "stop"].includes(action))
      throw Error("Unknown desktop control action");
    if (action === "resume") {
      if (this.mode !== "ready") {
        this.mode = "ready";
        this.active = null;
        this.publish();
      }
      return this.getStatus();
    }
    this.mode = action === "pause" ? "paused" : "stopped";
    this.generation++;
    if (action === "stop")
      for (const key of this.turns.keys()) this.turns.set(key, false);
    this.sessions.clear();
    this.active = null;
    this.queue = Promise.resolve();
    this.publish();
    await Promise.allSettled(
      [...this.connections].map((runtime) => runtime.close()),
    );
    return this.getStatus();
  }
  perform(request: ComputerRequestParams): Promise<ComputerResult> {
    // Reject unsafe arguments before creating any driver or queuing side effects.
    let args: Record<string, unknown>;
    try {
      args = backgroundCuaArguments(request.action, request.arguments);
      this.check(this.generation);
    } catch (error) {
      return Promise.reject(error);
    }
    if (!request.sessionId || !request.turnId)
      return Promise.reject(Error("Computer session and turn are required"));
    const generation = this.generation,
      key = JSON.stringify([request.sessionId, request.turnId]);
    if (request.backgroundOnly && request.action === "run_steps") args.foreground_fallback = false;
    if (!this.turns.has(key)) this.turns.set(key, true);
    const operation = this.queue.then(async () => {
      this.check(generation, key);
      const blocked = this.blockedTurns.get(key);
      if (blocked) throw new ComputerRefusal(blocked);
      const target=JSON.stringify([args.pid,args.window_id]);
      const mutating=["click","double_click","right_click","hotkey","invoke_menu","run_steps"].includes(request.action);
      const identityArgs={...args};
      if(request.action === "hotkey" || request.action === "press_key") delete identityArgs.element_token;
      const actionKey=JSON.stringify(stableAction([request.action,identityArgs]));
      const previous=this.lastActions.get(key);
      if(mutating && previous?.key===actionKey && !previous.changed)
        throw new ComputerRefusal("The same input was already dispatched and no changed state was verified. Stop and inspect the target; do not repeat it automatically.");
      let session = this.sessions.get(request.sessionId);
      if (session?.turn !== request.turnId) {
        await session?.runtime.close();
        session = undefined;
      }
      if (!session) {
        const runtime = await this.connect(generation, key);
        session = { runtime, turn: request.turnId, observed:new Map() };
        this.sessions.set(request.sessionId, session);
      }
      this.check(generation, key);
      const activity: ComputerControlActivity = {
        sessionId: request.sessionId,
        turnId: request.turnId,
        action: request.action,
        app:
          typeof args.bundle_id === "string"
            ? args.bundle_id
            : `PID ${args.pid ?? ""}`,
        pid: typeof args.pid === "number" ? args.pid : undefined,
        windowId:
          typeof args.window_id === "number" ? args.window_id : undefined,
      };
      this.active = activity;
      this.publish();
      try {
        let result:ComputerToolResult;
        if (request.action === "wait_new_window") {
          const excluded = new Set(args.exclude_window_ids as number[]), deadline = Date.now() + Number(args.timeout_ms ?? 3000);
          let candidates: unknown[] = [];
          do {
            this.check(generation,key);
            const snapshot = await beforeDeadline(session.runtime.call("list_windows",{pid:args.pid}),deadline-Date.now());
            observation(snapshot);
            const windows = snapshot.structuredContent?.windows;
            if (!Array.isArray(windows)) throw Error("Cua did not return structured window data");
            candidates = windows.filter((value: unknown) => {
              if (!value || typeof value !== "object") return false;
              const window = value as Record<string,unknown>;
              return Number.isSafeInteger(window.window_id) && !excluded.has(Number(window.window_id)) && window.layer === 0 && typeof window.title === "string" && !!window.title.trim();
            });
            if(candidates.length) break;
            if(Date.now() < deadline) await pause(Math.min(200,deadline-Date.now()));
          } while(Date.now() < deadline);
          result={content:[{type:"text",text:candidates.length ? "New window candidates found; confirm the target before acting." : "No new ordinary titled window appeared before the timeout. Do not repeat the action automatically."}],structuredContent:{windows:candidates,timed_out:!candidates.length}};
        } else {
          session.lastAction=this.lastActions.get(key);
          if(mutating) { session.lastAction={key:actionKey,target,baseline:session.observed.get(target),changed:false}; this.lastActions.set(key,session.lastAction); }
          result = request.backgroundOnly
            ? await session.runtime.call(request.action,args)
            : await this.callWithForegroundFallback(session.runtime,request.action,args,()=>this.check(generation,key));
          if (request.backgroundOnly && backgroundRefused(result)) {
            const reason = "Computer Use stopped: this action requires foreground operation, which the user prohibited. Do not retry, use shell alternatives, or perform further desktop actions; report the blocker.";
            this.blockedTurns.set(key, reason);
            throw new ComputerRefusal(reason);
          }
          if (request.action === "get_window_state" && !result.isError && args.query === undefined && args.since === undefined && args.include_accessibility_tree !== false && (Array.isArray(result.structuredContent?.elements) || typeof result.structuredContent?.total_element_count === "number")) {
            const empty = result.structuredContent?.total_element_count === 0 || (result.structuredContent?.total_element_count === undefined && Array.isArray(result.structuredContent?.elements) && result.structuredContent.elements.length === 0);
            const count = empty ? (this.emptyReads.get(key) ?? 0) + 1 : 0;
            this.emptyReads.set(key, count);
            if (count >= 3) {
              const reason = "Computer Use stopped: three consecutive full window reads returned no accessibility elements. The desktop may be locked or unavailable; stop retrying and report the limitation.";
              this.blockedTurns.set(key, reason);
              throw new ComputerRefusal(reason);
            }
          }
          if(request.action==="get_window_state" && result.structuredContent && !result.isError && args.since === undefined) {
            const scope=JSON.stringify([args.query??null,args.max_depth??null,args.max_elements??null,args.tree_format??null,args.include_accessibility_tree??null]);
            const snapshot={scope,fingerprint:stateFingerprint(result.structuredContent)};
            session.observed.set(target,snapshot);
            if(session.lastAction?.target===target && session.lastAction.baseline?.scope===scope && session.lastAction.baseline.fingerprint!==snapshot.fingerprint) session.lastAction.changed=true;
          }
        }
        let output = observation(result);
        if (result.structuredContent?.self_activation_suppressed === false)
          throw Error("The target app did not stay in the background; automation stopped. Do not automatically relaunch it.");
        this.check(generation, key);
        if (request.action === "run_steps" && result.structuredContent?.ok === false) output.verificationFailed = true;
        output=resultText(request.action,result,output);
        return output;
      } catch (error) {
        if (error instanceof ComputerDriverError && /screen_locked/i.test(error.message)) {
          const reason = "Computer Use stopped: the driver reported screen_locked (locked screen or screen saver). Ask the user to unlock before a new turn; do not wait or retry.";
          this.blockedTurns.set(key, reason);
          throw new ComputerRefusal(reason);
        }
        if (error instanceof ComputerRefusal) throw error;
        if (error instanceof ComputerDriverError && request.action !== "run_steps" && /element_token is stale/i.test(error.message)) {
          // A rejected stale token was not dispatched. The next fresh token may be used.
          this.lastActions.delete(key); session.lastAction=undefined; throw error;
        }
        if (this.sessions.get(request.sessionId) === session)
          this.sessions.delete(request.sessionId);
        await session.runtime.close();
        throw Error(
          `${String(error)}. Do not replay uncertain input; obtain a fresh window state before deciding what to do next.`,
        );
      } finally {
        if (this.active === activity) {
          this.active = null;
          this.publish();
        }
      }
    });
    this.queue = operation.catch(() => {});
    return operation;
  }
  // Cua owns its click-through agent cursor; the host no longer polls AX bounds
  // every 250 ms or draws a second competing target overlay.
  async windowBounds(
    _activity: ComputerControlActivity,
  ): Promise<NonNullable<ComputerControlActivity["frame"]> | null> {
    return null;
  }
  async diagnostics(): Promise<ComputerDiagnostics> {
    let runtime: ComputerRuntime | undefined;
    try {
      runtime = await this.createRuntime();
      const result = await runtime.call("check_permissions", { prompt: false });
      const data =
        result.structuredContent ?? JSON.parse(observation(result).text);
      return {
        platform: process.platform,
        available: true,
        accessibility:
          typeof data.accessibility === "boolean" ? data.accessibility : null,
        screenCapture:
          typeof data.screen_recording === "boolean"
            ? data.screen_recording
            : null,
        toolCount: cuaToolSpecs.length,
        message: "Cua Driver：仅后台窗口操作，使用独立 AIClaw 光标。",
      };
    } catch (error) {
      return {
        platform: process.platform,
        available: false,
        accessibility: null,
        screenCapture: null,
        toolCount: 0,
        message: `Cua Driver 不可用：${String(error)}`,
      };
    } finally {
      await runtime?.close();
    }
  }
  async showPermissionSetup(): Promise<ComputerDiagnostics> {
    await this.permissionSetup();
    return this.diagnostics();
  }
  async finishTurn(sessionId: string, turnId: string): Promise<void> {
    const key=JSON.stringify([sessionId, turnId]);
    this.turns.set(key, false);
    this.lastActions.delete(key);
    this.blockedTurns.delete(key);
    this.emptyReads.delete(key);
    const session = this.sessions.get(sessionId);
    if (session?.turn === turnId) {
      this.sessions.delete(sessionId);
      await session.runtime.close();
    }
    if (this.active?.sessionId === sessionId && this.active.turnId === turnId) {
      this.active = null;
      this.publish();
    }
  }
  async releaseSession(sessionId: string): Promise<void> {
    for (const key of this.turns.keys())
      if (JSON.parse(key)[0] === sessionId) {
        this.turns.set(key, false);
        this.lastActions.delete(key);
        this.blockedTurns.delete(key);
        this.emptyReads.delete(key);
      }
    const session = this.sessions.get(sessionId);
    this.sessions.delete(sessionId);
    await session?.runtime.close();
    if (this.active?.sessionId === sessionId) {
      this.active = null;
      this.publish();
    }
  }
  async close(): Promise<void> {
    await this.setControl("stop");
    this.turns.clear();
    this.lastActions.clear();
    this.blockedTurns.clear();
    this.emptyReads.clear();
  }
}
