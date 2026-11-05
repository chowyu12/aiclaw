import { execFile } from "node:child_process";
import { createHash } from "node:crypto";
import { cp, lstat, mkdir, mkdtemp, readFile, realpath, rename, rm, rmdir } from "node:fs/promises";
import { homedir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { setTimeout as pause } from "node:timers/promises";
const run = promisify(execFile);
export const COMPUTER_APP_NAME = "AIClaw Computer Use.app";
export function installedComputerBundle(): string { return join(homedir(), ".aiclaw", "computer-use", COMPUTER_APP_NAME); }
async function bundleIdentity(bundle: string): Promise<string> {
  const hash = createHash("sha256");
  for (const file of ["Contents/MacOS/AIClawComputerUse", "Contents/_CodeSignature/CodeResources"]) hash.update(await readFile(join(bundle, file)));
  return hash.digest("hex");
}
const verify = async (bundle: string) => { await run("/usr/bin/codesign", ["--verify", "--strict", bundle], { timeout: 15_000 }); };

/** Install the bundled, signed helper at a stable user-owned path. The lock and
 * same-directory renames serialize competing hosts and keep partial copies out
 * of LaunchServices. No download, privileged installer or permission grant.
 */
export async function installComputerBundle(source: string, directory = join(homedir(), ".aiclaw", "computer-use"), verifySignature: (bundle: string) => Promise<void> = verify): Promise<string> {
  source = await realpath(source);
  await mkdir(directory, { recursive: true, mode: 0o700 });
  const parent = await lstat(directory);
  if (!parent.isDirectory() || parent.isSymbolicLink() || parent.uid !== process.getuid?.() || (parent.mode & 0o022)) throw Error("Computer Use installation requires an owned directory not writable by other users");
  const target = join(directory, COMPUTER_APP_NAME);
  const lock = join(directory, ".install.lock");
  let locked = false;
  for (let attempt = 0; attempt < 100 && !locked; attempt++) {
    try { await mkdir(lock, { mode: 0o700 }); locked = true; }
    catch (error) { if ((error as NodeJS.ErrnoException).code !== "EEXIST") throw error; await pause(100); }
  }
  if (!locked) throw Error("Computer Use installation is busy; retry after the other AIClaw instance finishes");
  let staging: string | undefined;
  try {
    await verifySignature(source);
    const identity = await bundleIdentity(source);
    const existing = await lstat(target).catch(error => { if (error.code === "ENOENT") return undefined; throw error; });
    if (existing && (!existing.isDirectory() || existing.isSymbolicLink() || existing.uid !== process.getuid?.())) throw Error("Refusing to replace an unowned or linked Computer Use app");
    if (existing) {
      try { if (await bundleIdentity(target) === identity) { await verifySignature(target); return target; } } catch { /* Repair an invalid owned copy from the verified bundled app. */ }
    }
    staging = await mkdtemp(join(directory, ".install-"));
    const candidate = join(staging, COMPUTER_APP_NAME), previous = join(staging, "previous.app");
    await cp(source, candidate, { recursive: true });
    await verifySignature(candidate);
    if (await bundleIdentity(candidate) !== identity) throw Error("Computer Use app changed during installation");
    if (existing) await rename(target, previous);
    try { await rename(candidate, target); }
    catch (error) { if (existing) await rename(previous, target); throw error; }
    return target;
  } finally {
    if (staging) await rm(staging, { recursive: true, force: true });
    await rmdir(lock);
  }
}
