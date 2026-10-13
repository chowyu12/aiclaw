import { tr } from "../shared/i18n.js";
import { dirname, join, relative, isAbsolute, sep } from "node:path";
import { existsSync, lstatSync, realpathSync, mkdirSync, mkdtempSync, writeFileSync, renameSync, rmSync } from "node:fs";
import { MAX_MEMORY_BYTES, memoryBytes } from "../shared/memory.js";
import { readTextPreview } from "./file-preview.js";
import type { MemoryDocumentView } from "../shared/types.js";

/** Settings reads fixed memory paths independently of loaded session prompts. */
export function readMemoryDocument(path: string, protectedPaths: string[]): MemoryDocumentView {
  try {
    const result = readTextPreview(path, { base: dirname(path), protectedPaths });
    if (!result) return { path, content: "", exists: null, error: tr("这不是一个文件") };
    return { path, content: result.content, exists: true, error: "" };
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === "ENOENT") return { path, content: "", exists: false, error: "" };
    return { path, content: "", exists: null, error: error instanceof Error ? error.message : String(error) };
  }
}

/** Optimistic content check plus atomic replacement; the host owns the target path. */
export function saveMemoryDocument(path: string, base: string, protectedPaths: string[], input: { content: string; expectedContent: string; expectedExists: boolean }): MemoryDocumentView {
  if (typeof input.content !== "string" || typeof input.expectedContent !== "string" || typeof input.expectedExists !== "boolean") throw new Error(tr("记忆保存参数无效。"));
  if (memoryBytes(input.content) > MAX_MEMORY_BYTES) throw new Error(tr("记忆不能超过 32 KB，请先精简内容。"));
  if (input.content.includes("\0")) throw new Error(tr("记忆不能包含空字符。"));
  // Check canonical parent locations before creating or replacing anything.
  readTextPreview(base, { base, protectedPaths });
  const parent = dirname(path);
  if (existsSync(parent)) readTextPreview(parent, { base, protectedPaths });
  const root = realpathSync(base);
  if (existsSync(parent)) {
    const location = relative(root, realpathSync(parent));
    if (location === ".." || location.startsWith(`..${sep}`) || isAbsolute(location)) throw new Error(tr("记忆目录指向所选范围之外，不能修改。"));
  }
  const check = () => {
    try { if (lstatSync(path).isSymbolicLink()) throw new Error(tr("记忆文件是符号链接，不能修改。")); }
    catch (error) { if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error; }
    const current = readMemoryDocument(path, protectedPaths);
    if (current.error) throw new Error(current.error);
    if (current.content !== input.expectedContent || current.exists !== input.expectedExists) throw new Error(tr("记忆已被其他操作修改，请刷新后再编辑。"));
  };
  check();
  mkdirSync(parent, { recursive: true });
  const tempDir = mkdtempSync(join(parent, ".memory-edit-"));
  try {
    const temporary = join(tempDir, "memory.md");
    writeFileSync(temporary, input.content, { mode: 0o600, flag: "wx" });
    check();
    renameSync(temporary, path);
  } finally { rmSync(tempDir, { recursive: true, force: true }); }
  return readMemoryDocument(path, protectedPaths);
}
