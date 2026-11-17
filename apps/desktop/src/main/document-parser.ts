/** Local, text-only extraction. Archives live solely in 7-Zip's WASM filesystem. */
import SevenZipImport, { type SevenZipModuleFactory } from "7z-wasm";
import { DOMParser } from "@xmldom/xmldom";
import { definePDFJSModule, getDocumentProxy } from "unpdf";
import { read as readWorkbook, utils as spreadsheet } from "xlsx";
import { createRequire } from "node:module";
import { dirname, join } from "node:path";
import { parseDoc } from "@jose.espana/docstream/dist/parsers/legacy/doc.js";
import { parsePpt } from "@jose.espana/docstream/dist/parsers/legacy/ppt.js";
import { astToMarkdown } from "@jose.espana/docstream/dist/utils/markdownUtils.js";
import type { OfficeParserConfig } from "@jose.espana/docstream";
import { posix } from "node:path";
import { ARCHIVE_EXTENSIONS, DOCUMENT_EXTENSIONS, TEXT_EXTENSIONS, attachmentExtension, limitAttachmentText, MAX_ARCHIVE_ENTRIES, MAX_DOCUMENT_BYTES, MAX_EXTRACTED_BYTES, type ParsedAttachment, type AttachmentReadQuery, type AttachmentSection } from "../shared/attachment-files.js";

const config: Required<OfficeParserConfig> = { outputErrorToConsole: false, newlineDelimiter: "\n", ignoreNotes: false, putNotesAtLast: false, extractAttachments: false, includeRawContent: false, ocr: false, ocrLanguage: "eng", pdfWorkerSrc: "" };
const SevenZip = SevenZipImport as unknown as SevenZipModuleFactory;
type Entry = { name: string; size: number; directory: boolean };
type Budget = { bytes: number; entries: number };

async function unpack(data: Uint8Array, name: string, budget: Budget): Promise<Map<string, Uint8Array>> {
  let output = "";
  const zip = await SevenZip({
    print: line => { output += line + "\n"; if (output.length > 1024 * 1024) throw Error("Archive listing is too large"); },
    printErr: () => {}, stdin: () => null as unknown as number,
  });
  // Gzip may omit its embedded filename; preserve suffixes such as .txt.gz or
  // .tar.gz so 7-Zip can name that member correctly.
  const archive = "/" + posix.basename(name.replaceAll("\\", "/")).replace(/[^a-zA-Z0-9._-]/g, "_");
  zip.FS.writeFile(archive, data);
  const run = (args: string[]) => {
    const code = zip.callMain([...args, "-bsp0", "-p-"]) as unknown as number;
    if (code) throw Error("Archive is damaged, encrypted, or uses an unsupported compression method");
  };
  run(["l", "-slt", "-ba", archive]);
  const entries: Entry[] = [];
  for (const block of output.trim().split(/\n\s*\n/)) {
    if (!block.trim()) continue;
    const fields = Object.fromEntries(block.split("\n").map(line => { const i = line.indexOf(" = "); return [line.slice(0, i), line.slice(i + 3).trimEnd()]; }));
    const path = fields.Path;
    if (!path || /[\x00-\x1f]/.test(path) || path.startsWith("/") || path.includes("\\") || /^[a-z]:/i.test(path) || path.split("/").includes("..")) throw Error("Archive contains an unsafe file path");
    if (fields.Encrypted === "+") throw Error("Password-protected archives are not supported");
    if (fields["Symbolic Link"] || fields["Hard Link"] || /[lrwx-]{10}/.test(fields.Attributes ?? "") && (fields.Attributes ?? "").includes("l")) throw Error("Archive links are not supported");
    const directory = fields.Folder === "+" || (fields.Attributes ?? "").startsWith("D");
    const size = Number(fields.Size ?? (directory ? "0" : "NaN"));
    if (!Number.isSafeInteger(size) || size < 0) throw Error("Invalid archive entry size");
    budget.entries++;
    budget.bytes += size;
    if (budget.entries > MAX_ARCHIVE_ENTRIES || budget.bytes > MAX_EXTRACTED_BYTES) throw Error("Archive exceeds the limit of 512 entries or 64 MiB expanded data");
    entries.push({ name: path, size, directory });
  }
  // No NODEFS mount: neither absolute paths nor links can reach the host filesystem.
  output = "";
  let written = 0;
  const write = zip.FS.write.bind(zip.FS);
  zip.FS.write = (stream, buffer, offset, length, position) => {
    written += length;
    if (written > MAX_EXTRACTED_BYTES) throw Error("Archive exceeds 64 MiB expanded data");
    return write(stream, buffer, offset, length, position);
  };
  run(["x", "-y", "-o/out", archive]);
  const files = new Map<string, Uint8Array>();
  for (const entry of entries) {
    if (entry.directory) continue;
    const path = `/out/${entry.name}`;
    if (!zip.FS.isFile(zip.FS.lstat(path).mode)) throw Error("Archive contains a non-regular file");
    const bytes = zip.FS.readFile(path) as Uint8Array;
    if (bytes.byteLength !== entry.size) throw Error("Archive entry size does not match its directory");
    files.set(entry.name, bytes);
  }
  return files;
}

