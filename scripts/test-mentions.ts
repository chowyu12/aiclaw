import { strict as assert } from "node:assert";
import { test } from "node:test";

import { activeReferences, applyMention, MAX_REFERENCES, mentionCandidates, mentionQuery, mentionTitle } from "../apps/desktop/src/renderer/mentions.ts";

/** 输入框里的 @ 引用会话：什么时候弹、插进去什么、发送时交哪几个。 */

test("@ 在开头或空白后面才算；@ 与光标之间有空白就不算", () => {
  assert.deepEqual(mentionQuery("@", 1), { start: 0, query: "" });
  assert.deepEqual(mentionQuery("看看 @周报", 6), { start: 3, query: "周报" });
  assert.equal(mentionQuery("mail@example.com", 16), null, "邮箱地址里的 @ 不弹");
  assert.equal(mentionQuery("@周报 然后", 6), null, "已经打完空格了");
  assert.deepEqual(mentionQuery("a @x b", 4), { start: 2, query: "x" }, "看的是光标前面");
});

test("选中后换成「@标题 」，光标落在后面；标题去掉换行、截断", () => {
  assert.deepEqual(applyMention("看看 @周 的", 3, 5, "周报"), { text: "看看 @周报  的", caret: 7 });
  assert.equal(mentionTitle("第一行\n第二行"), "第一行 第二行");
  assert.equal([...mentionTitle("长".repeat(300))].length, 160);
  assert.equal(mentionTitle(""), "未命名会话");
});

test("发送时只交还留在正文里的，按 id 去重、最多 16 个", () => {
  const bindings = [
    { id: "s1", title: "周报" },
    { id: "s2", title: "方案" },
    { id: "s1", title: "周报" },
  ];
  assert.deepEqual(activeReferences("照 @周报 写", bindings), [{ id: "s1", title: "周报" }]);
  const many = Array.from({ length: 20 }, (_, i) => ({ id: `s${i}`, title: `t${i}` }));
  const text = many.map((b) => `@${b.title}`).join(" ");
  assert.equal(activeReferences(text, many).length, MAX_REFERENCES);
});

test("候选：标题命中的在前，然后是正文命中的，去掉当前会话", () => {
  const sessions = [
    { id: "a", title: "周报整理" },
    { id: "b", title: "发版" },
    { id: "cur", title: "周报当前" },
  ];
  const hits = [{ id: "b", title: "发版" }, { id: "c", title: "别的" }];
  assert.deepEqual(mentionCandidates("周报", sessions, hits, "cur").map((s) => s.id), ["a", "b", "c"]);
  assert.deepEqual(mentionCandidates("", sessions, hits, "cur").map((s) => s.id), ["a", "b"], "没打字时列最近的，不混正文搜索");
});
