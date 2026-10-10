import { strict as assert } from "node:assert";
import { test } from "node:test";

import { groupTurns, imageArtifacts, turnArtifacts, unmentionedArtifacts, turnSources, stepsElapsed } from "../apps/desktop/src/renderer/turns.ts";
import type { TimelineEntry } from "../apps/desktop/src/renderer/store.ts";

/**
 * 时间线按轮分组。
 *
 * 这段逻辑错了界面不会空、也不会报错——只是步骤挂到了别的提问底下，
 * 或者一轮的步骤被切成两块。所以这里逐条钉住形状。
 */

const user = (id: string, text = "问"): TimelineEntry => ({ kind: "user", id, text });
const agent = (id: string, text = "答"): TimelineEntry => ({
  kind: "agent",
  id,
  text,
  streaming: false,
});
const step = (id: string, extra: Record<string, unknown> = {}): TimelineEntry =>
  ({
    kind: "step",
    id,
    step: "tool",
    title: id,
    detail: "",
    state: "done",
    ...extra,
  }) as TimelineEntry;

test("产物汇总按轮去重，保留文档并排除失败、运行中和正文路径", () => {
  const turns = groupTurns([
    user("u1"), step("docs", { artifacts: ["报告.md", "报告.docx", "报告.pptx", "报告.md", ""] }),
    step("failed", { state: "failed", artifacts: ["failed.pdf"] }),
    step("running", { state: "running", artifacts: ["pending.xlsx"] }), agent("a1", "fake.pdf"),
    user("u2"), step("next", { artifacts: ["报告.md"] }),
  ]);
  assert.deepEqual(turnArtifacts(turns[0]!), ["报告.md", "报告.docx", "报告.pptx"]);
  assert.deepEqual(turnArtifacts(turns[1]!), ["报告.md"]);
});

test("图片预览按轮取结构化产物，去重并排除失败步骤与音频", () => {
  const turns = groupTurns([
    user("u1"), step("one", { artifacts: ["generated/cat.png", "generated/cat.png", "speech.wav"] }),
    step("failed", { state: "failed", artifacts: ["failed.png"] }), agent("a1", "fake.png"),
    user("u2"), step("two", { artifacts: ["second.JPG"] }),
  ]);
  assert.deepEqual(imageArtifacts(turns[0]!), ["generated/cat.png"]);
  assert.deepEqual(imageArtifacts(turns[1]!), ["second.JPG"]);
});

test("一轮里交替出现的回答与步骤，步骤全部归成一块", () => {
  // 这正是界面上看到的形状：模型说一句 → 调几个工具 → 再说一句。
  const turns = groupTurns([
    user("u1"),
    step("s1"),
    agent("a1", "我先查一下："),
    step("s2"),
    step("s3"),
    agent("a2", "查到了。"),
  ]);

  assert.equal(turns.length, 1);
  assert.equal(turns[0]!.key, "u1");
  assert.deepEqual(
    turns[0]!.steps.map((s) => s.id),
    ["s1", "s2", "s3"],
  );
  // 回答保持原来的先后顺序，读起来是连着的一段。
  assert.deepEqual(
    turns[0]!.messages.map((m) => m.text),
    ["我先查一下：", "查到了。"],
  );
});

test("每条用户消息开一轮，步骤不会串到上一轮去", () => {
  const turns = groupTurns([user("u1"), step("s1"), agent("a1"), user("u2"), step("s2")]);

  assert.equal(turns.length, 2);
  assert.deepEqual(turns[0]!.steps.map((s) => s.id), ["s1"]);
  assert.deepEqual(turns[1]!.steps.map((s) => s.id), ["s2"]);
  assert.equal(turns[1]!.messages.length, 0);
});

test("以助手消息开头的旧存档自成一轮，不并进后面那条提问", () => {
  // 并进去的话，它会显示在一条它根本不属于的提问底下。
  const turns = groupTurns([agent("a0", "上次没说完"), user("u1"), agent("a1")]);

  assert.equal(turns.length, 2);
  assert.equal(turns[0]!.user, null);
  assert.equal(turns[0]!.key, "lead-a0");
  assert.equal(turns[1]!.user?.id, "u1");
});

test("空时间线没有轮", () => {
  assert.deepEqual(groupTurns([]), []);
});

test("用时按墙上时间算，不是把每步相加", () => {
  // 三个只读工具并行跑，各 5 秒，同时开始。相加会说成 15 秒。
  const steps = [
    step("s1", { startedAt: 1000, durationMs: 5000 }),
    step("s2", { startedAt: 1000, durationMs: 5000 }),
    step("s3", { startedAt: 1000, durationMs: 5000 }),
  ] as Extract<TimelineEntry, { kind: "step" }>[];
  assert.equal(stepsElapsed(steps), 5000);
});

test("用时覆盖首尾：最后一步结束减第一步开始", () => {
  const steps = [
    step("s1", { startedAt: 1000, durationMs: 500 }),
    step("s2", { startedAt: 2000, durationMs: 1500 }),
  ] as Extract<TimelineEntry, { kind: "step" }>[];
  assert.equal(stepsElapsed(steps), 2500);
});

test("没有时间戳时用时是 0，不显示", () => {
  assert.equal(stepsElapsed([step("s1")] as Extract<TimelineEntry, { kind: "step" }>[]), 0);
});

test("末尾仅保留未提到的产物，绝对路径匹配相对路径，同名文件不误合并", () => {
  const turn = groupTurns([user("files"), step("files", { artifacts: ["report.md", "other.docx", "a/same.pdf", "b/same.pdf"] }),
    agent("answer", "已生成 `/Users/user/work/report.md` 和 `same.pdf`。")])[0]!;
  assert.deepEqual(unmentionedArtifacts(turn), ["other.docx", "a/same.pdf", "b/same.pdf"]);
});

test("search sources deduplicate within a turn and reject unsafe URLs without guessing prose", () => {
  const source = { title: "Docs", url: "https://example.com/docs#one", snippet: "Summary" };
  const turns = groupTurns([
    user("search-1"), step("search", { sources: [source, { ...source, url: "file:///etc/passwd" }, { ...source, url: "javascript:alert(1)" }, { ...source, url: "https://user:secret@example.com" }] }),
    step("exec", { state: "failed", sources: [{ ...source, url: "https://example.com/docs#two" }] }), agent("reply", "https://not-a-source.example"),
    user("search-2"), agent("ordinary"),
  ]);
  assert.deepEqual(turnSources(turns[0]!), [{ ...source, url: "https://example.com/docs" }]);
  assert.deepEqual(turnSources(turns[1]!), []);
});