function xml(data: Uint8Array | undefined) {
  if (!data) throw Error("Office document is missing required content");
  const text = new TextDecoder().decode(data);
  if (/<!DOCTYPE|<!ENTITY/i.test(text)) throw Error("XML entities are not supported");
  return new DOMParser({ onError: () => { throw Error("Invalid Office XML"); } }).parseFromString(text, "text/xml");
}
type XmlNode = ReturnType<typeof xml>;
function nodes(doc: XmlNode | ReturnType<XmlNode["createElement"]>, name: string) { return Array.from(doc.getElementsByTagNameNS("*", name)); }
/** Walk once: preserve tabs, line breaks and table cells without duplicating text boxes. */
function paragraphs(data: Uint8Array): string {
  function walk(node: ReturnType<XmlNode["createElement"]>): string {
    const kind = node.localName;
    if (kind === "t") return node.textContent ?? "";
    if (kind === "tab") return "\t";
    if (kind === "br" || kind === "cr") return "\n";
    const text = Array.from(node.childNodes).filter(child => child.nodeType === 1).map(child => walk(child as typeof node)).join("");
    if (kind === "tc") return text.trimEnd() + "\t";
    if (kind === "tr") return text.trimEnd() + "\n";
    return kind === "p" ? text + "\n" : text;
  }
  return walk(xml(data).documentElement!).trimEnd();
}
function relationshipTargets(files: Map<string, Uint8Array>, part: string, kind?: string): Map<string, string> {
  const rel = posix.join(posix.dirname(part), "_rels", posix.basename(part) + ".rels");
  const result = new Map<string, string>();
  if (!files.has(rel)) return result;
  for (const node of nodes(xml(files.get(rel)), "Relationship")) {
    if (node.getAttribute("TargetMode") === "External" || kind && !node.getAttribute("Type")?.endsWith("/" + kind)) continue;
    const target = node.getAttribute("Target") ?? "";
    result.set(node.getAttribute("Id") ?? "", posix.normalize(target.startsWith("/") ? target.slice(1) : posix.join(posix.dirname(part), target)));
  }
  return result;
}
function relationId(node: ReturnType<XmlNode["createElement"]>): string {
  return node.getAttributeNS("http://schemas.openxmlformats.org/officeDocument/2006/relationships", "id")
    ?? node.getAttributeNS("http://purl.oclc.org/ooxml/officeDocument/relationships", "id") ?? "";
}
type Section = AttachmentSection & { text: string };
const section = (number: number, kind: string, label: string, text: string): Section => ({ number, kind, label, text });
function selected<T>(items: T[], query?: AttachmentReadQuery): T[] {
  if (query?.mode !== "read") return items;
  const number = query.section ?? 1;
  if (!items[number - 1]) throw Error("Document section does not exist; read the index first");
  return [items[number - 1]!];
}
async function office(data: Uint8Array, ext: string, budget: Budget, query?: AttachmentReadQuery): Promise<Section[]> {
  if (ext === "doc" || ext === "ppt") {
    const ast = await (ext === "doc" ? parseDoc : parsePpt)(Buffer.from(data), config);
    if (ext === "doc") return selected([section(1, "document", "Document", query?.mode === "index" ? "" : ast.toMarkdown())], query);
    const slides = ast.content.filter(node => node.type === "slide").map((node, i) => {
      const noteId = node.metadata && "noteId" in node.metadata ? node.metadata.noteId : undefined;
      const notes = ast.content.filter(note => noteId && note.type === "note" && note.metadata && "noteId" in note.metadata && note.metadata.noteId === noteId);
      return section(i + 1, "slide", `Slide ${i + 1}`, query?.mode === "index" ? "" : astToMarkdown([node, ...notes], config));
    });
    return selected(slides, query);
  }
  const files = ext === "xls" ? new Map<string, Uint8Array>() : await unpack(data, `document.${ext}`, budget);
  if (ext === "xls" || ext === "xlsx") {
    if (ext === "xls" && Buffer.from(data.subarray(0, 8)).toString("hex") !== "d0cf11e0a1b11ae1") throw Error("Invalid Excel binary file");
    const index = readWorkbook(data, { type: "array", bookSheets: true });
    const descriptors = index.SheetNames.map((name, i) => section(i + 1, "sheet", name, ""));
    if (query?.mode === "index") return descriptors;
    const wanted = selected(descriptors, query);
    const workbook = readWorkbook(data, { type: "array", sheets: wanted.map(item => item.number - 1), cellText: true, cellNF: true, cellFormula: true, sheetStubs: true, dateNF: "yyyy-mm-dd" });
    return wanted.map(item => {
      const sheet = workbook.Sheets[item.label];
      if (!sheet) throw Error("Worksheet is missing");
      const cells = Object.keys(sheet).filter(key => !key.startsWith("!")).sort((a, b) => {
        const left = spreadsheet.decode_cell(a), right = spreadsheet.decode_cell(b);
        return left.r - right.r || left.c - right.c;
      });
      const lines = cells.map(address => {
        const cell = sheet[address]!;
        const hasValue = cell.v !== undefined && cell.v !== null;
        const value = cell.t === "b" ? (cell.v ? "TRUE" : "FALSE") : hasValue ? spreadsheet.format_cell(cell) : "";
        return `${address}: ${cell.f ? "=" + cell.f + (hasValue ? " [cached: " + value + "]" : " [no cached result; not evaluated]") : value}`;
      });
      return { ...item, text: `[Sheet: ${item.label}]\n${lines.join("\n")}` };
    });
  }
  if (ext === "docx") {
    if (!files.has("word/document.xml")) throw Error("Word document is missing its body");
    const parts = ["word/document.xml", ...[...files.keys()].filter(name => /^word\/(?:header|footer|footnotes|endnotes).*\.xml$/.test(name)).sort()];
    const descriptors = parts.map((name, i) => section(i + 1, "document-part", name, ""));
    return query?.mode === "index" ? descriptors : selected(descriptors, query).map(item => ({ ...item, text: paragraphs(files.get(item.label)!) }));
  }
  const targets = relationshipTargets(files, "ppt/presentation.xml");
  const slides = nodes(xml(files.get("ppt/presentation.xml")), "sldId").map((node, i) => ({ ...section(i + 1, "slide", `Slide ${i + 1}`, ""), path: targets.get(relationId(node)) }));
  if (query?.mode === "index") return slides;
  return selected(slides, query).map(item => {
    if (!item.path || !files.has(item.path)) throw Error("PowerPoint slide is missing");
    const notes = [...relationshipTargets(files, item.path, "notesSlide").values()].map(path => {
      const doc = xml(files.get(path));
      for (const shape of nodes(doc, "sp")) {
        const placeholder = nodes(shape, "ph")[0]?.getAttribute("type");
        if (["sldNum", "dt", "hdr", "ftr"].includes(placeholder ?? "")) shape.parentNode?.removeChild(shape);
      }
      return paragraphs(new TextEncoder().encode(doc.toString()));
    }).filter(Boolean).join("\n");
    return { ...item, text: `[${item.label}]\n${paragraphs(files.get(item.path)!)}${notes ? "\n[Speaker notes]\n" + notes : ""}` };
  });
}

