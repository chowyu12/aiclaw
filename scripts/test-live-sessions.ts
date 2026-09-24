/**
 * 事件按会话落地：切走的会话在后台照样跑，切回来能看到它跑到哪儿。
 *
 * 早先渲染层只有一条时间线，切会话就整个换掉；后台那一轮的事件继续到，
 * 全写进了正在看的会话里。这几条钉住的是「谁的事件进谁的记录」。
 *
 * 跑法：make test-renderer
 */
import { strict as assert } from "node:assert";
import { test } from "node:test";
import { reactive } from "vue";

import {
  applyAgentEvent,
  ensureLive,
  newLive,
  type LiveSession,
} from "../apps/desktop/src/renderer/live-sessions.ts";

function event(method: string, sessionId: string, extra: Record<string, unknown> = {}) {
  return { method, params: { sessionId, turnId: "t1", ...extra } };
}

function fresh(): Record<string, LiveSession> {
  return reactive({}) as Record<string, LiveSession>;
}

test("后台会话的事件不会写进正在看的会话", () => {
  const live = fresh();
  live.A = newLive();
  live.B = newLive();
  // 看着 B，A 在跑。
  applyAgentEvent(live, event("turn/started", "A"), "B");
  applyAgentEvent(live, event("item/started", "A", { item: { id: "m1", kind: "agentMessage" } }), "B");
  applyAgentEvent(live, event("item/delta", "A", { itemId: "m1", delta: "你好" }), "B");

  assert.equal(live.B!.timeline.length, 0, "B 不该收到 A 的条目");
  assert.equal(live.B!.busy, false, "B 没在跑");
  assert.equal(live.A!.busy, true);
  assert.equal(live.A!.timeline.length, 1);
  const entry = live.A!.timeline[0]!;
  assert.equal(entry.kind === "agent" && entry.text, "你好");
});

test("没打开过的会话来了事件也有地方落，切过去就能看", () => {
  const live = fresh();
  applyAgentEvent(live, event("item/started", "X", { item: { id: "s1", kind: "toolCall", toolName: "exec" } }), "");
  assert.ok(live.X, "应当按需建出记录");
  assert.equal(live.X!.timeline.length, 1);
  const step = live.X!.timeline[0]!;
  assert.equal(step.kind === "step" && step.state, "running");
});

test("turn/completed 只收尾自己那个会话，并把错误带出来", () => {
  const live = fresh();
  ensureLive(live, "A").busy = true;
  ensureLive(live, "B").busy = true;
  applyAgentEvent(live, event("item/started", "A", { item: { id: "s1", kind: "toolCall", toolName: "exec" } }), "B");

  const applied = applyAgentEvent(live, event("turn/completed", "A", { error: "上游 500" }), "B");

  assert.equal(applied.sessionId, "A");
  assert.equal(applied.error, "上游 500");
  assert.equal(live.A!.busy, false);
  assert.equal(live.B!.busy, true, "B 那一轮还在跑");
  const step = live.A!.timeline[0]!;
  assert.equal(step.kind === "step" && step.state, "done", "跑着的步骤要收成 done");
});

test("「已中断」不算错误", () => {
  const live = fresh();
  const applied = applyAgentEvent(live, event("turn/completed", "A", { error: "已中断" }), "A");
  assert.equal(applied.error, "");
});

test("没带 sessionId 的事件算到当前会话头上", () => {
  const live = fresh();
  const applied = applyAgentEvent(live, { method: "error", params: { message: "坏了" } }, "CUR");
  assert.equal(applied.sessionId, "CUR");
  assert.equal(applied.error, "坏了");
  assert.equal(live.CUR!.busy, false);
});

test("一个会话都没有时的事件不会炸也不会造出空记录", () => {
  const live = fresh();
  const applied = applyAgentEvent(live, { method: "error", params: { message: "坏了" } }, "");
  assert.equal(applied.sessionId, "");
  assert.deepEqual(Object.keys(live), []);
});

test("通道会话的提问要实时出现，不必切走再切回来", () => {
  const live = fresh();
  // 微信来的消息：没有本机回显可认领，直接追加。
  applyAgentEvent(
    live,
    event("item/completed", "C", { item: { id: "u1", kind: "userMessage", text: "你好" } }),
    "C",
  );
  applyAgentEvent(
    live,
    event("item/completed", "C", { item: { id: "m1", kind: "agentMessage", text: "你好！" } }),
    "C",
  );
  const kinds = live.C!.timeline.map((e) => e.kind);
  assert.deepEqual(kinds, ["user", "agent"], "问题要排在答案前面");
  const question = live.C!.timeline[0]!;
  assert.equal(question.kind === "user" && question.text, "你好");
});

test("本机发送的回显被内核的 userMessage 认领，不会重复", () => {
  const live = fresh();
  const record = ensureLive(live, "S");
  record.timeline.push({ kind: "user", id: "local-1", text: "帮我看看", pending: true });

  applyAgentEvent(
    live,
    event("item/completed", "S", { item: { id: "kernel-1", kind: "userMessage", text: "帮我看看" } }),
    "S",
  );

  assert.equal(record.timeline.length, 1, "不该多出一条");
  const entry = record.timeline[0]!;
  assert.equal(entry.id, "kernel-1", "要换成内核的 id，恢复历史时才对得上");
  assert.equal(entry.kind === "user" && entry.pending, undefined, "认领之后不再是待认领");
});

test("排队的两条各认各的，不会张冠李戴", () => {
  const live = fresh();
  const record = ensureLive(live, "S");
  record.timeline.push({ kind: "user", id: "local-1", text: "第一条", pending: true });
  record.timeline.push({ kind: "user", id: "local-2", text: "第二条", pending: true });

  applyAgentEvent(live, event("item/completed", "S", { item: { id: "k1", kind: "userMessage", text: "第一条" } }), "S");
  applyAgentEvent(live, event("item/completed", "S", { item: { id: "k2", kind: "userMessage", text: "第二条" } }), "S");

  assert.deepEqual(record.timeline.map((e) => e.id), ["k1", "k2"]);
  assert.equal(record.timeline.length, 2, "两条回显认领两条事件，不该有第三条");
});

test("消息带上时间：实时来的用内核的，认领回显时也换成内核的", () => {
  const live = fresh();
  applyAgentEvent(live, event("item/completed", "C", { item: { id: "u1", kind: "userMessage", text: "你好", at: 1000 } }), "C");
  applyAgentEvent(live, event("item/completed", "C", { item: { id: "m1", kind: "agentMessage", text: "嗨", at: 2000 } }), "C");
  const [question, answer] = live.C!.timeline;
  assert.equal(question!.kind === "user" && question!.at, 1000);
  assert.equal(answer!.kind === "agent" && answer!.at, 2000);

  const record = ensureLive(live, "S");
  record.timeline.push({ kind: "user", id: "local", text: "问", pending: true, at: 500 });
  applyAgentEvent(live, event("item/completed", "S", { item: { id: "k", kind: "userMessage", text: "问", at: 510 } }), "S");
  assert.equal(record.timeline[0]!.kind === "user" && record.timeline[0]!.at, 510);
});
