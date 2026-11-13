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
  restoreHistory,
  restoreSessionHistory,
  type TimelineEntry,
  newLive,
  type LiveSession,
} from "../apps/desktop/src/renderer/live-sessions.ts";

function event(method: string, sessionId: string, extra: Record<string, unknown> = {}) {
  return { method, params: { sessionId, turnId: "t1", ...extra } };
}

function fresh(): Record<string, LiveSession> {
  return reactive({}) as Record<string, LiveSession>;
}

test("AI 检查与人工确认分别更新同一条计时步骤，不重复追加", () => {
  const live = fresh();
  for (const [id, title, durationMs] of [["review", "AI 检查", 8000], ["wait", "人工确认", 12000]] as const) {
    applyAgentEvent(live, event("item/started", "A", { item: { id, kind: "notice", text: title, startedAt: 1000 } }), "A");
    const running = live.A!.timeline.at(-1)!;
    assert.ok(running.kind === "step" && running.state === "running" && running.startedAt === 1000);
    applyAgentEvent(live, event("item/completed", "A", { item: { id, kind: "notice", text: title + "结束", startedAt: 1000, durationMs } }), "A");
    assert.ok(running.kind === "step" && running.state === "done" && running.durationMs === durationMs && running.title === title + "结束");
  }
  assert.equal(live.A!.timeline.length, 2);
});

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
  // 英文界面下内核给的是英文。
  const english = applyAgentEvent(fresh(), event("turn/completed", "A", { error: "Interrupted" }), "A");
  assert.equal(english.error, "");
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

test("请求身份认领相同文字的消息；重复通知不消费第二条回显", () => {
  const live = fresh();
  const record = ensureLive(live, "A");
  record.timeline.push(
    { kind: "user", id: "local1", requestId: "r1", text: "", images: ["image1"], pending: true },
    { kind: "user", id: "local2", requestId: "r2", text: "", images: ["image2"], pending: true },
  );
  const confirmed = event("item/completed", "A", { item: { id: "server1", requestId: "r1", kind: "userMessage", text: "" } });
  applyAgentEvent(live, confirmed, "A");
  applyAgentEvent(live, confirmed, "A");
  assert.equal(record.timeline.length, 2);
  assert.equal(record.timeline[0]!.id, "server1");
  assert.equal(record.timeline[1]!.id, "local2");
});

test("search sources propagate through both live completion and history restoration", () => {
  const sources = [{title: "Docs", url: "https://example.com", snippet: "Summary"}];
  const live: Record<string, LiveSession> = {};
  applyAgentEvent(live, event("item/started", "A", {item: {id:"search", kind:"toolCall", toolName:"search__web_search"}}), "A");
  applyAgentEvent(live, event("item/completed", "A", {item: {id:"search", kind:"toolCall", sources}}), "A");
  assert.deepEqual((live.A!.timeline[0] as Extract<TimelineEntry,{kind:"step"}>).sources, sources);
  const restored = restoreHistory([{id:"search", kind:"toolCall", sources}]);
  assert.deepEqual((restored[0] as Extract<TimelineEntry,{kind:"step"}>).sources, sources);
});

test("queued optimistic input moves after the preceding answer when execution starts", () => {
  const live = { A: newLive() };
  live.A.timeline.push(
    {kind:"user",id:"first",text:"first task"},
    {kind:"user",id:"local",requestId:"queued-request",text:"next task",pending:true,queued:true},
    {kind:"agent",id:"answer",text:"first answer",streaming:false},
  );
  applyAgentEvent(live,event("item/completed","A",{item:{id:"confirmed",requestId:"queued-request",kind:"userMessage",text:"next task"}}),"A");
  assert.deepEqual(live.A.timeline.map(item=>item.id),["first","answer","confirmed"]);
  assert.equal(live.A.timeline[2]?.kind==="user" && live.A.timeline[2].queued,undefined);
});

test("background completion marks only the finished conversation unread", () => {
  const live = fresh();
  applyAgentEvent(live, event("turn/started", "A"), "A");
  applyAgentEvent(live, event("item/completed", "A", { item: { id: "reply", kind: "agentMessage", text: "done" } }), "B", "B");
  assert.ok(!live.A!.unread, "streamed output is not a completion notification");
  applyAgentEvent(live, event("turn/completed", "A"), "B", "B");
  assert.equal(live.A!.unread, true);
  assert.equal(live.A!.busy, false);
  assert.equal(live.B, undefined);
});

test("settings hides the selected conversation; visible completion stays read", () => {
  const live = fresh();
  applyAgentEvent(live, event("turn/started", "A"), "A", "");
  applyAgentEvent(live, event("turn/completed", "A"), "A", "");
  assert.equal(live.A!.unread, true);
  live.A!.unread = false;
  applyAgentEvent(live, event("turn/started", "A"), "A", "A");
  applyAgentEvent(live, event("turn/completed", "A"), "A", "A");
  assert.equal(live.A!.unread, false);
  // A replay after reading the result must not create another notification.
  applyAgentEvent(live, event("turn/completed", "A"), "B", "B");
  assert.equal(live.A!.unread, false);
});