let pdfEngine: Promise<void> | undefined;
async function document(data: Uint8Array, ext: string, budget: Budget, query?: AttachmentReadQuery): Promise<Section[]> {
  if (ext !== "pdf") return office(data, ext, budget, query);
  if (Buffer.from(data.subarray(0, 1024)).indexOf("%PDF-") < 0) throw Error("Invalid PDF file");
  // Use the Node reader with local CMaps. The serverless default cannot load
  // file:// CMap URLs and can misclassify selectable CJK text as a scan.
  pdfEngine ??= definePDFJSModule(() => import("pdfjs-dist/legacy/build/pdf.mjs"));
  await pdfEngine;
  const root = dirname(createRequire(typeof __filename === "string" ? __filename : import.meta.url).resolve("pdfjs-dist/package.json"));
  const pdfOptions = { useSystemFonts: false, disableFontFace: true, stopAtErrors: true, isEvalSupported: false, useWasm: false, cMapUrl: join(root, "cmaps") + "/", cMapPacked: true, standardFontDataUrl: join(root, "standard_fonts") + "/", verbosity: 0 };
  const pdf = await getDocumentProxy(new Uint8Array(data), pdfOptions);
  try {
    const descriptors = Array.from({ length: pdf.numPages }, (_, i) => section(i + 1, "page", `Page ${i + 1}`, ""));
    if (query?.mode === "index") return descriptors;
    const result: Section[] = [];
    let bytes = 0;
    for (const item of selected(descriptors, query)) {
      const page = await pdf.getPage(item.number);
      const content = await page.getTextContent();
      const text = content.items.map(node => "str" in node ? node.str + (node.hasEOL ? "\n" : " ") : "").join("");
      page.cleanup();
      if (query?.mode === "read" && !text.trim()) throw Error("This PDF page has no selectable text; scanned pages need OCR");
      result.push({ ...item, text: `[${item.label}]\n${text}` });
      bytes += Buffer.byteLength(text);
      if (!query && bytes > 128 * 1024) break;
    }
    if (!result.some(item => item.text.replace(/\[Page \d+\]/g, "").trim())) throw Error("PDF contains no selectable text; scanned PDFs need OCR");
    return result;
  } finally { await pdf.loadingTask.destroy(); }
}

