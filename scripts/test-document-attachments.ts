import test from "node:test";
import assert from "node:assert/strict";
import { readFile, mkdtemp, rm } from "node:fs/promises";
import { build } from "esbuild";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fork } from "node:child_process";
import { fileURLToPath } from "node:url";
import { zipSync, gzipSync, strToU8 } from "fflate";
import { utils, write } from "xlsx";
import { unzipSync } from "fflate";
import SevenZipImport, { type SevenZipModuleFactory } from "7z-wasm";
import { parseDocumentAttachment } from "../apps/desktop/src/main/document-parser.ts";
import { classifyFile, inlineText } from "../apps/desktop/src/renderer/attachments.ts";
import { MAX_DOCUMENT_BYTES, MAX_ATTACHMENT_TEXT_BYTES, limitAttachmentText } from "../apps/desktop/src/shared/attachment-files.ts";
import { stageHostDependencies } from "./stage-host-dependencies.mjs";

const SevenZip = SevenZipImport as unknown as SevenZipModuleFactory;
const fixture = (name: string) => readFile(new URL(`./fixtures/document-attachments/${name}`, import.meta.url));
const pack = (files: Record<string, string>) => zipSync(Object.fromEntries(Object.entries(files).map(([name, text]) => [name, strToU8(text)])));
const word = pack({ "word/document.xml": '<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>中文文档</w:t></w:r></w:p><w:tbl><w:tr><w:tc><w:p><w:r><w:t>表格内容</w:t></w:r></w:p></w:tc></w:tr></w:tbl></w:body></w:document>' });
function workbook(): Uint8Array {
  const wb = utils.book_new();
  const sheet = utils.aoa_to_sheet([["销售金额", null, 123.5]]);
  sheet.D1 = { t: "n", f: "1+1", v: 2 };
  sheet["!ref"] = "A1:D1";
  utils.book_append_sheet(wb, sheet, "收入");
  utils.book_append_sheet(wb, utils.aoa_to_sheet([["成本"]]), "支出");
  return new Uint8Array(write(wb, { type: "buffer", bookType: "xlsx", bookSST: true }));
}
const excel = workbook();
const slides = pack({
  "ppt/presentation.xml": '<p:presentation xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><p:sldIdLst><p:sldId r:id="r2"/><p:sldId r:id="r1"/></p:sldIdLst></p:presentation>',
  "ppt/_rels/presentation.xml.rels": '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="r1" Target="slides/slide1.xml"/><Relationship Id="r2" Target="slides/slide2.xml"/></Relationships>',
  "ppt/slides/slide1.xml": '<a:root xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><a:p><a:r><a:t>第二页</a:t></a:r></a:p></a:root>',
  "ppt/slides/slide2.xml": '<a:root xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><a:p><a:r><a:t>第一页</a:t></a:r></a:p></a:root>',
});

function pdf(content = "PDF attachment works", cjk = false, pages = 1): Uint8Array {
  const objects = ['<< /Type /Catalog /Pages 2 0 R >>', ''];
  const kids: string[] = [];
  for (let i = 0; i < pages; i++) {
    const page = objects.length + 1, font = page + 1, contents = page + 2;
    const stream = content ? `BT /F1 12 Tf 20 100 Td ${cjk ? "<4e2d6587>" : `(${content} ${i + 1})`} Tj ET` : "";
    kids.push(`${page} 0 R`);
    objects.push(`<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] /Resources << /Font << /F1 ${font} 0 R >> >> /Contents ${contents} 0 R >>`,
      cjk ? `<< /Type /Font /Subtype /Type0 /BaseFont /STSong-Light /Encoding /UniGB-UCS2-H /DescendantFonts [${page + 3} 0 R] >>` : '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>',
      `<< /Length ${stream.length} >>\nstream\n${stream}\nendstream`);
    if (cjk) objects.push(`<< /Type /Font /Subtype /CIDFontType0 /BaseFont /STSong-Light /CIDSystemInfo << /Registry (Adobe) /Ordering (GB1) /Supplement 4 >> /FontDescriptor ${page + 4} 0 R /DW 1000 >>`,
      '<< /Type /FontDescriptor /FontName /STSong-Light /Flags 6 /FontBBox [-250 -143 600 857] /ItalicAngle 0 /Ascent 857 /Descent -143 /CapHeight 857 /StemV 80 >>');
  }
  objects[1] = `<< /Type /Pages /Kids [${kids.join(" ")}] /Count ${pages} >>`;
  let text = "%PDF-1.4\n";
  const offsets = [0];
  for (const [i, object] of objects.entries()) { offsets.push(text.length); text += `${i + 1} 0 obj\n${object}\nendobj\n`; }
  const start = text.length, size = objects.length + 1;
  text += `xref\n0 ${size}\n0000000000 65535 f \n${offsets.slice(1).map(offset => `${String(offset).padStart(10, "0")} 00000 n \n`).join("")}trailer\n<< /Size ${size} /Root 1 0 R >>\nstartxref\n${start}\n%%EOF\n`;
  return strToU8(text);
}

