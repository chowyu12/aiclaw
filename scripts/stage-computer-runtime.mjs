/** Stage our own platform service; preserve the already-built macOS signature. */
import { createHash } from "node:crypto";
import { cp, access, readFile, mkdir } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
export async function stageComputerRuntime({ repo, destination, platform, arch }) {
  if (platform !== "darwin") {
    const files = {linux:["service.py","x11.py","clipboard.py"],win32:["Service.ps1","Native.cs"]}[platform];
    if (!files) throw Error(`Unsupported Computer Use platform: ${platform}`);
    const directory = join(destination,"computer-use",platform);
    await mkdir(directory,{recursive:true});
    for (const name of files) await cp(join(repo,"tools/computer-use",platform === "win32" ? "windows" : "linux",name),join(directory,name));
    await cp(join(repo,"tools/claw-agent/internal/agent/computer-tools.json"),join(directory,"Tools.json"));
    return;
  }
  const name = "AIClaw Computer Use.app";
  const source = join(repo, "apps/desktop/computer-use", name);
  await access(join(source, "Contents/MacOS/AIClawComputerUse")).catch(() => { throw Error("Missing AIClaw Computer Use.app; build on macOS with node scripts/build-computer-runtime.mjs --universal or download the CI native artifact"); });
  const build = JSON.parse(await readFile(join(repo, "apps/desktop/computer-use/AIClaw Computer Use.build.json"), "utf8"));
  if (!build.architectures.includes(arch) || build.protocolVersion !== "AIClawComputerUseIPC-1") throw Error("Native Computer Use bundle does not match the package architecture/protocol");
  const digest = createHash("sha256").update(await readFile(join(source, "Contents/MacOS/AIClawComputerUse"))).digest("hex");
  if (digest !== build.executableSHA256) throw Error("Native Computer Use executable differs from its build identity");
  await cp(source, join(destination, "computer-use", name), { recursive: true });
}
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const repo = resolve(dirname(fileURLToPath(import.meta.url)), "..");
  await stageComputerRuntime({repo,destination:join(repo,"apps/desktop"),platform:process.platform,arch:process.arch});
}
