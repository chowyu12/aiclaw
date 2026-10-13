import { readMemoryDocument, saveMemoryDocument } from "../apps/desktop/src/main/memory-files.ts";
import { memoryEntries, memoryBytes, MAX_MEMORY_BYTES } from "../apps/desktop/src/shared/memory.ts";
import { workspaceFiles } from "../apps/desktop/src/main/workspace-files.ts";
import { textMatches } from "../apps/desktop/src/renderer/file-find.ts";
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

test("memory settings distinguish saved content, missing files and read failures without writing", () => {
  const dir = mkdtempSync(join(tmpdir(),"aiclaw-memory-view-"));
  try {
    const path = join(dir,"memory.md");
    assert.deepEqual(readMemoryDocument(path,[]),{path,content:"",exists:false,error:""});
    writeFileSync(path,"- Prefer SQL examples\n<script>not executable</script>");
    const memory=readMemoryDocument(path,[]);
    assert.equal(memory.exists,true);assert.equal(memory.content,"- Prefer SQL examples\n<script>not executable</script>");assert.equal(memory.error,"");
    const protectedMemory=readMemoryDocument(path,[dir]);assert.equal(protectedMemory.exists,null);assert.ok(protectedMemory.error);assert.equal(protectedMemory.content,"");
    writeFileSync(path,Buffer.from([0xff]));assert.ok(readMemoryDocument(path,[]).error);
    rmSync(path);mkdirSync(path);assert.ok(readMemoryDocument(path,[]).error);
  } finally {rmSync(dir,{recursive:true,force:true});}
});

test("memory entry grouping preserves exact text and continuation lines", () => {
  const text = "# Notes\n\n- (2026-10-13) Use Go\n  continuation\n- Prefer SQL\n";
  const entries = memoryEntries(text);
  assert.equal(entries.length,3);assert.equal(entries.join(""),text);
  assert.equal(entries[1],"- (2026-10-13) Use Go\n  continuation\n");
  assert.equal(memoryBytes("中文"),6);
});

test("memory edits enforce capacity, conflicts and protected paths", () => {
  const dir=mkdtempSync(join(tmpdir(),"aiclaw-memory-edit-")), path=join(dir,".aiclaw","memory.md");
  try {
    const first={content:"- Original\n",expectedContent:"",expectedExists:false};
    assert.equal(saveMemoryDocument(path,dir,[],first).content,first.content);
    const edit={content:"- Edited\n",expectedContent:first.content,expectedExists:true};
    assert.throws(()=>saveMemoryDocument(path,dir,[dir],edit));
    assert.throws(()=>saveMemoryDocument(path,dir,[],{...edit,content:"中".repeat(Math.ceil(MAX_MEMORY_BYTES/3))}));
    assert.throws(()=>saveMemoryDocument(path,dir,[],{...edit,content:"bad\0text"}));
    assert.equal(saveMemoryDocument(path,dir,[],edit).content,edit.content);
    assert.throws(()=>saveMemoryDocument(path,dir,[],edit),/刷新|Refresh/);
    assert.equal(readMemoryDocument(path,[]).content,edit.content);
    assert.equal(saveMemoryDocument(path,dir,[],{content:"",expectedContent:edit.content,expectedExists:true}).content,"");
    assert.equal(readMemoryDocument(path,[]).exists,true);
  } finally {rmSync(dir,{recursive:true,force:true});}
});