async function compressed(type: string, data = strToU8("压缩包里的文字"), name = "readme.txt", password = false): Promise<Uint8Array> {
  const zip = await SevenZip({ print: () => {}, printErr: () => {}, stdin: () => null as unknown as number });
  zip.FS.writeFile(`/${name}`, data);
  const archive = `/archive.${type}`;
  const code = zip.callMain(["a", `-t${type}`, "-bsp0", ...(password ? ["-psecret", "-mem=AES256"] : []), archive, `/${name}`]) as unknown as number;
  assert.equal(code, 0);
  return zip.FS.readFile(archive);
}

test("classifies modern/legacy Office and common archives, enforcing file limits", () => {
  for (const ext of ["PDF", "doc", "docx", "xls", "xlsx", "ppt", "pptx", "zip", "7z", "rar", "tar", "gz", "tgz"]) {
    assert.equal(classifyFile({ name: `file.${ext}`, size: 100, type: "application/octet-stream" }, 0).accept, "document");
    assert.equal(classifyFile({ name: `file.${ext}`, size: MAX_DOCUMENT_BYTES + 1, type: "" }, 0).accept, "no");
  }
  assert.equal(classifyFile({ name: "a.pdf", size: 0, type: "" }, 0).accept, "no");
  assert.equal(classifyFile({ name: "a.exe", size: 100, type: "" }, 0).accept, "no");
});
test("Word preserves Chinese paragraphs and table text as a normal message attachment", async () => {
  const result = await parseDocumentAttachment("文件.docx", word);
  assert.match(result.text, /中文文档\n表格内容/);
  assert.match(inlineText({ kind: "text", name: "文件.docx", ...result }), /附件 文件.docx/);
});
test("Excel follows workbook relationships, retains sheet names, cell coordinates and cached formula values", async () => {
  const result = await parseDocumentAttachment("data.xlsx", excel);
  assert.match(result.text, /Sheet: 收入.*A1: 销售金额.*C1: 123.5.*D1: =1\+1 \[cached: 2\].*Sheet: 支出.*成本/s);
});
test("PowerPoint follows presentation order rather than archive filename order", async () => {
  assert.match((await parseDocumentAttachment("slides.pptx", slides)).text, /Slide 1.*第一页.*Slide 2.*第二页/s);
});
test("PDF extracts text locally and gives a clear error for pages without selectable text", async () => {
  assert.match((await parseDocumentAttachment("file.pdf", pdf())).text, /Page 1.*PDF attachment works/s);
  await assert.rejects(parseDocumentAttachment("scan.pdf", pdf("")), /OCR/);
});
test("real legacy DOC/XLS/PPT files extract nonempty content", async () => {
  for (const ext of ["doc", "xls", "ppt"]) {
    const result = await parseDocumentAttachment(`sample.${ext}`, await fixture(`sample.${ext}`));
    assert.ok(result.text.trim().length > 5, ext);
    assert.match(result.text, ext === "doc" ? /Word 97-SR2/ : ext === "xls" ? /replaceMe/ : /test title/);
    assert.ok(!result.text.includes("\u0000"), ext);
  }
  await assert.rejects(parseDocumentAttachment("locked.doc", await fixture("encrypted.doc")), /encrypt|password/i);
});
test("ZIP exposes a directory and nested documents, and reports unsupported members", async () => {
  const data = zipSync({ "文档.docx": word, "table.xlsx": excel, "a.txt": strToU8("hello archive"), "bad.pdf": strToU8("broken"), "run.exe": new Uint8Array([0, 1]) });
  const result = await parseDocumentAttachment("bundle.zip", data);
  assert.match(result.text, /Archive contents/);
  assert.match(result.text, /中文文档/);
  assert.match(result.text, /销售金额/);
  assert.match(result.text, /hello archive/);
  assert.match(result.text, /bad.pdf: Invalid PDF/);
  assert.match(result.text, /run.exe: contents not read/);
});
test("7z, TAR, GZ and TAR.GZ archives extract contents without host commands", async () => {
  for (const type of ["7z", "tar", "gzip"]) {
    const result = await parseDocumentAttachment(`file.${type === "gzip" ? "gz" : type}`, await compressed(type));
    assert.match(result.text, /压缩包里的文字/, type);
  }
  const tar = await compressed("tar");
  assert.match((await parseDocumentAttachment("file.tar.gz", await compressed("gzip", tar, "inner.tar"))).text, /压缩包里的文字/);
  for (const name of ["no-name.tar.gz", "no-name.tgz"]) assert.match((await parseDocumentAttachment(name, gzipSync(tar))).text, /压缩包里的文字/);
  assert.match((await parseDocumentAttachment("notes.txt.gz", gzipSync(strToU8("没有内嵌文件名")))).text, /没有内嵌文件名/);
});
test("RAR5 fixture has a readable directory", async () => {
  const result = await parseDocumentAttachment("sample.rar", await fixture("sample.rar"));
  assert.match(result.text, /Archive contents/);
  assert.ok(result.text.length > 40);
});
test("encrypted, corrupt, unsafe and oversized archives fail explicitly", async () => {
  await assert.rejects(parseDocumentAttachment("secret.zip", await compressed("zip", strToU8("secret"), "a.txt", true)), /password|encrypt/i);
  await assert.rejects(parseDocumentAttachment("bad.zip", strToU8("not a zip")), /damaged|unsupported/i);
  await assert.rejects(parseDocumentAttachment("unsafe.zip", pack({ "../escape.txt": "secret" })), /unsafe/);
  const links = await SevenZip({ print: () => {}, printErr: () => {}, stdin: () => null as unknown as number });
  links.FS.symlink("/etc/passwd", "/link");
  assert.equal(links.callMain(["a", "-ttar", "-snl", "/links.tar", "/link", "-bsp0"]) as unknown as number, 0);
  await assert.rejects(parseDocumentAttachment("links.tar", links.FS.readFile("/links.tar")), /links are not supported/);
  const many = pack(Object.fromEntries(Array.from({ length: 513 }, (_, i) => [`${i}.txt`, ""])));
  await assert.rejects(parseDocumentAttachment("many.zip", many), /512 entries/);
  const bomb = Buffer.from(pack({ "huge.txt": "x" }));
  const offset = bomb.indexOf(Buffer.from([0x50, 0x4b, 0x01, 0x02]));
  bomb.writeUInt32LE(65 * 1024 * 1024, offset + 24);
  bomb.writeUInt32LE(65 * 1024 * 1024, 22);
  await assert.rejects(parseDocumentAttachment("bomb.zip", bomb), /64 MiB/);
});
test("malformed Office/PDF and XML entities cannot silently become successful attachments", async () => {
  for (const ext of ["pdf", "doc", "docx", "xls", "xlsx", "ppt", "pptx"]) await assert.rejects(parseDocumentAttachment(`broken.${ext}`, strToU8("broken")));
  await assert.rejects(parseDocumentAttachment("bad.docx", pack({ "word/document.xml": '<!DOCTYPE x [<!ENTITY x "payload">]><root>&x;</root>' })), /entities/);
});
test("UTF-8 truncation is explicit and never splits a Chinese character", async () => {
  const result = await parseDocumentAttachment("long.zip", pack({ "big.txt": "中".repeat(60000) }));
  assert.equal(result.truncated, true);
  assert.ok(Buffer.byteLength(result.text) <= MAX_ATTACHMENT_TEXT_BYTES);
  assert.ok(!result.text.endsWith("\uFFFD"));
  assert.equal(limitAttachmentText("short").truncated, false);
});
test("bundled parser runs outside the checkout using staged production modules only", async () => {
  const directory = await mkdtemp(join(tmpdir(), "aiclaw-document-worker-"));
  try {
    const repo = fileURLToPath(new URL("../", import.meta.url));
    await stageHostDependencies({ repo, destination: directory });
    const entry = join(directory, "worker.cjs");
    await build({ entryPoints: [join(repo, "apps/desktop/src/main/document-worker.ts")], outfile: entry, bundle: true, platform: "node", target: "node22", format: "cjs", external: ["unpdf", "7z-wasm", "pdfjs-dist"] });
    const child = fork(entry, [], { cwd: directory, env: { ...process.env, NODE_PATH: "" }, execArgv: [], serialization: "advanced", stdio: ["ignore", "ignore", "pipe", "ipc"] });
    try {
      for (const [name, data] of [["file.doc", await fixture("sample.doc")], ["file.pdf", pdf()], ["中文.pdf", pdf("Chinese", true)], ["file.zip", zipSync({ "a.docx": word })]] as const) {
      const reply = await new Promise<{result?: {text: string}; error?: string}>((resolve, reject) => {
        const timer = setTimeout(() => reject(Error("worker timed out")), 15_000);
        child.once("message", value => { clearTimeout(timer); resolve(value as {result: {text: string}}); });
        child.once("exit", code => { clearTimeout(timer); reject(Error(`worker exited ${code}`)); });
        child.send({ name, data });
      });
      assert.equal(reply.error, undefined);
      assert.ok(reply.result?.text);
      if (name === "中文.pdf") assert.match(reply.result!.text, /中文/);
      }
    } finally { child.kill(); }
  } finally { await rm(directory, { recursive: true, force: true }); }
});