function resultFor(sections: Section[], query?: AttachmentReadQuery): ParsedAttachment {
  if (query?.mode === "index") {
    const start = query.offset ?? 0, end = start + (query.limit ?? 50);
    const items = sections.slice(start, end).map(({ number, kind, label }) => ({ number, kind, label }));
    return { text: "Document section index", truncated: end < sections.length, sections: items, total: sections.length, ...(end < sections.length ? { nextOffset: end } : {}) };
  }
  const text = sections.map(item => item.text).join("\n\n");
  if (!text.trim()) throw Error("No readable text was found; the file may be empty, encrypted, or damaged");
  if (!query) return limitAttachmentText(text);
  const data = Buffer.from(text), offset = query.offset ?? 0, limit = query.limit ?? 16384;
  if (offset > data.length || offset < data.length && (data[offset]! & 0xc0) === 0x80) throw Error("Invalid UTF-8 byte offset; use nextOffset from the previous result");
  const part = new TextDecoder().decode(data.subarray(offset, offset + limit), { stream: true });
  const next = offset + Buffer.byteLength(part);
  return { text: part, truncated: next < data.length, total: data.length, ...(next < data.length ? { nextOffset: next } : {}) };
}
async function archive(data: Uint8Array, name: string, budget: Budget, query?: AttachmentReadQuery): Promise<ParsedAttachment> {
  let files = await unpack(data, name, budget);
  if (/\.(?:tar\.gz|tgz)$/i.test(name) && files.size === 1) {
    const [path, bytes] = [...files][0]!;
    files = await unpack(bytes, `${path}.tar`, budget);
  }
  if (query?.member) {
    const bytes = files.get(query.member);
    if (!bytes) throw Error("Archive member does not exist; read the archive index first");
    const ext = attachmentExtension(query.member);
    if (DOCUMENT_EXTENSIONS.has(ext)) return resultFor(await document(bytes, ext, budget, query), query);
    if (TEXT_EXTENSIONS.has(ext)) return resultFor([section(1, "text", query.member, new TextDecoder().decode(bytes))], query);
    throw Error("Archive member format is not supported for text extraction; the original archive is retained");
  }
  if (query) {
    const members = [...files].map(([path, bytes]) => ({ name: path, size: bytes.length, supported: DOCUMENT_EXTENSIONS.has(attachmentExtension(path)) || TEXT_EXTENSIONS.has(attachmentExtension(path)) }));
    const start = query.offset ?? 0, end = start + (query.limit ?? 50);
    return { text: "Archive member index; specify member to read a file", truncated: end < members.length, members: members.slice(start, end), total: members.length, ...(end < members.length ? { nextOffset: end } : {}) };
  }
  const result = ["[Archive contents]", ...[...files].map(([path, bytes]) => `${path} (${bytes.length} bytes)`), ""];
  for (const [path, bytes] of files) {
    const ext = attachmentExtension(path);
    try {
      if (DOCUMENT_EXTENSIONS.has(ext)) result.push(`\n[File: ${path}]\n${resultFor(await document(bytes, ext, budget)).text}`);
      else if (TEXT_EXTENSIONS.has(ext)) result.push(`\n[File: ${path}]\n${new TextDecoder().decode(bytes.subarray(0, 128 * 1024 + 4))}`);
      else result.push(`[${path}: contents not read]`);
    } catch (error) { result.push(`[${path}: ${error instanceof Error ? error.message : "Could not read file"}]`); }
    if (Buffer.byteLength(result.join("\n")) > 128 * 1024) break;
  }
  return limitAttachmentText(result.join("\n"));
}

