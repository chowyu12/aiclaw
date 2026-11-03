import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { StringDecoder } from "node:string_decoder";

export type CommandEvent =
  | { id: string; sessionId: string; type: "output"; text: string }
  | { id: string; sessionId: string; type: "exit"; code: number | null; stopped: boolean; error?: string };

/** Match Node's cmd.exe quoting rules while executing the exact shell command. */
export function shellInvocation(command: string, platform: NodeJS.Platform = process.platform, env: NodeJS.ProcessEnv = process.env): {shell: string; args: string[]; verbatim: boolean} {
  if (platform === "win32") return {shell: env.ComSpec || env.COMSPEC || "cmd.exe", args: ["/d", "/s", "/c", `"${command}"`], verbatim: true};
  return {shell: env.SHELL || "/bin/sh", args: ["-c", command], verbatim: false};
}

interface Job { child: ChildProcessWithoutNullStreams; sessionId: string; stopped: boolean; bytes: number; truncated: boolean }

/** Commands here are explicitly started by the user, outside the agent sandbox. */
export class CommandRunner {
  private jobs = new Map<string, Job>();
  private emit: (event: CommandEvent) => void;
  private closed = false;
  constructor(emit: (event: CommandEvent) => void) { this.emit = emit; }

  start(input: { id: string; sessionId: string; command: string; cwd: string; path?: string }): void {
    if (this.closed) throw new Error("Command runner is closed");
    if (!/^[a-zA-Z0-9-]{1,100}$/.test(input.id) || !input.sessionId || !input.command.trim() || input.command.length > 100_000 || input.command.includes("\0")) throw new Error("Invalid command request");
    if (this.jobs.has(input.id)) throw new Error("Command is already running");
    if (this.jobs.size >= 20) throw new Error("Too many commands are running");
    const windows = process.platform === "win32";
    const {shell, args, verbatim} = shellInvocation(input.command);
    const env = {...process.env, ...(input.path ? {PATH: input.path} : {})};
    const child = spawn(shell, args, { cwd: input.cwd, env, detached: !windows, stdio: ["pipe", "pipe", "pipe"], windowsHide: true, windowsVerbatimArguments: verbatim });
    child.stdin.end();
    const job: Job = {child, sessionId: input.sessionId, stopped: false, bytes: 0, truncated: false};
    this.jobs.set(input.id, job);
    const stdout = new StringDecoder("utf8"), stderr = new StringDecoder("utf8");
    const output = (text: string) => {
      if (!text) return;
      const remaining = 1_000_000 - job.bytes;
      if (remaining <= 0) { job.truncated = true; return; }
      const clipped = text.slice(0, remaining);
      job.bytes += clipped.length;
      if (clipped.length < text.length) job.truncated = true;
      this.emit({id: input.id, sessionId: input.sessionId, type: "output", text: clipped});
    };
    child.stdout.on("data", (chunk: Buffer) => output(stdout.write(chunk)));
    child.stderr.on("data", (chunk: Buffer) => output(stderr.write(chunk)));
    let error: string | undefined;
    child.on("error", err => { error = err.message; });
    child.on("close", (code) => {
      output(stdout.end()); output(stderr.end());
      if (job.truncated) outputNotice(this.emit, input.id, input.sessionId);
      this.jobs.delete(input.id);
      this.emit({id: input.id, sessionId: input.sessionId, type: "exit", code, stopped: job.stopped, error});
    });
  }

  stop(id: string, sessionId: string, force = false): boolean {
    const job = this.jobs.get(id);
    if (!job || job.sessionId !== sessionId) return false;
    job.stopped = true;
    const pid = job.child.pid;
    if (!pid) return false;
    try {
      if (process.platform === "win32") {
        const killer = spawn("taskkill", ["/pid", String(pid), "/t", "/f"], {windowsHide: true, stdio: "ignore"});
        killer.on("error", () => job.child.kill());
        killer.on("close", code => { if (code !== 0 && this.jobs.get(id) === job) job.child.kill(); });
      } else {
        process.kill(-pid, force ? "SIGKILL" : "SIGTERM");
        const escalation = setTimeout(() => {
          if (this.jobs.get(id) !== job) return;
          try { process.kill(-pid, "SIGKILL"); } catch { /* Process already exited. */ }
        }, 3000);
        escalation.unref();
      }
      return true;
    } catch { return job.child.kill(); }
  }

  stopAll(close = true): void { if (close) this.closed = true; for (const [id, job] of this.jobs) this.stop(id, job.sessionId, close); }
}

function outputNotice(emit: (event: CommandEvent) => void, id: string, sessionId: string): void {
  emit({id, sessionId, type: "output", text: "\n[Output truncated after 1,000,000 characters]\n"});
}
