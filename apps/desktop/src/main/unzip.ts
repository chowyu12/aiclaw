import { inflateRawSync } from "node:zlib";
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, join, resolve, sep } from "node:path";

/**
 * 解一个 ZIP 到目标目录。
 *
 * 自己实现而不是引三方库，是因为这里真正要紧的那件事必须是我们自己写的、
 * 自己测过的：**zip slip**。ZIP 里的文件名可以是 `../../.ssh/authorized_keys`，
 * 一个不校验路径的解压器会把任意文件写到目标目录之外。技能包来自内部平台，
 * 而内部平台上的包是别人上传的——这是一条真实的攻击路径，不是理论风险。
 *
 * 只认 deflate（方法 8）与 stored（方法 0）。这两种覆盖了实际能遇到的全部
 * ZIP；别的方法直接报错而不是跳过，免得解出来一个缺文件的技能还以为成功了。
 */

/** ZIP 的几个定长结构大小与魔数。 */
const EOCD_SIGNATURE = 0x06054b50;
const CENTRAL_SIGNATURE = 0x02014b50;
const EOCD_MIN_SIZE = 22;

export interface UnzipResult {
  files: string[];
}

export function unzipInto(archive: Buffer, targetDir: string): UnzipResult {
  const root = resolve(targetDir);
  const entries = readCentralDirectory(archive);
  const files: string[] = [];

  for (const entry of entries) {
    // 目录项本身不用写，它的父目录会在写文件时按需建出来。
    if (entry.name.endsWith("/")) continue;

    const destination = safeJoin(root, entry.name);
    const content = extractEntry(archive, entry);
    mkdirSync(dirname(destination), { recursive: true });
    writeFileSync(destination, content);
    files.push(entry.name);
  }
  return { files };
}

/**
 * 把 ZIP 里的相对路径拼到 root 下，并确认结果没跑出去。
 *
 * 三道都要：绝对路径、`..`、以及解析之后仍然落在外面的情况（Windows 的
 * 盘符、混用分隔符都能绕过只查 `..` 的写法）。
 */
export function safeJoin(root: string, entryName: string): string {
  const normalized = entryName.replace(/\\/g, "/");
  if (normalized.startsWith("/") || /^[a-zA-Z]:/.test(normalized)) {
    throw new Error(`压缩包里有绝对路径，已拒绝：${entryName}`);
  }
  const destination = resolve(root, normalized);
  if (destination !== root && !destination.startsWith(root + sep)) {
    throw new Error(`压缩包试图写到目标目录之外，已拒绝：${entryName}`);
  }
  return destination;
}

interface Entry {
  name: string;
  method: number;
  compressedSize: number;
  uncompressedSize: number;
  localHeaderOffset: number;
}

function readCentralDirectory(archive: Buffer): Entry[] {
  const eocd = findEOCD(archive);
  const count = archive.readUInt16LE(eocd + 10);
  let offset = archive.readUInt32LE(eocd + 16);

  const entries: Entry[] = [];
  for (let i = 0; i < count; i += 1) {
    if (offset + 46 > archive.length || archive.readUInt32LE(offset) !== CENTRAL_SIGNATURE) {
      throw new Error("压缩包的中央目录损坏");
    }
    const method = archive.readUInt16LE(offset + 10);
    const compressedSize = archive.readUInt32LE(offset + 20);
    const uncompressedSize = archive.readUInt32LE(offset + 24);
    const nameLength = archive.readUInt16LE(offset + 28);
    const extraLength = archive.readUInt16LE(offset + 30);
    const commentLength = archive.readUInt16LE(offset + 32);
    const localHeaderOffset = archive.readUInt32LE(offset + 42);
    const name = archive.subarray(offset + 46, offset + 46 + nameLength).toString("utf8");

    entries.push({ name, method, compressedSize, uncompressedSize, localHeaderOffset });
    offset += 46 + nameLength + extraLength + commentLength;
  }
  return entries;
}

function findEOCD(archive: Buffer): number {
  // EOCD 在文件末尾，后面可能跟着最多 64KB 的注释，所以要往回找。
  const earliest = Math.max(0, archive.length - EOCD_MIN_SIZE - 0xffff);
  for (let i = archive.length - EOCD_MIN_SIZE; i >= earliest; i -= 1) {
    if (archive.readUInt32LE(i) === EOCD_SIGNATURE) return i;
  }
  throw new Error("这不是一个合法的 ZIP 文件");
}

function extractEntry(archive: Buffer, entry: Entry): Buffer {
  const header = entry.localHeaderOffset;
  if (header + 30 > archive.length) {
    throw new Error(`压缩包条目越界：${entry.name}`);
  }
  // 本地头里的名字与扩展字段长度可能与中央目录不同，必须按本地头算数据起点。
  const nameLength = archive.readUInt16LE(header + 26);
  const extraLength = archive.readUInt16LE(header + 28);
  const start = header + 30 + nameLength + extraLength;
  const raw = archive.subarray(start, start + entry.compressedSize);

  switch (entry.method) {
    case 0:
      return Buffer.from(raw);
    case 8:
      return inflateRawSync(raw);
    default:
      throw new Error(`不支持的压缩方式 ${entry.method}：${entry.name}`);
  }
}

/** 取压缩包里所有条目的公共顶层目录；没有共同前缀时返回空串。 */
export function commonTopLevelDir(names: string[]): string {
  let prefix: string | null = null;
  for (const name of names) {
    const [top] = name.replace(/\\/g, "/").split("/");
    if (!top) return "";
    if (prefix === null) prefix = top;
    else if (prefix !== top) return "";
  }
  return prefix ?? "";
}

/** 从 ZIP 里读出一个条目的文本内容，不落盘。用来在安装前预读 SKILL.md。 */
export function readTextEntry(archive: Buffer, predicate: (name: string) => boolean): string | null {
  for (const entry of readCentralDirectory(archive)) {
    if (entry.name.endsWith("/")) continue;
    if (predicate(entry.name)) return extractEntry(archive, entry).toString("utf8");
  }
  return null;
}

export { join as joinPath };
