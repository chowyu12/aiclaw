import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { Duplex } from "node:stream";
import { fileURLToPath } from "node:url";
import { ComputerNativeRuntime, COMPUTER_PROTOCOL } from "./computer-runtime.js";

/** A private child process per connection, using the same framed action protocol.
 * No listening endpoint, third-party desktop service, shell expansion or caller-supplied code.
 */
export class ComputerPortableRuntime extends ComputerNativeRuntime {
  private readonly sourceIdentity: string;
  constructor(transport: Duplex, sourceIdentity: string) { super(transport); this.sourceIdentity = sourceIdentity; }
  static async create(): Promise<ComputerPortableRuntime> {
    if (process.platform !== "linux") throw Error("A validated Windows desktop driver is not available yet");
    const root = resolve(dirname(fileURLToPath(import.meta.url)), "../../computer-use");
    const directory = join(root, "linux"), script = join(directory, "service.py");
    const source = await Promise.all([readFile(script), readFile(join(directory, "x11.py"))]).catch(() => { throw Error("Linux desktop service is missing; build the Computer Use runtime or reinstall AIClaw"); });
    const identity = createHash("sha256").update(source[0]!).update(source[1]!).digest("hex");
    const child = spawn("/usr/bin/python3", ["-I", script, join(root, "Tools.json")], {
      cwd: directory, stdio: ["pipe", "pipe", "pipe"], windowsHide: true,
      env: { ...process.env, PYTHONNOUSERSITE: "1", PYTHONDONTWRITEBYTECODE: "1" },
    });
    let stderr = "";
    child.stderr.on("data", chunk => { stderr = (stderr + String(chunk)).slice(-8192); });
    const transport = Duplex.from({ readable: child.stdout, writable: child.stdin });
    child.once("error", error => transport.destroy(error));
    child.once("exit", code => { transport.destroy(code ? Error(`Linux desktop service exited (${code})`) : undefined); });
    transport.once("close", () => {
      child.stdin.end();
      const timer = setTimeout(() => { if (child.exitCode === null && child.signalCode === null) child.kill("SIGTERM"); }, 3000);
      timer.unref(); child.once("exit", () => clearTimeout(timer));
    });
    const runtime = new ComputerPortableRuntime(transport, identity);
    try { await runtime.initialize(); return runtime; }
    catch (error) {
      await runtime.close();
      throw Error(`Linux desktop service could not initialize. It requires X11, system Python with PyGObject/AT-SPI, XComposite and XTest. ${String(error)}${stderr ? `\n${stderr.trim().split("\n").slice(-2).join("\n")}` : ""}`);
    }
  }
  override async initialize(): Promise<void> {
    const response = await this.request("ComputerUseIPCHandshakeRequest", {});
    if (response?.protocolVersion !== COMPUTER_PROTOCOL || response.serverVersion !== "1" || !Array.isArray(response.tools) || response.serviceSourceSHA256 !== this.sourceIdentity) throw Error("Portable desktop service identity or protocol does not match this build");
    this.tools = response.tools;
  }
}
