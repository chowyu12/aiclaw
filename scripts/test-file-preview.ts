import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync, symlinkSync, rmSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { tmpdir, homedir } from "node:os";
import { join } from "node:path";
import { readTextPreview } from "../apps/desktop/src/main/file-preview.ts";
import { fileLanguage, previewReference, MAX_TEXT_PREVIEW_BYTES } from "../apps/desktop/src/shared/file-preview.ts";
import { highlightCode } from "../apps/desktop/src/renderer/code-highlight.ts";

test("text preview uses workspace paths and supports source formats without executing scripts", () => {
  const dir = mkdtempSync(join(tmpdir(),"aiclaw-preview-"));
  try {
    for (const [name,language] of Object.entries({'query.sql':'sql','report.md':'markdown','main.go':'go','app.py':'python','index.ts':'typescript','app.js':'javascript','README':'plaintext','Dockerfile':'dockerfile','a.json':'json','page.html':'xml','icon.svg':'xml','a.cs':'csharp','a.kt':'kotlin','a.rs':'rust'})) {
      writeFileSync(join(dir,name),'你好\r\n<script>alert(1)</script>\n');
      const result = readTextPreview(name,{base:dir,protectedPaths:[]})!;
      assert.equal(result.language,language,name);assert.equal(result.path,join(dir,name));
      assert.equal(result.content,'你好\r\n<script>alert(1)</script>\n');
    }
    writeFileSync(join(dir,'blank.txt'),'');assert.equal(readTextPreview('blank.txt',{base:dir,protectedPaths:[]})?.content,'');
    writeFileSync(join(dir,'doc.docx'),'binary');assert.equal(readTextPreview('doc.docx',{base:dir,protectedPaths:[]}),null);
    assert.equal(fileLanguage('project'),null);assert.equal(fileLanguage('go'),null);
    assert.equal(previewReference('/work/docs/readme.md','../query.sql'),'/work/docs/../query.sql');
    assert.equal(previewReference('/work/docs/readme.md','/other/file.sql'),'/other/file.sql');
  } finally { rmSync(dir,{recursive:true,force:true}); }
});

test("text preview rejects protected paths including symlink aliases before reading", () => {
  const dir=mkdtempSync(join(tmpdir(),'aiclaw-preview-'));
  try {
    const guarded=join(dir,'secret');mkdirSync(guarded);writeFileSync(join(guarded,'key.txt'),'SECRET');
    symlinkSync(guarded,join(dir,'alias'));
    assert.throws(()=>readTextPreview('alias/key.txt',{base:dir,protectedPaths:[guarded]}),/凭据|credentials/);
    // The original built-in credential location is refused even if it does not exist.
    assert.throws(()=>readTextPreview(join(homedir(),'.ssh/key.txt'),{base:dir,protectedPaths:[]}),/凭据|credentials/);
    const aliasRoot=join(dir,'guarded-alias');symlinkSync(guarded,aliasRoot);
    assert.throws(()=>readTextPreview(join(guarded,'key.txt'),{base:dir,protectedPaths:[aliasRoot]}),/凭据|credentials/);
  } finally { rmSync(dir,{recursive:true,force:true}); }
});

test("text preview bounds reads and rejects binary or invalid UTF-8", () => {
 const dir=mkdtempSync(join(tmpdir(),'aiclaw-preview-'));const options={base:dir,protectedPaths:[]};
 try {
  writeFileSync(join(dir,'big.txt'),Buffer.alloc(MAX_TEXT_PREVIEW_BYTES+1,65));
  assert.throws(()=>readTextPreview('big.txt',options),/512 KB/);
  writeFileSync(join(dir,'binary.txt'),Buffer.from([65,0,66]));assert.throws(()=>readTextPreview('binary.txt',options),/UTF-8/);
  writeFileSync(join(dir,'invalid.txt'),Buffer.from([0xff,0xfe]));assert.throws(()=>readTextPreview('invalid.txt',options),/UTF-8/);
  assert.throws(()=>readTextPreview('missing.txt',options));assert.throws(()=>readTextPreview('',options));
  mkdirSync(join(dir,'folder.txt'));assert.equal(readTextPreview('folder.txt',options),null);
 } finally {rmSync(dir,{recursive:true,force:true});}
});

test("syntax highlighting renders code as escaped text and has a bounded plain fallback", () => {
 const malicious='<img src=x onerror=alert(1)><script>bad()</script>';
 for(const language of ['xml','sql','javascript','python','plaintext','unknown']){
  const html=highlightCode(malicious,language);assert.doesNotMatch(html,/<img|<script|onerror="/);
 }
 assert.match(highlightCode('SELECT id FROM users WHERE id = 1;','sql'),/hljs-keyword/);
 assert.match(highlightCode('const answer = 42;','typescript'),/hljs-keyword/);
 assert.match(highlightCode('x'.repeat(50_001)+'<script>','sql'),/&lt;script&gt;/);
});

test("preview refuses named pipes without waiting for a writer", { skip: process.platform === "win32" }, () => {
 const dir=mkdtempSync(join(tmpdir(),'aiclaw-preview-'));
 try {
  execFileSync('mkfifo',[join(dir,'pipe.txt')]);
  assert.throws(()=>readTextPreview('pipe.txt',{base:dir,protectedPaths:[]}),/文件|file/);
 } finally {rmSync(dir,{recursive:true,force:true});}
});
