import { spawn } from "node:child_process";
import { join } from "node:path";
import { Duplex } from "node:stream";
import type { Readable } from "node:stream";
import { ComputerNativeRuntime, COMPUTER_PROTOCOL, type ComputerToolResult } from "./computer-runtime.js";
import { setTimeout as pause } from "node:timers/promises";
import { syncPortableComputerRuntime } from "./computer-portable-install.js";

/** A private child process per connection, using the same framed action protocol.
 * No listening endpoint, third-party desktop service, shell expansion or caller-supplied code.
 */
export class ComputerPortableRuntime extends ComputerNativeRuntime {
  private readonly sourceIdentity: string;
  private processDiagnostic = () => "";
  constructor(transport: Duplex, sourceIdentity: string) { super(transport); this.sourceIdentity = sourceIdentity; }
  static async create(): Promise<ComputerPortableRuntime> {
    const { directory, sourceIdentity: identity } = await syncPortableComputerRuntime();
    const windows = process.platform === "win32";
    const bootstrap = '& ([scriptblock]::Create([IO.File]::ReadAllText($env:AICLAW_COMPUTER_SERVICE))) -Manifest $env:AICLAW_COMPUTER_MANIFEST';
    const executable = windows ? join(process.env.SystemRoot ?? "C:\\Windows", "System32/WindowsPowerShell/v1.0/powershell.exe") : "/usr/bin/python3";
    const args = windows ? ["-NoProfile", "-NonInteractive", "-MTA", "-EncodedCommand", Buffer.from(bootstrap, "utf16le").toString("base64")] : ["-I", join(directory,"service.py"), join(directory,"Tools.json")];
    const child = spawn(executable, args, {
      cwd: directory, stdio: windows ? ["pipe", "pipe", "pipe"] : ["pipe", "pipe", "pipe", "pipe"], windowsHide: true,
      env: { ...process.env, PYTHONNOUSERSITE: "1", PYTHONDONTWRITEBYTECODE: "1", AICLAW_COMPUTER_CONTROL_FD: windows ? "" : "3", AICLAW_COMPUTER_SERVICE: join(directory,"Service.ps1"), AICLAW_COMPUTER_MANIFEST: join(directory,"Tools.json"), AICLAW_COMPUTER_PARENT_PID: String(process.pid) },
    });
    let stderr = "";
    child.stderr!.on("data", chunk => { stderr = (stderr + String(chunk)).slice(-8192); });
    const transport = Duplex.from({ readable: child.stdout!, writable: child.stdin! });
    let retained = false, controlBuffer = "";
    let timer: ReturnType<typeof setTimeout> | undefined;
    const control = child.stdio[3] as Readable | undefined;
    control?.on("data", chunk => {
      controlBuffer += String(chunk);
      if (controlBuffer.length > 128) { control.destroy(); return; }
      if (controlBuffer === '{"event":"clipboard-retained"}\n') {
        retained = true; clearTimeout(timer);
        control.destroy(); child.stderr!.destroy(); child.unref();
      }
    });
    child.once("error", error => transport.destroy(error));
    child.once("exit", code => { transport.destroy(code ? Error(`Desktop service exited (${code})`) : undefined); });
    transport.once("close", () => {
      child.stdin!.end();
      if (!retained) {
        timer = setTimeout(() => { if (child.exitCode === null && child.signalCode === null) child.kill("SIGTERM"); }, 3000);
        timer.unref(); child.once("exit", () => clearTimeout(timer));
      }
    });
    const runtime = new ComputerPortableRuntime(transport, identity);
    runtime.processDiagnostic = () => `exit=${child.exitCode}, signal=${child.signalCode}${stderr ? `\n${stderr.trim().split("\n").slice(-4).join("\n")}` : ""}`;
    try { await runtime.initialize(); return runtime; }
    catch (error) {
      await runtime.close();
      throw Error(`Desktop service could not initialize. ${windows ? "Requires Windows PowerShell and UI Automation." : "Requires X11, system Python with PyGObject/AT-SPI, XComposite and XTest."} ${String(error)}${stderr ? `\n${stderr.trim().split("\n").slice(-2).join("\n")}` : ""}`);
    }
  }
  override async call(action: string, args: Record<string, unknown>): Promise<ComputerToolResult> {
    try { return await super.call(action, args); }
    catch (error) {
      await pause(10); // Let the process exit event supply its status, without replay.
      throw Error(`${String(error)}\nDesktop service: ${this.processDiagnostic()}`);
    }
  }
  override async initialize(): Promise<void> {
    const response = await this.request("ComputerUseIPCHandshakeRequest", {});
    if (response?.protocolVersion !== COMPUTER_PROTOCOL || response.serverVersion !== "1" || !Array.isArray(response.tools) || response.serviceSourceSHA256 !== this.sourceIdentity) throw Error("Portable desktop service identity or protocol does not match this build");
    this.tools = response.tools;
  }
}
