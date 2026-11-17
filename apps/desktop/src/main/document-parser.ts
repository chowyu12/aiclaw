/** Local, text-only extraction. Archives live solely in 7-Zip's WASM filesystem. */
import SevenZipImport, { type SevenZipModuleFactory } from "7z-wasm";
import { DOMParser } from "@xmldom/xmldom";
import { getDocumentProxy } from "unpdf";
import { parseDoc } from "@jose.espana/docstream/dist/parsers/legacy/doc.js";
import { parseXls } from "@jose.espana/docstream/dist/parsers/legacy/xls.js";
import { parsePpt } from "@jose.espana/docstream/dist/parsers/legacy/ppt.js";
import type { OfficeParserConfig } from "@jose.espana/docstream";
import { posix } from "node:path";
import { ARCHIVE_EXTENSIONS, DOCUMENT_EXTENSIONS, TEXT_EXTENSIONS, attachmentExtension, limitAttachmentText, MAX_ARCHIVE_ENTRIES, MAX_DOCUMENT_BYTES, MAX_EXTRACTED_BYTES, type ParsedAttachment } from "../shared/attachment-files.js";

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
function runs(doc: XmlNode | ReturnType<XmlNode["createElement"]>) { return nodes(doc, "t").map(node => node.textContent ?? "").join(""); }
function paragraphs(data: Uint8Array) { return nodes(xml(data), "p").map(node => runs(node)).join("\n"); }
function relationshipTargets(files: Map<string, Uint8Array>, part: string): Map<string, string> {
  const rel = posix.join(posix.dirname(part), "_rels", posix.basename(part) + ".rels");
  const result = new Map<string, string>();
  for (const node of nodes(xml(files.get(rel)), "Relationship")) {
    if (node.getAttribute("TargetMode") === "External") continue;
    const target = node.getAttribute("Target") ?? "";
    result.set(node.getAttribute("Id") ?? "", posix.normalize(target.startsWith("/") ? target.slice(1) : posix.join(posix.dirname(part), target)));
  }
  return result;
}
async function office(data: Uint8Array, ext: string, budget: Budget): Promise<string> {
  if (["doc", "xls", "ppt"].includes(ext)) {
    const parse = ext === "doc" ? parseDoc : ext === "xls" ? parseXls : parsePpt;
    return (await parse(Buffer.from(data), config)).toMarkdown();
  }
  const files = await unpack(data, `document.${ext}`, budget);
  if (ext === "docx") {
    const body = files.get("word/document.xml");
    if (!body) throw Error("Word document is missing its body");
    const parts = ["word/document.xml", ...[...files.keys()].filter(name => /^word\/(?:header|footer|footnotes|endnotes).*\.xml$/.test(name)).sort()];
    return parts.map(name => paragraphs(files.get(name)!)).join("\n\n");
  }
  if (ext === "xlsx") {
    const shared = files.has("xl/sharedStrings.xml") ? nodes(xml(files.get("xl/sharedStrings.xml")), "si").map(runs) : [];
    const targets = relationshipTargets(files, "xl/workbook.xml");
    return nodes(xml(files.get("xl/workbook.xml")), "sheet").map(sheet => {
      const target = targets.get(sheet.getAttribute("r:id") ?? "");
      const rows = nodes(xml(files.get(target ?? "")), "row").map(row => nodes(row, "c").map(cell => {
        const value = nodes(cell, "v")[0]?.textContent ?? "";
        const text = cell.getAttribute("t") === "s" ? shared[Number(value)] ?? "" : cell.getAttribute("t") === "inlineStr" ? runs(cell) : value;
        return `${cell.getAttribute("r") ?? ""}: ${text}`;
      }).join("\t"));
      return `[Sheet: ${sheet.getAttribute("name")}]\n${rows.join("\n")}`;
    }).join("\n\n");
  }
  const targets = relationshipTargets(files, "ppt/presentation.xml");
  return nodes(xml(files.get("ppt/presentation.xml")), "sldId").map((slide, i) => {
    const target = targets.get(slide.getAttribute("r:id") ?? "");
    if (!target || !files.has(target)) throw Error("PowerPoint slide is missing");
    return `[Slide ${i + 1}]\n${paragraphs(files.get(target)!)}`;
  }).join("\n\n");
}

async function document(data: Uint8Array, ext: string, budget: Budget): Promise<string> {
  if (ext !== "pdf") return office(data, ext, budget);
  if (Buffer.from(data.subarray(0, 5)).toString() !== "%PDF-") throw Error("Invalid PDF file");
  const pdf = await getDocumentProxy(new Uint8Array(data), { useSystemFonts: false, stopAtErrors: true });
  try {
    let text = "";
    for (let page = 1; page <= pdf.numPages; page++) {
      const content = await (await pdf.getPage(page)).getTextContent();
      text += `[Page ${page}]\n` + content.items.map(item => "str" in item ? item.str + (item.hasEOL ? "\n" : " ") : "").join("") + "\n";
      if (Buffer.byteLength(text) > 128 * 1024) break;
    }
    if (!text.replace(/\[Page \d+\]/g, "").trim()) throw Error("PDF contains no selectable text; scanned PDFs need OCR");
    return text;
  } finally { await pdf.loadingTask.destroy(); }
}

async function archive(data: Uint8Array, name: string, budget: Budget, depth = 0): Promise<string> {
  const files = await unpack(data, name, budget);
  const result = ["[Archive contents]", ...[...files].map(([path, bytes]) => `${path} (${bytes.length} bytes)`), ""];
  for (const [path, bytes] of files) {
    const ext = attachmentExtension(path);
    let text = "";
    try {
      if (DOCUMENT_EXTENSIONS.has(ext)) text = await document(bytes, ext, budget);
      else if (TEXT_EXTENSIONS.has(ext)) text = new TextDecoder().decode(bytes.subarray(0, 128 * 1024 + 4));
      else if (depth === 0 && /\.(?:gz|tgz)$/i.test(name) && (ext === "tar" || /\.(?:tar\.gz|tgz)$/i.test(name))) text = await archive(bytes, `${path}.tar`, budget, depth + 1);
      else { result.push(`[${path}: contents not read]`); continue; }
      result.push(`\n[File: ${path}]\n${text}`);
    } catch (error) { result.push(`[${path}: ${error instanceof Error ? error.message : "Could not read file"}]`); }
    if (Buffer.byteLength(result.join("\n")) > 128 * 1024) break;
  }
  return result.join("\n");
}

export async function parseDocumentAttachment(name: string, data: Uint8Array): Promise<ParsedAttachment> {
  if (!data.length || data.length > MAX_DOCUMENT_BYTES) throw Error("Document/archive must be nonempty and at most 25 MiB");
  const ext = attachmentExtension(name), budget = { bytes: 0, entries: 0 };
  if (!DOCUMENT_EXTENSIONS.has(ext) && !ARCHIVE_EXTENSIONS.has(ext)) throw Error("Unsupported document or archive format");
  const text = DOCUMENT_EXTENSIONS.has(ext) ? await document(data, ext, budget) : await archive(data, name, budget);
  if (!text.trim()) throw Error("No readable text was found; the file may be empty, encrypted, or damaged");
  return limitAttachmentText(text);
}