export async function parseDocumentAttachment(name: string, data: Uint8Array, query?: AttachmentReadQuery): Promise<ParsedAttachment> {
  if (!data.length || data.length > MAX_DOCUMENT_BYTES) throw Error("Document/archive must be nonempty and at most 25 MiB");
  if (query) {
    if (!["index", "read"].includes(query.mode ?? "") || !Number.isSafeInteger(query.offset ?? 0) || (query.offset ?? 0) < 0 || !Number.isSafeInteger(query.limit ?? 50) || (query.limit ?? 50) < (query.mode === "index" ? 1 : 4) || (query.limit ?? 50) > (query.mode === "index" ? 100 : 32768) || !Number.isSafeInteger(query.section ?? 1) || (query.section ?? 1) < 1) throw Error("Invalid attachment read range");
  }
  const ext = attachmentExtension(name), budget = { bytes: 0, entries: 0 };
  if (!DOCUMENT_EXTENSIONS.has(ext) && !ARCHIVE_EXTENSIONS.has(ext) && !TEXT_EXTENSIONS.has(ext)) throw Error("Unsupported document or archive format");
  if (ARCHIVE_EXTENSIONS.has(ext)) return archive(data, name, budget, query);
  if (TEXT_EXTENSIONS.has(ext)) return resultFor([section(1, "text", name, new TextDecoder().decode(data))], query);
  return resultFor(await document(data, ext, budget, query), query);
}
