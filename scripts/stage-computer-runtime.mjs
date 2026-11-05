/** Extract only the target runtime from the integrity-verified, pinned upstream
 * npm package. Preserve the macOS app's upstream signature and notarization.
 */
import { createHash, timingSafeEqual } from "node:crypto";
import { cp, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { dirname, join } from "node:path";
import { tmpdir } from "node:os";
const run = promisify(execFile);

export function runtimeTarget(platform, arch) {
  if (!["arm64", "x64"].includes(arch)) throw Error(`Unsupported Computer Use architecture: ${arch}`);
  if (platform === "darwin") return join("dist", "Open Computer Use.app");
  if (platform === "linux" || platform === "win32") return join("dist", platform === "win32" ? "windows" : "linux", arch === "x64" ? "amd64" : arch);
  throw Error(`Unsupported Computer Use target: ${platform}-${arch}`);
}

export function verifyIntegrity(bytes, integrity) {
  const match = /^sha512-([A-Za-z0-9+/=]+)$/.exec(integrity ?? "");
  if (!match) throw Error("Missing SHA-512 integrity for Open Computer Use package");
  const expected = Buffer.from(match[1], "base64"), actual = createHash("sha512").update(bytes).digest();
  if (expected.length !== actual.length || !timingSafeEqual(actual, expected)) throw Error("Open Computer Use package integrity mismatch");
}

export async function stageComputerRuntime({ repo, destination, platform, arch }) {
  const lock = JSON.parse(await readFile(join(repo, "package-lock.json"), "utf8"));
  const artifact = lock.packages["node_modules/open-computer-use"];
  const desktop = JSON.parse(await readFile(join(repo, "apps/desktop/package.json"), "utf8"));
  if (!artifact || artifact.version !== desktop.dependencies["open-computer-use"]) throw Error("Missing exact pinned Open Computer Use package");
  const staging = await mkdtemp(join(tmpdir(), "aiclaw-ocu-package-"));
  try {
    const response = await fetch(artifact.resolved, { signal: AbortSignal.timeout(180_000) });
    if (!response.ok) throw Error(`Open Computer Use download failed: HTTP ${response.status}`);
    const bytes = Buffer.from(await response.arrayBuffer()); verifyIntegrity(bytes, artifact.integrity);
    const archive = join(staging, "runtime.tgz"); await writeFile(archive, bytes);
    const target = runtimeTarget(platform, arch);
    await run("tar", ["-xzf", archive, "-C", staging, "package/package.json", "package/LICENSE", "package/THIRD_PARTY_NOTICES.md", `package/${target.replaceAll("\\", "/")}`], { timeout: 30_000 });
    const manifest = JSON.parse(await readFile(join(staging, "package/package.json"), "utf8"));
    if (manifest.name !== "open-computer-use" || manifest.version !== artifact.version) throw Error("Open Computer Use archive identity mismatch");
    const output = join(destination, "node_modules", "open-computer-use");
    await mkdir(dirname(join(output, target)), { recursive: true });
    await cp(join(staging, "package", target), join(output, target), { recursive: true });
    for (const file of ["package.json", "LICENSE", "THIRD_PARTY_NOTICES.md"]) await cp(join(staging, "package", file), join(output, file));
  } finally { await rm(staging, { recursive: true, force: true }); }
}
