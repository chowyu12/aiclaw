import test from "node:test";
import assert from "node:assert/strict";
import {fileDiff} from "../apps/desktop/src/renderer/file-diff.ts";

test("diff reconstructs both versions including trailing newlines",()=>{
 for(const [before,after] of [["a\nb\nc\n","a\nnew\nc\n"],["","new"],["old",""],["same","same"],["x\n","x"],["<script>\n你好","<img>\n你好"]]) {
  const diff=fileDiff(before!,after!);
  if(before===after){assert.equal(diff.length,0);continue;}
  assert.equal(diff.filter(l=>l.kind!=="add").map(l=>l.text).join("\n"),before);
  assert.equal(diff.filter(l=>l.kind!=="remove").map(l=>l.text).join("\n"),after);
 }
});
test("large diffs have bounded computation and retain complete content",()=>{
 const before=Array.from({length:600},(_,i)=>`old ${i}`).join("\n");
 const after=Array.from({length:600},(_,i)=>`new ${i}`).join("\n");
 const diff=fileDiff(before,after);
 assert.equal(diff.length,1200);
 assert.equal(diff.filter(l=>l.kind==="add").map(l=>l.text).join("\n"),after);
});

test("preview bounds work for creation, deletion and large replacements",()=>{
 const lines="line\n".repeat(100_000);
 for(const [before,after] of [["",lines],[lines,""],[lines,`new\n${lines}`]]) assert.equal(fileDiff(before!,after!,3000).length,3000);
});

test("diff reconstructs repeated, blank and Unicode lines across generated edits", () => {
 let seed = 42;
 const next = () => (seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0);
 const lines = ["", "重复", "same", "same", "\tindent", "emoji 🦀", "<b>html</b>", "windows\r"];
 for (let i = 0; i < 250; i++) {
  const version = () => Array.from({length: next() % 20}, () => lines[next() % lines.length]!).join("\n");
  const before = version(), after = version();
  const diff = fileDiff(before, after);
  if (before === after) { assert.deepEqual(diff, []); continue; }
  assert.equal(diff.filter(line => line.kind !== "add").map(line => line.text).join("\n"), before, `before case ${i}`);
  assert.equal(diff.filter(line => line.kind !== "remove").map(line => line.text).join("\n"), after, `after case ${i}`);
 }
});

test("bounded previews preserve the prefix of the full diff in every size branch", () => {
 const large = "repeated\n".repeat(600);
 for (const [before, after] of [["", "a\nb\n"], ["a\nb\n", ""], ["x\nx\n", "x\nnew\nx\n"], [large, `new\n${large}`]]) {
  const full = fileDiff(before!, after!);
  for (const limit of [0, 1, 5, 100, 2000]) assert.deepEqual(fileDiff(before!, after!, limit), full.slice(0, limit));
 }
});
