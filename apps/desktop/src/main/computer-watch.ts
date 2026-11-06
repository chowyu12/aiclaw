import { createHash, randomUUID } from "node:crypto";
import type { ComputerResult } from "@aiclaw/agent-client";
import type { ComputerRuntime, ComputerToolResult } from "./computer-runtime.js";

type Snapshot = { sequence: number; metadata: Record<string, unknown>; lines: string[]; image: string; hash: string };
type Watch = {
  id: string; session: string; turn: string; app: string; interval: number; runtime: ComputerRuntime;
  current: Snapshot; previous?: Snapshot; timer?: ReturnType<typeof setTimeout>; idle?: ReturnType<typeof setTimeout>;
  native?: { id: string; sequence: number };
  stopped: boolean; failure?: Error; waiters: Set<() => void>;
};
const IDLE_MS = 60_000;
function integer(value: unknown, fallback: number, minimum: number, maximum: number): number {
  const number = value ?? fallback;
  if (!Number.isSafeInteger(number) || Number(number) < minimum || Number(number) > maximum) throw Error(`Expected integer from ${minimum} to ${maximum}`);
  return Number(number);
}
function snapshot(result: ComputerToolResult, app: string, sequence: number): Snapshot {
  const text = result.content?.filter(item => item.type === "text").map(item => item.text ?? "").join("\n");
  if (result.isError || !text) throw Error(text || "Empty observation subscription result");
  const [header, ...tree] = text.split("\n");
  const metadata = JSON.parse(header!) as Record<string, unknown>;
  if (metadata.app !== app || !Number.isSafeInteger(metadata.pid) || !Number.isSafeInteger(metadata.window_id)) throw Error("Observation subscription target is invalid");
  const image = result.content.find(item => item.type === "image");
  if (image?.mimeType !== "image/png" || !image.data) throw Error("Observation subscription screenshot is missing");
  // Connection-local AX handles change each sample and must never become action IDs.
  const lines = tree.filter(Boolean).map(line => line.replace(/^(\s*)\[\d+\] /, "$1"));
  return { sequence, metadata, lines, image: image.data, hash: createHash("sha256").update(image.data).digest("hex") };
}
function splice(before: string[], after: string[]) {
  let start = 0, end = 0;
  while (start < before.length && start < after.length && before[start] === after[start]) start++;
  while (end < before.length - start && end < after.length - start && before[before.length - end - 1] === after[after.length - end - 1]) end++;
  return { start, delete_count: before.length - start - end, lines: after.slice(start, after.length - end) };
}

/** Bounded, in-memory subscriptions. A separate native connection prevents
 * sampling from invalidating the caller's action observations or element handles.
 */
