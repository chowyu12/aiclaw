// The supervisor for the Go core.
//
// This is the complexity the Electron architecture adds over Wails: the
// application's behaviour now lives in a second process that can fail to
// start, die mid-turn, or outlive its parent. Every one of those has to be
// handled explicitly. See docs/design/electron-migration.md.

import { type ChildProcessWithoutNullStreams, spawn } from "node:child_process";
import { createInterface, type Interface } from "node:readline";

/** One call to the core. */
export interface CoreRequest {
  id: string;
  command: string;
  params?: unknown[];
}

/** The core's terminal answer to a request. */
export interface CoreResponse {
  id: string;
  ok: boolean;
  result?: unknown;
  error?: string;
}

/** An out-of-band event, tagged with the request that produced it. */
export interface CoreEvent {
  id?: string;
  event: string;
  data?: unknown[];
}

/** A request from the core for something only the host can do. */
export interface HostCall {
  id: string;
  host: string;
  params?: unknown;
}

export interface Handshake {
  ready: true;
  protocol: number;
  commands: string[];
}

/** The protocol revision this shell speaks. */
export const PROTOCOL_VERSION = 1;

export type SidecarState =
  | "starting"
  | "ready"
  | "restarting"
  | "failed"
  | "stopped";

export interface SidecarHooks {
  /** Forwards an event to the interface. */
  onEvent(event: CoreEvent): void;
  /** Answers a host call; the returned value is sent back to the core. */
  onHostCall(call: HostCall): Promise<unknown>;
  /** Reports a state change so the interface can show the truth. */
  onState(state: SidecarState, detail?: string): void;
  /** Receives the core's stderr, which is logging rather than protocol. */
  onLog?(line: string): void;
}

export interface SidecarOptions {
  /** Path to the aiclaw-core executable. */
  executable: string;
  /** Data directory passed to the core. */
  dataDir?: string;
  hooks: SidecarHooks;
  /** Overridable for tests. */
  spawnFn?: typeof spawn;
  /** How long to wait for the handshake before treating the start as failed. */
  handshakeTimeoutMs?: number;
}

const HANDSHAKE_TIMEOUT_MS = 10_000;
const RESTART_BASE_MS = 1_000;
const RESTART_MAX_MS = 30_000;
/** Give up after this many consecutive failures rather than reconnecting
 *  forever against, say, a corrupt database. */
const MAX_RESTARTS = 5;
const SHUTDOWN_GRACE_MS = 5_000;
const SIGKILL_DELAY_MS = 2_000;

interface Pending {
  resolve(response: CoreResponse): void;
  reject(error: Error): void;
}

/**
 * Sidecar owns one Go core process: starting it, routing messages, restarting
 * it when it dies, and making sure it does not outlive this process.
 */
export class Sidecar {
  private child?: ChildProcessWithoutNullStreams;
  private reader?: Interface;
  private state: SidecarState = "stopped";
  private readonly pending = new Map<string, Pending>();
  private nextId = 0;
  private restarts = 0;
  private restartTimer?: NodeJS.Timeout;
  private commands: string[] = [];
  private stopping = false;

  constructor(private readonly options: SidecarOptions) {}

  /** The commands the core reported in its handshake. */
  get availableCommands(): readonly string[] {
    return this.commands;
  }

  get currentState(): SidecarState {
    return this.state;
  }

  /** Starts the core and resolves once it has handshaken. */
  async start(): Promise<Handshake> {
    this.stopping = false;
    return this.launch();
  }

  private async launch(): Promise<Handshake> {
    this.setState("starting");
    const spawnFn = this.options.spawnFn ?? spawn;
    const args = this.options.dataDir ? ["-data-dir", this.options.dataDir] : [];
    const child = spawnFn(this.options.executable, args, {
      stdio: ["pipe", "pipe", "pipe"],
    }) as ChildProcessWithoutNullStreams;
    this.child = child;

    const handshake = new Promise<Handshake>((resolve, reject) => {
      const timeout = setTimeout(() => {
        reject(
          new Error(
            `the core did not report ready within ${
              this.options.handshakeTimeoutMs ?? HANDSHAKE_TIMEOUT_MS
            }ms`,
          ),
        );
      }, this.options.handshakeTimeoutMs ?? HANDSHAKE_TIMEOUT_MS);

      this.reader = createInterface({ input: child.stdout });
      this.reader.on("line", (line) => {
        let message: unknown;
        try {
          message = JSON.parse(line);
        } catch {
          // A line that is not JSON means the pipe is desynchronised; report
          // it rather than silently dropping protocol traffic.
          this.options.hooks.onLog?.(`unreadable line from core: ${line}`);
          return;
        }
        const settled = this.handle(message);
        if (settled) {
          clearTimeout(timeout);
          resolve(settled);
        }
      });
    });

    child.stderr.on("data", (chunk: Buffer) => {
      for (const line of chunk.toString().split("\n")) {
        if (line.trim()) this.options.hooks.onLog?.(line);
      }
    });

    child.on("exit", (code, signal) => this.onExit(code, signal));
    child.on("error", (error) => {
      this.failAllPending(new Error(`core process error: ${error.message}`));
      this.setState("failed", error.message);
    });

    try {
      const result = await handshake;
      if (result.protocol !== PROTOCOL_VERSION) {
        // A mismatched core is worse than no core: it would answer some calls
        // and silently misbehave on others.
        throw new Error(
          `core speaks protocol ${result.protocol}, this shell speaks ${PROTOCOL_VERSION}`,
        );
      }
      this.commands = result.commands;
      this.restarts = 0;
      this.setState("ready");
      return result;
    } catch (error) {
      this.kill();
      this.setState("failed", (error as Error).message);
      throw error;
    }
  }

