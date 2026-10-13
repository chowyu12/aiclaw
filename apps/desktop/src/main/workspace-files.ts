import { readdir, realpath, stat } from "node:fs/promises";
import { resolve, relative, isAbsolute, sep, join } from "node:path";
import { homedir } from "node:os";
import { classifyOpen, protectedLocations } from "./open-file-rules.js";
import { tr } from "../shared/i18n.js";
import type { WorkspaceFileEntry, WorkspaceFilesView } from "../shared/types.js";

const SEARCH_SKIP = new Set([".git", "node_modules", "dist", "build", "release"]);
function inside(root: string, path: string): boolean {
  const part = relative(root,path);
  return part !== ".." && !part.startsWith(`..${sep}`) && !isAbsolute(part);
}
/** Enumerate metadata only, rooted in the session's workspace. */
export async function workspaceFiles(base: string, path: string, query: string, protectedPaths: string[]): Promise<WorkspaceFilesView> {
  if (!base.trim()) throw new Error(tr("请先设置会话工作区。"));
  if (typeof path !== "string" || typeof query !== "string" || isAbsolute(path)) throw new Error(tr("文件浏览路径无效。"));
  const root = await realpath(resolve(base));
  const protectedRoots = (await Promise.all(protectedLocations(homedir(),protectedPaths).map(async value => {
    try { return [resolve(value), await realpath(value)]; } catch { return [resolve(value)]; }
  }))).flat();
  const allowed = async (candidate: string): Promise<string> => {
    if (!inside(root,candidate)) throw new Error(tr("文件必须位于当前工作区内。"));
    const verdict = classifyOpen(candidate,{home:homedir(),protectedPaths:protectedRoots,executable:false});
    if (verdict.action === "refuse") throw new Error(verdict.reason);
    const canonical = await realpath(candidate);
    if (!inside(root,canonical)) throw new Error(tr("文件链接指向工作区之外。"));
    const target = classifyOpen(canonical,{home:homedir(),protectedPaths:protectedRoots,executable:false});
    if (target.action === "refuse") throw new Error(target.reason);
    return canonical;
  };
  // Check both user-visible and canonical roots, including a workspace that is itself protected.
  const original = classifyOpen(resolve(base),{home:homedir(),protectedPaths:protectedRoots,executable:false});
  if (original.action === "refuse") throw new Error(original.reason);
  await allowed(root);
  const directory = await allowed(resolve(root,path));
  if (!(await stat(directory)).isDirectory()) throw new Error(tr("这不是一个目录"));
  const entry = async (candidate: string): Promise<WorkspaceFileEntry | null> => {
    try {
      const canonical = await allowed(candidate), info = await stat(canonical);
      if (!info.isDirectory() && !info.isFile()) return null;
      return {name:candidate.split(sep).pop()!,path:relative(root,candidate).split(sep).join("/"),absolutePath:candidate,directory:info.isDirectory()};
    } catch { return null; }
  };
  if (!query.trim()) {
    const names = await readdir(directory);
    names.sort((a,b)=>a.localeCompare(b));
    const entries = (await Promise.all(names.slice(0,1000).map(name=>entry(join(resolve(root,path),name))))).filter((item): item is WorkspaceFileEntry => !!item);
    entries.sort((a,b)=>Number(b.directory)-Number(a.directory)||a.name.localeCompare(b.name));
    return {root,entries,truncated:names.length>1000};
  }
  const needle = query.trim().toLocaleLowerCase(), queue = [directory], seen = new Set<string>(), entries: WorkspaceFileEntry[] = [];
  let scanned = 0;
  while (queue.length) {
    const dir = queue.shift()!;
    if (seen.has(dir)) continue;
    seen.add(dir);
    let children;
    try { children = await readdir(dir,{withFileTypes:true}); } catch { continue; }
    children.sort((a,b)=>a.name.localeCompare(b.name));
    for (const child of children) {
      if (++scanned>5000 || entries.length>=200) return {root,entries,truncated:true};
      if (child.isDirectory() && SEARCH_SKIP.has(child.name)) continue;
      const candidate = join(dir,child.name);
      if (child.isFile() && !relative(root,candidate).toLocaleLowerCase().includes(needle)) continue;
      const item = await entry(candidate);
      if (!item) continue;
      if (item.directory) { if (!SEARCH_SKIP.has(item.name)) queue.push(await realpath(candidate)); }
      else if (item.path.toLocaleLowerCase().includes(needle)) entries.push(item);
    }
  }
  return {root,entries,truncated:false};
}
