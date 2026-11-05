/** Stage only runtime dependencies and the lockfile-verified target native library.
 * The release workflow cross-packages all platforms on Linux, so npm's installed
 * optional dependency alone is insufficient for the other three targets.
 */
import { createHash, timingSafeEqual } from "node:crypto";
import { cp, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { dirname, join } from "node:path";
import { tmpdir } from "node:os";
import { fileURLToPath } from "node:url";

const run = promisify(execFile);

export function driverTarget(platform, arch) {
  const suffix = { darwin: "", win32: "-msvc", linux: "-gnu" }[platform];
  if (suffix === undefined || !["x64", "arm64"].includes(arch)) throw Error(`Unsupported Cua Driver target: ${platform}-${arch}`);
  return `@trycua/cua-driver-${platform}-${arch}${suffix}`;
}

export function verifyIntegrity(bytes, integrity) {
  const match = /^sha512-([A-Za-z0-9+/=]+)$/.exec(integrity ?? "");
  if (!match) throw Error("Missing SHA-512 integrity for Cua Driver native package");
  const expected = Buffer.from(match[1], "base64"), actual = createHash("sha512").update(bytes).digest();
  if (expected.length !== actual.length || !timingSafeEqual(actual, expected)) throw Error("Cua Driver native package integrity mismatch");
}

export async function stageComputerDriver({ repo, destination, platform, arch }) {
  const lock = JSON.parse(await readFile(join(repo, "package-lock.json"), "utf8"));
  const version = lock.packages["node_modules/@trycua/cua-driver"].version;
  for (const name of ["@trycua/cua-driver", "@ubjs/core", "@ubjs/node"]) {
    // Resolve the ESM entry, then walk up to the package root (exports may hide
    // package.json). Preserve the library's generated files and source notices.
    let source = dirname(fileURLToPath(import.meta.resolve(name)));
    for (;;) {
      try { if (JSON.parse(await readFile(join(source, "package.json"), "utf8")).name === name) break; } catch { /* walk */ }
      const parent = dirname(source);
      if (parent === source) throw Error(`Cannot locate ${name}`);
      source = parent;
    }
    const manifest = JSON.parse(await readFile(join(source, "package.json"), "utf8"));
    if (manifest.version !== lock.packages[`node_modules/${name}`]?.version) throw Error(`Installed ${name} does not match package-lock.json`);
    await cp(source, join(destination, "node_modules", name), { recursive: true });
  }
  const name = driverTarget(platform, arch);
  const artifact = lock.packages[`node_modules/${name}`];
  if (!artifact || artifact.version !== version) throw Error(`Missing pinned native package ${name}@${version}`);
  const staging = await mkdtemp(join(tmpdir(), "aiclaw-cua-package-"));
  try {
    const response = await fetch(artifact.resolved, { signal: AbortSignal.timeout(180_000) });
    if (!response.ok) throw Error(`Cua Driver download failed: HTTP ${response.status}`);
    const bytes = Buffer.from(await response.arrayBuffer());
    verifyIntegrity(bytes, artifact.integrity);
    const archive = join(staging, "native.tgz");
    await writeFile(archive, bytes);
    await run("tar", ["-xzf", archive, "-C", staging], { timeout: 30_000 });
    const manifest = JSON.parse(await readFile(join(staging, "package", "package.json"), "utf8"));
    if (manifest.name !== name || manifest.version !== version) throw Error("Cua Driver archive identity mismatch");
    await cp(join(staging, "package"), join(destination, "node_modules", name), { recursive: true });
  } finally { await rm(staging, { recursive: true, force: true }); }
  await mkdir(join(destination, "third-party"), { recursive: true });
  for (const file of ["CUA-DRIVER-LICENSE.txt", "MPL-2.0.txt", "cua-driver-NOTICE.md"]) {
    await cp(join(repo, "docs", "third-party", file), join(destination, "third-party", file));
  }
}
