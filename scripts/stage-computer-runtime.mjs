/** Stage our already-built macOS app, preserving its signature. Other OSes have
 * no substitute driver: their Computer Use tools are deliberately unavailable.
 */
import { createHash } from "node:crypto";
import { cp, access, readFile } from "node:fs/promises";
import { join } from "node:path";
export async function stageComputerRuntime({ repo, destination, platform, arch }) {
  if (platform !== "darwin") return;
  const name = "AIClaw Computer Use.app";
  const source = join(repo, "apps/desktop/computer-use", name);
  await access(join(source, "Contents/MacOS/AIClawComputerUse")).catch(() => { throw Error("Missing AIClaw Computer Use.app; build on macOS with node scripts/build-computer-runtime.mjs --universal or download the CI native artifact"); });
  const build = JSON.parse(await readFile(join(repo, "apps/desktop/computer-use/AIClaw Computer Use.build.json"), "utf8"));
  if (!build.architectures.includes(arch) || build.protocolVersion !== "AIClawComputerUseIPC-1") throw Error("Native Computer Use bundle does not match the package architecture/protocol");
  const digest = createHash("sha256").update(await readFile(join(source, "Contents/MacOS/AIClawComputerUse"))).digest("hex");
  if (digest !== build.executableSHA256) throw Error("Native Computer Use executable differs from its build identity");
  await cp(source, join(destination, "computer-use", name), { recursive: true });
}
