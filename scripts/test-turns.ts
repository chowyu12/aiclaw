import { strict as assert } from "node:assert";
import { test } from "node:test";

import { groupTurns, turnBlocks, finalMessage, imageArtifacts, turnArtifacts, turnSources, stepsElapsed } from "../apps/desktop/src/renderer/turns.ts";
import { activitySummary } from "../apps/desktop/src/renderer/activity-summary.ts";
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

test("截图会话中的辅助脚本只留在步骤，不成为回答附件", () => {
  const paths = ["/Users/user/yunfang/window-shots/list_windows.swift", "/Users/user/yunfang/window-shots/list_ego.swift"];
  const turn = groupTurns([
    user("mail", "列举我最新的10封邮件"),
    step("exec", { artifacts: paths }),
    agent("answer", "截图覆盖全部应用窗口，过程中新建的窗口已关闭。"),
  ])[0]!;
  assert.deepEqual(turnArtifacts(turn), []);
  assert.deepEqual(imageArtifacts(turn), []);
  assert.deepEqual(turn.steps[0]!.artifacts, paths);
});

test("任意中间文件不会自动交付，旧存档中的产物记录同样处理", () => {
  const paths = ["helper.swift", "inspect.py", "report.md", "report.docx", "tmp/screenshot.png", ".archify/run/candidate.json", "receipt.json", "speech.wav"];
  const turn = groupTurns([user("u"), step("write", { artifacts: paths }), agent("answer", "已完成。")])[0]!;
  assert.deepEqual(turnArtifacts(turn), []);
  assert.deepEqual(imageArtifacts(turn), []);
  assert.deepEqual(turn.steps[0]!.artifacts, paths);
});

test("仅最终回答选择交付文件，保留未由内置写文件工具生成的交付链接", () => {
  const turn = groupTurns([
    user("report"),
    agent("progress", "检查 `helper.py` 和 `tmp/screenshot.png`。"),
    step("write", { artifacts: ["helper.py", "tmp/screenshot.png", "report.docx", "final.png"] }),
    agent("answer", "[报告](/work/report.docx) ![最终图](/work/final.png) [命令生成的文件](/work/export.xlsx)"),
  ])[0]!;
  assert.deepEqual(turnArtifacts(turn).sort(), ["/work/export.xlsx", "/work/final.png", "/work/report.docx"]);
  assert.deepEqual(imageArtifacts(turn), ["/work/final.png"]);
});

test("流式回答或只有工具步骤时不提前发布产物，轮次之间不串图", () => {
  const turns = groupTurns([
    user("u1"), step("image", { artifacts: ["first.png"] }), agent("a1", "![图](/work/first.png)"),
    user("u2"), step("next", { artifacts: ["second.png"] }),
  ]);
  assert.deepEqual(imageArtifacts(turns[0]!), ["/work/first.png"]);
  assert.deepEqual(imageArtifacts(turns[1]!), []);
  turns[1]!.messages.push({ kind: "agent", id: "stream", text: "![图](/work/second.png)", streaming: true });
  turns[1]!.entries.push(turns[1]!.messages[0]!);
  assert.deepEqual(imageArtifacts(turns[1]!), []);
  turns[1]!.messages[0]!.streaming = false;
  assert.deepEqual(imageArtifacts(turns[1]!), ["/work/second.png"]);
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


test("进展和连续工具按原顺序放入过程，最终回答独立", () => {
  const turn = groupTurns([
    user("u"), agent("start", "先检查本地记录。"),
    step("read", { title: "read_file" }), step("command", { title: "exec" }),
    agent("finding", "已找到原因，接下来修复。"), step("write", { title: "write_file" }),
    agent("final", "已修复并验证。"),
  ])[0]!;
  assert.equal(finalMessage(turn)?.text, "已修复并验证。");
  const blocks = turnBlocks(turn);
  assert.deepEqual(blocks.map(block => block.kind), ["message", "activity", "message", "activity"]);
  assert.deepEqual(blocks.filter(block => block.kind === "activity").map(block => block.steps.map(s => s.id)), [["read", "command"], ["write"]]);
  assert.ok(blocks.every(block => block.key !== "final"));
  assert.equal(turnBlocks(turn, true).at(-1)?.key, "final");
});

test("中断在工具或采样阶段时，上一条进展不能冒充最终回答", () => {
  for (const kind of ["tool", "llm"] as const) {
    const turn = groupTurns([user("u"), agent("progress", "接下来检查。"), step("failed", { step: kind, state: "failed" })])[0]!;
    assert.equal(finalMessage(turn), null);
    assert.deepEqual(turnArtifacts(turn), []);
    assert.equal(turnBlocks(turn)[0]?.key, "progress");
  }
});

test("流式进展、失败和采样信息可追溯，完成的纯采样不占活动行", () => {
  const turn = groupTurns([user("u"), step("thinking", { step: "llm" }), agent("final", "答案")])[0]!;
  assert.deepEqual(turnBlocks(turn), []);
  assert.equal(turn.steps[0]!.id, "thinking");
  turn.steps[0]!.state = "running";
  assert.equal(turnBlocks(turn, true)[0]?.key, "thinking");
  turn.messages[0]!.streaming = true;
  assert.equal(finalMessage(turn), null);
});

test("活动摘要只汇总已完成的操作，运行和失败不冒充成功", () => {
  const steps = groupTurns([user("u"),
    step("read", { title: "read_file" }),
    step("run", { title: "exec · 验证修复", state: "running" }),
    step("write", { title: "write_file", state: "failed" }),
  ])[0]!.steps;
  assert.equal(activitySummary(steps), "读取了 1 项内容 · 有操作失败 · 正在执行：验证修复");
  assert.equal(activitySummary([{ ...steps[0]!, step: "llm", state: "running" }]), "正在思考…");
  assert.equal(activitySummary([{ ...steps[0]!, step: "llm", state: "failed" }]), "处理失败");
});