test("Word tabs and line breaks preserve token boundaries without duplicate nested text", async () => {
  const data = pack({ "word/document.xml": '<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>12</w:t><w:tab/><w:t>34</w:t><w:br/><w:t>56</w:t></w:r></w:p><w:p><w:r><w:txbxContent><w:p><w:r><w:t>nested</w:t></w:r></w:p></w:txbxContent></w:r></w:p></w:body></w:document>' });
  const result = await parseDocumentAttachment("tokens.docx", data, { mode: "read" });
  assert.match(result.text, /12\t34\n56/);
  assert.equal(result.text.match(/nested/g)?.length, 1);
});
test("formatted Excel values and formulas distinguish cached, absent and boolean results", async () => {
  const wb = utils.book_new();
  utils.book_append_sheet(wb, {
    A1: { t: "n", v: 45292, z: "yyyy-mm-dd" }, B1: { t: "n", v: 0.15, z: "0%" },
    C1: { t: "n", f: "1+1" }, D1: { t: "b", v: true }, E1: { t: "n", f: "2+2", v: 4 }, "!ref": "A1:E1",
  }, "Formatted");
  utils.book_append_sheet(wb, utils.aoa_to_sheet([["second only"]]), "Second");
  for (const bookType of ["xlsx", "xls"] as const) {
    const data = new Uint8Array(write(wb, { type: "buffer", bookType }));
    const result = await parseDocumentAttachment(`formatted.${bookType}`, data, { mode: "read", section: 1 });
    assert.match(result.text, /A1: 2024-01-01/);
    assert.match(result.text, /B1: 15%/);
    assert.match(result.text, /D1: TRUE/);
    // Legacy XLS writers may discard formulas without caches; XLSX retains them.
    if (bookType === "xlsx") {
      assert.match(result.text, /C1: =1\+1 \[no cached result; not evaluated\]/);
      assert.match(result.text, /E1: =2\+2 \[cached: 4\]/);
    }
    assert.ok(!result.text.includes("second only"));
    const index = await parseDocumentAttachment(`formatted.${bookType}`, data, { mode: "index", limit: 1 });
    assert.equal(index.total, 2);
    assert.equal(index.nextOffset, 1);
    assert.equal((await parseDocumentAttachment(`formatted.${bookType}`, data, { mode: "read", section: 2 })).text.includes("second only"), true);
  }
});
test("OOXML relationship namespace aliases preserve sheets, slide order and speaker notes", async () => {
  const workbook = unzipSync(excel);
  workbook["xl/workbook.xml"] = strToU8(new TextDecoder().decode(workbook["xl/workbook.xml"]).replaceAll("xmlns:r=", "xmlns:rel=").replaceAll("r:id=", "rel:id="));
  assert.match((await parseDocumentAttachment("aliases.xlsx", zipSync(workbook), { mode: "read", section: 1 })).text, /销售金额/);
  const presentation = unzipSync(slides);
  presentation["ppt/presentation.xml"] = strToU8(new TextDecoder().decode(presentation["ppt/presentation.xml"]).replaceAll("xmlns:r=", "xmlns:rel=").replaceAll("r:id=", "rel:id="));
  presentation["ppt/slides/_rels/slide2.xml.rels"] = strToU8('<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="note" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/notesSlide" Target="../notesSlides/notesSlide1.xml"/></Relationships>');
  presentation["ppt/notesSlides/notesSlide1.xml"] = strToU8('<p:notes xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><p:sp><p:nvSpPr><p:nvPr><p:ph type="body"/></p:nvPr></p:nvSpPr><a:p><a:r><a:t>演讲备注</a:t></a:r></a:p></p:sp><p:sp><p:nvSpPr><p:nvPr><p:ph type="sldNum"/></p:nvPr></p:nvSpPr><a:p><a:r><a:t>SLIDE_NUMBER</a:t></a:r></a:p></p:sp></p:notes>');
  const data = zipSync(presentation);
  const index = await parseDocumentAttachment("notes.pptx", data, { mode: "index" });
  assert.equal(index.sections?.length, 2);
  const first = await parseDocumentAttachment("notes.pptx", data, { mode: "read", section: 1 });
  assert.match(first.text, /第一页.*Speaker notes.*演讲备注/s);
  assert.ok(!first.text.includes("第二页"));
  assert.ok(!first.text.includes("SLIDE_NUMBER"));
  const legacy = await parseDocumentAttachment("old.ppt", await fixture("sample.ppt"), { mode: "index" });
  assert.equal(legacy.sections?.[0]?.kind, "slide");
});
test("CJK PDF CMaps and paged reads work without a system font or network request", async () => {
  assert.match((await parseDocumentAttachment("cjk.pdf", pdf("Chinese", true), { mode: "read", section: 1 })).text, /中文/);
  const data = pdf("page content", false, 2);
  assert.equal((await parseDocumentAttachment("pages.pdf", data, { mode: "index", offset: 1 })).sections?.[0]?.number, 2);
  assert.match((await parseDocumentAttachment("pages.pdf", data, { mode: "read", section: 2 })).text, /Page 2.*page content 2/s);
  await assert.rejects(parseDocumentAttachment("pages.pdf", data, { mode: "read", section: 3 }), /does not exist/);
});
test("archive member indexes and byte ranges can continue beyond the old preview limit", async () => {
  const text = "中".repeat(60000) + "TAIL";
  const data = zipSync({ "long.txt": strToU8(text), "nested.xlsx": excel, "program.exe": strToU8("unread") });
  const index = await parseDocumentAttachment("bundle.zip", data, { mode: "index", limit: 1 });
  assert.equal(index.total, 3);
  assert.equal(index.nextOffset, 1);
  const sheetIndex = await parseDocumentAttachment("bundle.zip", data, { mode: "index", member: "nested.xlsx" });
  assert.equal(sheetIndex.sections?.length, 2);
  const end = await parseDocumentAttachment("bundle.zip", data, { mode: "read", member: "long.txt", offset: 180000 });
  assert.equal(end.text, "TAIL");
  await assert.rejects(parseDocumentAttachment("bundle.zip", data, { mode: "read", member: "long.txt", offset: 1 }), /UTF-8/);
  await assert.rejects(parseDocumentAttachment("bundle.zip", data, { mode: "read", member: "missing.txt" }), /does not exist/);
  await assert.rejects(parseDocumentAttachment("bundle.zip", data, { mode: "read", member: "program.exe" }), /not supported/);
  for (const query of [{ mode: "read", limit: 3 }, { mode: "index", limit: 101 }, { mode: "read", offset: -1 }, { mode: "read", section: 0 }]) {
    await assert.rejects(parseDocumentAttachment("a.txt", strToU8("abc"), query as never), /Invalid attachment read range/);
  }
});
