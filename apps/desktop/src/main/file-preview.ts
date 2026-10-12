import { constants, openSync, closeSync, fstatSync, readSync, realpathSync, existsSync } from "node:fs";
import { homedir } from "node:os";
import { isAbsolute, join, resolve } from "node:path";
import { TextDecoder } from "node:util";
import { classifyOpen, protectedLocations } from "./open-file-rules.js";
import { fileLanguage, MAX_TEXT_PREVIEW_BYTES, type TextFilePreview } from "../shared/file-preview.js";
import { tr } from "../shared/i18n.js";

/** Read-only preview; inspect both the original and canonical paths before reading bytes. */
export function readTextPreview(raw: string, options: { base: string; protectedPaths: string[] }): TextFilePreview | null {
  if (typeof raw !== "string" || !raw.trim()) throw new Error(tr("路径是空的"));
  const text = raw.trim(), home = homedir();
  const expanded = text === "~" || text.startsWith("~/") ? join(home, text.slice(1)) : text;
  const path = isAbsolute(expanded) ? resolve(expanded) : resolve(options.base.trim() || home, expanded);
  const protectedPaths = protectedLocations(home, options.protectedPaths).flatMap(path => existsSync(path) ? [resolve(path), realpathSync(path)] : [resolve(path)]);
  const check = (candidate: string) => {
    const verdict = classifyOpen(candidate, { home, protectedPaths, executable: false });
    if (verdict.action === "refuse") throw new Error(verdict.reason);
  };
  check(path);
  const real = realpathSync(path);
  check(real);
  const language = fileLanguage(path);
  if (!language) return null;
  const fd = openSync(real, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0) | (constants.O_NONBLOCK ?? 0));
  try {
    const info = fstatSync(fd);
    if (!info.isFile()) {
      if (info.isDirectory()) return null;
      throw new Error(tr("这不是一个文件"));
    }
    if (info.size > MAX_TEXT_PREVIEW_BYTES) throw new Error(tr("文件超过 512 KB，无法在应用内预览。"));
    const buffer = Buffer.alloc(MAX_TEXT_PREVIEW_BYTES + 1);
    const length = readSync(fd, buffer, 0, buffer.length, 0);
    if (length > MAX_TEXT_PREVIEW_BYTES) throw new Error(tr("文件超过 512 KB，无法在应用内预览。"));
    const data = buffer.subarray(0, length);
    if (data.includes(0)) throw new Error(tr("这个文件不是 UTF-8 文本，无法预览。"));
    let content: string;
    try { content = new TextDecoder("utf-8", { fatal: true }).decode(data); }
    catch { throw new Error(tr("这个文件不是 UTF-8 文本，无法预览。")); }
    return { path, language, content };
  } finally { closeSync(fd); }
}
