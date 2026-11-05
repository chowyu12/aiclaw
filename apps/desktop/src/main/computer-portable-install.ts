import { createHash } from "node:crypto";
import { lstat, mkdir, mkdtemp, readFile, rename, rm, writeFile } from "node:fs/promises";
import { homedir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

export const portableSources = {
  linux: ["service.py", "x11.py", "clipboard.py"],
  win32: ["Service.ps1", "Native.cs", "Clipboard.cs"],
} as const;
export type PortablePlatform = keyof typeof portableSources;

async function directory(path: string) {
  await mkdir(path, { mode: 0o700 }).catch(error => { if (error.code !== "EEXIST") throw error; });
  const entry = await lstat(path);
  if (!entry.isDirectory() || entry.isSymbolicLink() || (process.getuid && (entry.uid !== process.getuid() || (entry.mode & 0o022)))) throw Error("Computer Use requires an owned directory without links or foreign write permissions");
}
async function file(path: string): Promise<Buffer> {
  const entry = await lstat(path);
  if (!entry.isFile() || entry.isSymbolicLink()) throw Error("Computer Use refuses linked or non-file runtime sources");
  return readFile(path);
}
function digest(bytes: Buffer[], frameFiles = false): string {
  const hash = createHash("sha256");
  for (const value of bytes) { if (frameFiles) hash.update(`${value.length}:`); hash.update(value); }
  return hash.digest("hex");
}

/** Immutable generations avoid replacing a working directory held by another
 * live host (including Windows), and make concurrent installs atomic.
 */
export async function installPortableComputer(source: string, platform: PortablePlatform, home = homedir()): Promise<{ directory: string; sourceIdentity: string }> {
  const names = [...portableSources[platform], "Tools.json"];
  const bytes = await Promise.all(names.map(name => file(join(source, name))));
  const sourceIdentity = digest(bytes.slice(0, -1)), identity = digest(bytes, true);
  let root = home;
  for (const part of [".aiclaw", "computer-use", platform]) { root = join(root, part); await directory(root); }
  const target = join(root, identity);
  const verify = async () => {
    const entry = await lstat(target);
    if (!entry.isDirectory() || entry.isSymbolicLink() || (process.getuid && (entry.uid !== process.getuid() || (entry.mode & 0o022)))) throw Error("Computer Use refuses a linked or unowned runtime generation");
    const installed = await Promise.all(names.map(name => file(join(target, name)))).catch(() => { throw Error("Installed Computer Use generation is damaged or linked"); });
    if (digest(installed, true) !== identity) throw Error("Installed Computer Use generation is damaged; reinstall from the bundled runtime");
  };
  try { await verify(); return { directory: target, sourceIdentity }; }
  catch (error) { if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error; }
  const staging = await mkdtemp(join(root, ".install-"));
  try {
    for (let index = 0; index < names.length; index++) await writeFile(join(staging, names[index]!), bytes[index]!, { mode: 0o600, flag: "wx" });
    try { await rename(staging, target); }
    catch (error) {
      if (!["EEXIST", "ENOTEMPTY", "EPERM"].includes((error as NodeJS.ErrnoException).code ?? "")) throw error;
      await verify(); // A concurrent host may already have published this exact generation.
    }
    await verify(); return { directory: target, sourceIdentity };
  } finally { await rm(staging, { recursive: true, force: true }); }
}

export async function syncPortableComputerRuntime() {
  const platform = process.platform;
  if (platform !== "linux" && platform !== "win32") throw Error("Unsupported portable Computer Use platform");
  return installPortableComputer(resolve(dirname(fileURLToPath(import.meta.url)), "../../computer-use", platform), platform);
}
