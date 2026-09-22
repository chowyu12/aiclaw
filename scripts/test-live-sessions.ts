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