test("stopped turns do not create unread completion dots; failures do", () => {
  for (const error of ["Interrupted", "已中断", "model unavailable"]) {
    const live = fresh();
    applyAgentEvent(live, event("turn/started", "A"), "B");
    applyAgentEvent(live, event("turn/completed", "A", { error }), "B");
    assert.equal(!!live.A!.unread, error === "model unavailable");
  }
});

test("code-mode 子调用在实时事件与恢复历史中保留父步骤和失败状态", () => {
  const live = fresh();
  const item = { id: "child", parentId: "exec", kind: "toolCall", toolName: "read_file", toolArgs: '{}', toolResult: "denied", toolFailed: true, durationMs: 12 };
  applyAgentEvent(live, event("item/started", "A", { item }), "A");
  applyAgentEvent(live, event("item/completed", "A", { item }), "A");
  const step = live.A!.timeline[0]!;
  assert.ok(step.kind === "step");
  assert.equal(step.parentId, "exec");
  assert.equal(step.state, "failed");
  const restored = restoreHistory([item])[0]!;
  assert.ok(restored.kind === "step");
  assert.equal(restored.parentId, "exec");
  assert.equal(restored.durationMs, 12);
});

test("agent start text, deltas and authoritative completion merge once across tools", () => {
 const live=fresh();
 applyAgentEvent(live,event('item/started','A',{item:{id:'m',kind:'agentMessage',text:'Heading\n'}}),'A');
 applyAgentEvent(live,event('item/delta','A',{itemId:'m',delta:'tail'}),'A');
 applyAgentEvent(live,event('item/started','A',{item:{id:'m',kind:'agentMessage',text:'Heading\n'}}),'A');
 assert.equal((live.A!.timeline[0] as {text:string}).text,'Heading\ntail');
 applyAgentEvent(live,event('item/started','A',{item:{id:'tool',kind:'toolCall',toolName:'read_file'}}),'A');
 applyAgentEvent(live,event('item/completed','A',{item:{id:'m',kind:'agentMessage',text:'## Corrected\nfinal'}}),'A');
 const messages=live.A!.timeline.filter(i=>i.kind==='agent');assert.equal(messages.length,1);
 assert.equal(messages[0]!.text,'## Corrected\nfinal');assert.equal(messages[0]!.streaming,false);
});
test("reasoning completion without start and restored sampling retain summary and timings",()=>{
 const live=fresh(),item={id:'sampling',kind:'llm',text:'Visible summary',startedAt:1000,durationMs:500,ttftMs:200,toolFailed:true};
 applyAgentEvent(live,event('item/completed','A',{item}),'A');
 const step=live.A!.timeline[0];assert.ok(step?.kind==='step');assert.equal(step.detail,item.text);
 const restored=restoreHistory([item])[0];assert.ok(restored?.kind==='step');assert.equal(restored.detail,item.text);assert.equal(restored.durationMs,500);assert.equal(restored.state,'failed');
});
test("both visible panes suppress unread while settings hide both",()=>{
 const live=fresh();
 for(const id of ['A','B','C']) {applyAgentEvent(live,event('turn/started',id),'A',['A','B']);applyAgentEvent(live,event('turn/completed',id),'A',['A','B']);}
 assert.equal(!!live.A!.unread,false);assert.equal(!!live.B!.unread,false);assert.equal(live.C!.unread,true);
 applyAgentEvent(live,event('turn/started','B'),'A',[]);applyAgentEvent(live,event('turn/completed','B'),'A',[]);assert.equal(live.B!.unread,true);
});

test("delayed history merges completed events and keeps authoritative user delivery", () => {
 const live=fresh(), record=ensureLive(live,'race'), revision=record.revision??0;
 applyAgentEvent(live,event('turn/started','race'),'race');
 applyAgentEvent(live,event('item/completed','race',{item:{id:'fresh',kind:'agentMessage',text:'just finished'}}),'race');
 applyAgentEvent(live,event('turn/completed','race'),'race');
 restoreSessionHistory(record,[{id:'old',kind:'agentMessage',text:'old answer'}],revision);
 assert.deepEqual(record.timeline.map(i=>i.id),['old','fresh']);assert.equal(record.busy,false);
 const pending=newLive([{kind:'user',id:'optimistic',requestId:'request',text:'hi',pending:true}]);pending.busy=true;
 restoreSessionHistory(pending,[{id:'confirmed',kind:'userMessage',requestId:'request',text:'hi'}],0);
 assert.equal(pending.timeline.length,1);assert.equal(pending.timeline[0]?.id,'confirmed');
});
test("live streaming content wins over an older snapshot without duplicating its item",()=>{
 const record=newLive([{kind:'agent',id:'m',text:'fresh delta',streaming:true}]);record.busy=true;
 restoreSessionHistory(record,[{id:'old',kind:'agentMessage',text:'older'},{id:'m',kind:'agentMessage',text:'stale delta'}],0);
 assert.deepEqual(record.timeline.map(i=>i.id),['old','m']);assert.equal((record.timeline[1] as {text:string}).text,'fresh delta');
});