export class ComputerWatches {
  private watches = new Map<string, Watch>();
  private readonly createRuntime: () => Promise<ComputerRuntime>;
  private readonly changed: () => void;
  private readonly failed: () => void;
  constructor(createRuntime: () => Promise<ComputerRuntime>, changed: () => void = () => {}, failed: () => void = () => {}) { this.createRuntime = createRuntime; this.changed = changed; this.failed = failed; }
  get size(): number { return this.watches.size; }
  private key(session: string, turn: string) { return JSON.stringify([session, turn]); }
  private touch(watch: Watch) {
    clearTimeout(watch.idle);
    watch.idle = setTimeout(() => { void this.closeWatch(watch); }, IDLE_MS);
    watch.idle.unref?.();
  }
  private signal(watch: Watch) { for (const wake of watch.waiters) wake(); watch.waiters.clear(); }
  private async closeWatch(watch: Watch) {
    watch.stopped = true; clearTimeout(watch.timer); clearTimeout(watch.idle); this.signal(watch);
    if (this.watches.get(this.key(watch.session, watch.turn)) === watch) this.watches.delete(this.key(watch.session, watch.turn));
    this.changed();
    await watch.runtime.close();
  }
  private poll(watch: Watch) {
    watch.timer = setTimeout(async () => {
      try {
        const result = watch.native
          ? await watch.runtime.nextCapture!({ capture_id: watch.native.id, after_sequence: watch.native.sequence, wait_ms: 10_000 })
          : await watch.runtime.call("get_app_state", { app: watch.app, window_id: watch.current.metadata.window_id, include_ax: true, include_screenshot: true, text_limit: "max" });
        if (watch.stopped) return;
        const next = snapshot(result, watch.app, watch.current.sequence + 1);
        if (watch.native) {
          const sequence = next.metadata.capture_sequence;
          if (next.metadata.capture_id !== watch.native.id || !Number.isSafeInteger(sequence) || Number(sequence) < watch.native.sequence) throw Error("Native capture identity or sequence changed");
          if (sequence === watch.native.sequence) { this.poll(watch); return; }
          watch.native.sequence = Number(sequence);
          next.sequence = Number(sequence);
        }
        if (next.metadata.pid !== watch.current.metadata.pid || next.metadata.window_id !== watch.current.metadata.window_id || next.metadata.process_start_time !== watch.current.metadata.process_start_time) throw Error("Observed process or window changed; restart the subscription explicitly");
        if (next.hash !== watch.current.hash || JSON.stringify(next.metadata) !== JSON.stringify(watch.current.metadata) || JSON.stringify(next.lines) !== JSON.stringify(watch.current.lines)) {
          watch.previous = watch.current; watch.current = next; this.signal(watch);
        }
        this.poll(watch);
      } catch (error) {
        if (watch.stopped) return;
        watch.failure = error instanceof Error ? error : Error(String(error)); this.signal(watch);
        this.failed();
        await watch.runtime.close();
      }
    }, watch.native ? 0 : watch.interval);
    watch.timer.unref?.();
  }
  private event(watch: Watch, after: number): ComputerResult {
    const current = watch.current, previous = watch.previous;
    const reset = !previous || after !== previous.sequence;
    const imageChanged = reset || previous.hash !== current.hash;
    return { text: JSON.stringify({ watch_id: watch.id, sequence: current.sequence, base_sequence: reset ? null : previous.sequence, changed: true, reset, dropped_updates: Math.max(0, current.sequence - after - 1), metadata: current.metadata,
      ax: reset ? { lines: current.lines } : splice(previous.lines, current.lines), image_changed: imageChanged,
      sampling: watch.native ? "native" : "polling", interval_ms: watch.interval, expires_after_idle_ms: IDLE_MS,
      action_authority: false, instruction: "Subscription samples have no valid observation_id or element indexes. Query or observe explicitly before input." }), ...(imageChanged ? { imageBase64: current.image } : {}) };
  }
  async start(session: string, turn: string, args: Record<string, unknown>, active: () => boolean): Promise<ComputerResult> {
    const key = this.key(session, turn), existing = this.watches.get(key);
    const interval = integer(args.interval_ms, 1000, 500, 5000);
    if (existing) await this.closeWatch(existing);
    if (this.watches.size >= 4) throw Error("At most four desktop observation subscriptions may run concurrently");
    const runtime = await this.createRuntime();
    try {
      if (!active()) throw Error("Computer Use turn ended during subscription startup");
      if (runtime.nativeCapture && (!runtime.startCapture || !runtime.nextCapture || !runtime.stopCapture)) throw Error("Native capture capabilities are incomplete");
      const current = snapshot(runtime.nativeCapture
        ? await runtime.startCapture!({ app: args.app, ...(args.window_id !== undefined ? { window_id: args.window_id } : {}), interval_ms: interval })
        : await runtime.call("get_app_state", { app: args.app, ...(args.window_id !== undefined ? { window_id: args.window_id } : {}), include_ax: true, include_screenshot: true, text_limit: "max" }), String(args.app), 1);
      if (!active()) throw Error("Computer Use turn ended during subscription startup");
      let native: Watch["native"];
      if (runtime.nativeCapture) {
        if (typeof current.metadata.capture_id !== "string" || !current.metadata.capture_id || !Number.isSafeInteger(current.metadata.capture_sequence) || Number(current.metadata.capture_sequence) < 1) throw Error("Invalid native capture receipt");
        native = { id: current.metadata.capture_id, sequence: Number(current.metadata.capture_sequence) };
        current.sequence = native.sequence;
      }
      const watch: Watch = { id: randomUUID(), session, turn, app: String(args.app), interval, runtime, current, native, stopped: false, waiters: new Set() };
      this.watches.set(key, watch); this.touch(watch); this.poll(watch);
      this.changed();
      return this.event(watch, 0);
    } catch (error) { await runtime.close(); throw error; }
  }
  async next(session: string, turn: string, args: Record<string, unknown>): Promise<ComputerResult> {
    const watch = this.watches.get(this.key(session, turn));
    if (!watch || watch.id !== args.watch_id) throw Error("Observation subscription is absent or belongs to another chat/turn");
    const after = integer(args.after_sequence, 0, 0, watch.current.sequence), wait = integer(args.wait_ms, 5000, 0, 10_000);
    this.touch(watch);
    if (watch.current.sequence <= after && !watch.failure && wait > 0) await new Promise<void>(resolve => {
      const wake = () => { clearTimeout(timer); watch.waiters.delete(wake); resolve(); };
      const timer = setTimeout(wake, wait); watch.waiters.add(wake);
    });
    if (watch.failure) throw Error(`Observation subscription failed: ${watch.failure.message}`);
    if (watch.stopped) throw Error("Observation subscription stopped or expired");
    if (watch.current.sequence > after) return this.event(watch, after);
    return { text: JSON.stringify({ watch_id: watch.id, sequence: watch.current.sequence, changed: false, sampling: watch.native ? "native" : "polling", interval_ms: watch.interval }) };
  }
  async stop(session: string, turn: string, args?: Record<string, unknown>): Promise<ComputerResult> {
    const watch = this.watches.get(this.key(session, turn));
    if (watch && args && watch.id !== args.watch_id) throw Error("Observation subscription belongs to another chat/turn");
    if (watch) await this.closeWatch(watch);
    return { text: JSON.stringify({ stopped: true }) };
  }
  async releaseSession(session: string) { await Promise.all([...this.watches.values()].filter(watch => watch.session === session).map(watch => this.closeWatch(watch))); }
  async close() { await Promise.all([...this.watches.values()].map(watch => this.closeWatch(watch))); }
}