test("memory editing refuses redirected directories and symlink files", {skip:process.platform==="win32"}, () => {
  const dir=mkdtempSync(join(tmpdir(),"aiclaw-memory-link-")), outside=mkdtempSync(join(tmpdir(),"aiclaw-memory-outside-"));
  try {
    symlinkSync(outside,join(dir,".aiclaw"),"dir");
    const input={content:"new",expectedContent:"",expectedExists:false};
    assert.throws(()=>saveMemoryDocument(join(dir,".aiclaw","memory.md"),dir,[],input));
    rmSync(join(dir,".aiclaw"));mkdirSync(join(dir,".aiclaw"));
    const target=join(outside,"missing.md");symlinkSync(target,join(dir,".aiclaw","memory.md"));
    assert.throws(()=>saveMemoryDocument(join(dir,".aiclaw","memory.md"),dir,[],input));
    writeFileSync(target,"original");
    assert.throws(()=>saveMemoryDocument(join(dir,".aiclaw","memory.md"),dir,[],{...input,expectedContent:"original",expectedExists:true}));
    assert.equal(readMemoryDocument(target,[]).content,"original");
  } finally {rmSync(dir,{recursive:true,force:true});rmSync(outside,{recursive:true,force:true});}
});

test("workspace browser expands directories and searches metadata within its root", async () => {
 const dir=mkdtempSync(join(tmpdir(),"aiclaw-explorer-"));
 try{
  mkdirSync(join(dir,"src"));mkdirSync(join(dir,"node_modules"));writeFileSync(join(dir,"src","query.sql"),"private contents should not be enumerated");writeFileSync(join(dir,"README.md"),"notes");writeFileSync(join(dir,"node_modules","query.sql"),"dependency");
  const listed=await workspaceFiles(dir,"","",[]);assert.equal(listed.entries[0].directory,true);assert.ok(listed.entries.some(entry=>entry.name==="README.md"));assert.ok(!JSON.stringify(listed).includes("private contents"));
  const child=await workspaceFiles(dir,"src","",[]);assert.equal(child.entries[0]?.path,"src/query.sql");
  const search=await workspaceFiles(dir,"","QUERY",[]);assert.deepEqual(search.entries.map(entry=>entry.path),["src/query.sql"]);
  await assert.rejects(workspaceFiles(dir,"../","",[]));await assert.rejects(workspaceFiles(dir,"","",[dir]));await assert.rejects(workspaceFiles("","","",[]));
 }finally{rmSync(dir,{recursive:true,force:true})}
});
test("workspace browser hides escaping links and handles internal cycles", {skip:process.platform==="win32"}, async () => {
 const dir=mkdtempSync(join(tmpdir(),"aiclaw-explorer-link-")),outside=mkdtempSync(join(tmpdir(),"aiclaw-explorer-outside-"));
 try{
  writeFileSync(join(outside,"secret.txt"),"secret");symlinkSync(outside,join(dir,"outside"),"dir");symlinkSync(dir,join(dir,"cycle"),"dir");writeFileSync(join(dir,"inside.txt"),"okay");
  const result=await workspaceFiles(dir,"","txt",[]);assert.deepEqual(result.entries.map(entry=>entry.name),["inside.txt"]);
  await assert.rejects(workspaceFiles(dir,"outside","",[]));
  assert.ok(!(await workspaceFiles(dir,"","",[])).entries.some(entry=>entry.name==="outside"));
 }finally{rmSync(dir,{recursive:true,force:true});rmSync(outside,{recursive:true,force:true})}
});
test("workspace search reports truncation rather than implying complete results", async () => {
 const dir=mkdtempSync(join(tmpdir(),"aiclaw-explorer-limit-"));
 try{for(let i=0;i<205;i++)writeFileSync(join(dir,`match-${i}.txt`),"");const found=await workspaceFiles(dir,"","match",[]);assert.equal(found.entries.length,200);assert.equal(found.truncated,true)}finally{rmSync(dir,{recursive:true,force:true})}
});
test("file find is literal, case-insensitive and bounded", () => {
 assert.deepEqual(textMatches("SELECT id; select ID;","select").matches,[{start:0,length:6},{start:11,length:6}]);
 assert.equal(textMatches("a.b axb","a.b").matches.length,1);assert.equal(textMatches("[x] <script>","[x]").matches[0]?.length,3);
 assert.equal(textMatches("中文中文","中文").matches.length,2);assert.equal(textMatches("aaa","",2).matches.length,0);
 assert.equal(textMatches("aaa","a",2).truncated,true);
});