  /** Routes one decoded message. Returns the handshake when that is what it was. */
  private handle(message: unknown): Handshake | undefined {
    if (!message || typeof message !== "object") return undefined;
    const record = message as Record<string, unknown>;

    if (record.ready === true) return record as unknown as Handshake;

    if (typeof record.host === "string") {
      void this.answerHostCall(record as unknown as HostCall);
      return undefined;
    }
    if (typeof record.event === "string") {
      this.options.hooks.onEvent(record as unknown as CoreEvent);
      return undefined;
    }
    if (typeof record.ok === "boolean" && typeof record.id === "string") {
      const waiter = this.pending.get(record.id);
      this.pending.delete(record.id);
      waiter?.resolve(record as unknown as CoreResponse);
      return undefined;
    }
    this.options.hooks.onLog?.(`unrecognised message from core: ${JSON.stringify(record)}`);
    return undefined;
  }

  private async answerHostCall(call: HostCall): Promise<void> {
    try {
      const result = await this.options.hooks.onHostCall(call);
      this.send({ id: call.id, reply: { ok: true, result } });
    } catch (error) {
      this.send({
        id: call.id,
        reply: { ok: false, error: (error as Error).message },
      });
    }
  }

  /** Invokes a command on the core. */
  invoke(command: string, params: unknown[] = []): Promise<unknown> {
    if (this.state !== "ready") {
      return Promise.reject(
        new Error(`the core is ${this.state}; try again once it is ready`),
      );
    }
    const id = `r${++this.nextId}`;
    return new Promise((resolve, reject) => {
      this.pending.set(id, {
        resolve: (response) => {
          if (response.ok) resolve(response.result);
          else reject(new Error(response.error ?? `${command} failed`));
        },
        reject,
      });
      try {
        this.send({ id, command, params });
      } catch (error) {
        this.pending.delete(id);
        reject(error as Error);
      }
    });
  }

  private send(message: unknown): void {
    const child = this.child;
    if (!child || child.stdin.destroyed) {
      throw new Error("the core is not running");
    }
    child.stdin.write(`${JSON.stringify(message)}\n`);
  }

  private onExit(code: number | null, signal: NodeJS.Signals | null): void {
    const reason = signal ? `signal ${signal}` : `exit code ${code}`;
    this.failAllPending(new Error(`the core stopped (${reason})`));
    this.reader?.close();
    this.reader = undefined;
    this.child = undefined;

    if (this.stopping) {
      this.setState("stopped");
      return;
    }
    if (this.restarts >= MAX_RESTARTS) {
      this.setState(
        "failed",
        `the core stopped ${this.restarts} times in a row (${reason})`,
      );
      return;
    }
    const delay = Math.min(
      RESTART_BASE_MS * 2 ** this.restarts,
      RESTART_MAX_MS,
    );
    this.restarts += 1;
    this.setState("restarting", `${reason}; retrying in ${delay}ms`);
    this.restartTimer = setTimeout(() => {
      if (this.stopping) return;
      // A failed relaunch lands back in onExit, which keeps the backoff going.
      this.launch().catch(() => {});
    }, delay);
  }

  /**
   * Stops the core: asks politely, then escalates.
   *
   * Closing stdin is the polite ask — the core watches for end of input and
   * exits, which is also what protects against orphans when this process is
   * killed outright and never runs this method at all.
   */
  async stop(): Promise<void> {
    this.stopping = true;
    if (this.restartTimer) clearTimeout(this.restartTimer);
    const child = this.child;
    if (!child) {
      this.setState("stopped");
      return;
    }
    const exited = new Promise<void>((resolve) => child.once("exit", () => resolve()));
    try {
      child.stdin.end();
    } catch {
      // Already gone; the escalation below still applies.
    }
    const graceful = await Promise.race([
      exited.then(() => true),
      new Promise<boolean>((resolve) => setTimeout(() => resolve(false), SHUTDOWN_GRACE_MS)),
    ]);
    if (graceful) {
      this.setState("stopped");
      return;
    }
    child.kill("SIGTERM");
    const terminated = await Promise.race([
      exited.then(() => true),
      new Promise<boolean>((resolve) => setTimeout(() => resolve(false), SIGKILL_DELAY_MS)),
    ]);
    if (!terminated) child.kill("SIGKILL");
    this.setState("stopped");
  }

  private kill(): void {
    try {
      this.child?.kill("SIGKILL");
    } catch {
      // Nothing to kill.
    }
    this.child = undefined;
  }

  private failAllPending(error: Error): void {
    for (const waiter of this.pending.values()) waiter.reject(error);
    this.pending.clear();
  }

  private setState(state: SidecarState, detail?: string): void {
    this.state = state;
    this.options.hooks.onState(state, detail);
  }
}
