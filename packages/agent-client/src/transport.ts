import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { EventEmitter } from "node:events";

/**
 * claw-agent 的线格式是「一行一个 JSON-RPC 2.0 帧」走 stdio。
 *
 * 帧分四种：我们发的请求（有 id + method）、它回的响应（有 id、无 method）、
 * 它发的通知（有 method、无 id）、它发的请求（有 id + method，要我们回——审批就是这个）。
 * 路由只按「有没有 id、有没有 method」判，不依赖 jsonrpc 字段。
 */

export type JsonValue =
  | null
  | boolean
  | number
  | string
  | JsonValue[]
  | { [key: string]: JsonValue };

export interface RpcError {
  code: number;
  message: string;
  data?: JsonValue;
}

interface IncomingFrame {
  id?: number | string;
  method?: string;
  params?: unknown;
  result?: unknown;
  error?: RpcError;
}

export interface TransportOptions {
  /** claw-agent 可执行文件路径。 */
  command: string;
  args?: string[];
  /** 子进程环境变量。模型 Key 只从这里注入，不落配置文件。 */
  env?: NodeJS.ProcessEnv;
  cwd?: string;
}

export interface TransportEvents {
  notification: (method: string, params: unknown) => void;
  /** 对方发来的请求，必须回响应，否则那一轮会一直等到超时。 */
  serverRequest: (id: number | string, method: string, params: unknown) => void;
  stderr: (chunk: string) => void;
  exit: (code: number | null, signal: NodeJS.Signals | null) => void;
  malformed: (line: string, error: Error) => void;
}

export declare interface AgentTransport {
  on<E extends keyof TransportEvents>(event: E, listener: TransportEvents[E]): this;
  off<E extends keyof TransportEvents>(event: E, listener: TransportEvents[E]): this;
  emit<E extends keyof TransportEvents>(
    event: E,
    ...args: Parameters<TransportEvents[E]>
  ): boolean;
}

export class AgentTransport extends EventEmitter {
  private child: ChildProcessWithoutNullStreams | null = null;
  private stdoutBuffer = "";
  private nextId = 1;
  private readonly options: TransportOptions;
  private readonly pending = new Map<
    number | string,
    { resolve: (value: unknown) => void; reject: (reason: Error) => void }
  >();

  constructor(options: TransportOptions) {
    super();
    this.options = options;
  }

  get running(): boolean {
    return this.child !== null && this.child.exitCode === null;
  }

  start(): void {
    if (this.child) {
      throw new Error("transport already started");
    }
    const child = spawn(this.options.command, this.options.args ?? ["serve"], {
      stdio: ["pipe", "pipe", "pipe"],
      env: this.options.env ?? process.env,
      cwd: this.options.cwd,
    });
    this.child = child;

    child.stdout.setEncoding("utf8");
    child.stdout.on("data", (chunk: string) => this.consume(chunk));
    child.stderr.setEncoding("utf8");
    child.stderr.on("data", (chunk: string) => this.emit("stderr", chunk));
    child.on("exit", (code, signal) => {
      this.failAllPending(new Error(`claw-agent exited (code=${code} signal=${signal})`));
      this.emit("exit", code, signal);
    });
    child.on("error", (error) => this.failAllPending(error));
  }

  request<T = unknown>(method: string, params?: unknown): Promise<T> {
    if (!this.child) {
      return Promise.reject(new Error("transport not started"));
    }
    const id = this.nextId++;
    const promise = new Promise<T>((resolve, reject) => {
      this.pending.set(id, { resolve: resolve as (value: unknown) => void, reject });
    });
    this.write({ jsonrpc: "2.0", id, method, params: params ?? {} });
    return promise;
  }

  notify(method: string, params?: unknown): void {
    this.write({ jsonrpc: "2.0", method, params: params ?? {} });
  }

  /** 回应对方的请求。审批弹窗点完之后走这里。 */
  respond(id: number | string, result: unknown): void {
    this.write({ jsonrpc: "2.0", id, result });
  }

  respondError(id: number | string, error: RpcError): void {
    this.write({ jsonrpc: "2.0", id, error });
  }

  async stop(): Promise<void> {
    const child = this.child;
    if (!child) return;
    this.child = null;
    // 先发 shutdown 让它落盘会话，再关管道；不退再 SIGTERM，最后 SIGKILL。
    try {
      this.write({ jsonrpc: "2.0", id: this.nextId++, method: "shutdown", params: {} });
    } catch {
      // 管道可能已经断了，忽略。
    }
    child.stdin.end();
    await new Promise<void>((resolve) => {
      const hardKill = setTimeout(() => {
        child.kill("SIGKILL");
        resolve();
      }, 5000);
      const softKill = setTimeout(() => child.kill("SIGTERM"), 1500);
      child.once("exit", () => {
        clearTimeout(softKill);
        clearTimeout(hardKill);
        resolve();
      });
    });
  }

  private write(frame: Record<string, unknown>): void {
    if (!this.child) {
      throw new Error("transport not started");
    }
    this.child.stdin.write(`${JSON.stringify(frame)}\n`);
  }

  private consume(chunk: string): void {
    this.stdoutBuffer += chunk;
    let newlineIndex = this.stdoutBuffer.indexOf("\n");
    while (newlineIndex !== -1) {
      const line = this.stdoutBuffer.slice(0, newlineIndex).trim();
      this.stdoutBuffer = this.stdoutBuffer.slice(newlineIndex + 1);
      if (line) this.dispatch(line);
      newlineIndex = this.stdoutBuffer.indexOf("\n");
    }
  }

  private dispatch(line: string): void {
    let frame: IncomingFrame;
    try {
      frame = JSON.parse(line) as IncomingFrame;
    } catch (error) {
      this.emit("malformed", line, error as Error);
      return;
    }

    if (frame.id !== undefined && frame.method === undefined) {
      const pending = this.pending.get(frame.id);
      if (!pending) return;
      this.pending.delete(frame.id);
      if (frame.error) {
        const error = new Error(frame.error.message) as Error & { code?: number };
        error.code = frame.error.code;
        pending.reject(error);
      } else {
        pending.resolve(frame.result);
      }
      return;
    }

    if (frame.method === undefined) {
      this.emit("malformed", line, new Error("frame without method or id"));
      return;
    }

    if (frame.id !== undefined) {
      this.emit("serverRequest", frame.id, frame.method, frame.params);
      return;
    }
    this.emit("notification", frame.method, frame.params);
  }

  private failAllPending(error: Error): void {
    for (const [, pending] of this.pending) pending.reject(error);
    this.pending.clear();
  }
}
