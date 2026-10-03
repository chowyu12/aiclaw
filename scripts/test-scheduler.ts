import { strict as assert } from "node:assert";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import { Scheduler } from "../apps/desktop/src/main/scheduler.ts";
import { nextRun, type ScheduledTask } from "../apps/desktop/src/shared/schedule.ts";

/**
 * 调度器：到点开会话、跑完记结果、一次性的跑完就停、错过太久的不补、
 * 模型经工具建和删。宿主那一端（开会话、发通知）换成记录下来的假的。
 */

function fixture() {
  const dir = mkdtempSync(join(tmpdir(), "aiclaw-scheduler-"));
  const runs: string[] = [];
  const notes: { name: string; ok: boolean; detail: string }[] = [];
  let failNext = "";
  const scheduler = new Scheduler(dir, {
    async runTask(task) {
      if (failNext) {
        const message = failNext;
        failNext = "";
        throw new Error(message);
      }
      runs.push(task.name);
      return `s_${runs.length}`;
    },
    notify(task, ok, detail) {
      notes.push({ name: task.name, ok, detail });
    },
  });
  const file = join(dir, "schedules.json");
  const read = () => JSON.parse(readFileSync(file, "utf8")) as ScheduledTask[];
  const patch = (id: string, change: Partial<ScheduledTask>) => {
    const tasks = read().map((task) => (task.id === id ? { ...task, ...change } : task));
    writeFileSync(file, JSON.stringify(tasks));
  };
  return { scheduler, runs, notes, read, patch, fail: (message: string) => (failNext = message) };
}

const at = (y: number, m: number, d: number, h = 0, min = 0) => new Date(y, m - 1, d, h, min);

test("校验：没名字、没内容、规则不对都不让存", () => {
  const { scheduler } = fixture();
  assert.throws(() => scheduler.save({ name: " ", prompt: "x", rule: { kind: "daily", time: "09:00" } }), /名字/);
  assert.throws(() => scheduler.save({ name: "a", prompt: "", rule: { kind: "daily", time: "09:00" } }), /要做什么/);
  assert.throws(() => scheduler.save({ name: "a", prompt: "x", rule: { kind: "interval", everyMinutes: 1 } }), /至少 5 分钟/);
});

test("到点开会话；跑完记为完成并通知；不到点不跑", async () => {
  const { scheduler, runs, notes, read, patch } = fixture();
  const saved = scheduler.save({ name: "日报", prompt: "写日报", rule: { kind: "daily", time: "9:00" } });
  assert.equal(saved.rule.time, "09:00", "时间补齐两位");
  patch(saved.id, { anchorAt: at(2026, 9, 29, 10).toISOString() });

  await scheduler.tick(at(2026, 9, 30, 8, 59));
  assert.deepEqual(runs, [], "还没到");

  await scheduler.tick(at(2026, 9, 30, 9, 0));
  assert.deepEqual(runs, ["日报"]);
  const running = read()[0]!;
  assert.equal(running.lastStatus, "running");
  assert.equal(running.lastSessionId, "s_1");

  await scheduler.tick(at(2026, 9, 30, 9, 1));
  assert.deepEqual(runs, ["日报"], "正在跑的不重复开");

  scheduler.onTurnCompleted("s_1", undefined);
  assert.equal(read()[0]!.lastStatus, "ok");
  assert.deepEqual(notes, [{ name: "日报", ok: true, detail: "" }]);
  scheduler.onTurnCompleted("s_other", undefined);
  assert.equal(notes.length, 1, "别的会话结束不关它的事");
});

test("一次性的跑过就停用；失败记下原因并通知", async () => {
  const { scheduler, runs, notes, read, patch, fail } = fixture();
  const once = scheduler.save({ name: "提醒", prompt: "提醒我开会", rule: { kind: "once", at: at(2026, 10, 1, 14).toISOString() } });
  patch(once.id, { anchorAt: at(2026, 9, 30).toISOString() });
  fail("本地运行时未启动");
  await scheduler.tick(at(2026, 10, 1, 14, 0));
  assert.equal(read()[0]!.lastStatus, "failed");
  assert.equal(read()[0]!.lastError, "本地运行时未启动");
  assert.equal(notes[0]!.ok, false);
  assert.deepEqual(runs, []);
});

test("错过太久的不补，从现在重新算", async () => {
  const { scheduler, runs, read, patch } = fixture();
  const saved = scheduler.save({ name: "备份", prompt: "备份", rule: { kind: "daily", time: "23:00" } });
  patch(saved.id, { anchorAt: at(2026, 9, 20, 23, 30).toISOString() });
  const now = at(2026, 9, 30, 12);
  await scheduler.tick(now);
  assert.deepEqual(runs, []);
  const task = read()[0]!;
  assert.equal(task.lastStatus, "missed");
  assert.equal(task.anchorAt, now.toISOString());
  assert.deepEqual(nextRun(task.rule, new Date(task.anchorAt)), at(2026, 9, 30, 23));
});

test("停用的不跑；重新启用从现在算，不补关着的那几天", async () => {
  const { scheduler, runs, read, patch } = fixture();
  const saved = scheduler.save({ name: "巡检", prompt: "看看", rule: { kind: "interval", everyMinutes: 30 } });
  scheduler.setEnabled(saved.id, false);
  patch(saved.id, { anchorAt: at(2026, 9, 30, 8).toISOString() });
  await scheduler.tick(at(2026, 9, 30, 9));
  assert.deepEqual(runs, []);
  // 跟「重新启用前的这一刻」比，不跟某个写死的日历时间比：CI 跑在 UTC，写死的本地
  // 时间可能还在未来，断言就成了看当时几点。
  const before = Date.now();
  scheduler.setEnabled(saved.id, true);
  assert.ok(Date.parse(read()[0]!.anchorAt) >= before, "锚点挪到了重新启用的那一刻");
});

test("模型经工具建、列、删", () => {
  const { scheduler, read } = fixture();
  const created = scheduler.handleModelRequest({
    action: "create",
    task: { name: "邮件汇总", prompt: "汇总未读邮件", kind: "weekdays", time: "09:00", workspace: "/tmp/ws" },
  });
  assert.match(created, /已建好定时任务「邮件汇总」/);
  assert.match(created, /每个工作日 09:00/);
  const id = read()[0]!.id;
  assert.equal(read()[0]!.workspace, "/tmp/ws");
  assert.match(scheduler.handleModelRequest({ action: "list" }), new RegExp(`\\[${id}\\] 邮件汇总 · 每个工作日 09:00`));
  assert.throws(() => scheduler.handleModelRequest({ action: "create", task: { name: "x", prompt: "y", kind: "daily", time: "25:00" } }), /09:00/);
  assert.throws(() => scheduler.handleModelRequest({ action: "delete", id: "t_nope" }), /没有编号/);
  assert.match(scheduler.handleModelRequest({ action: "delete", id }), /已删掉/);
  assert.equal(scheduler.handleModelRequest({ action: "list" }), "还没有定时任务。");
});

test("改了时间规则就从现在重新算；只改名字不动排期", () => {
  const { scheduler, read, patch } = fixture();
  const saved = scheduler.save({ name: "a", prompt: "x", rule: { kind: "daily", time: "09:00" } });
  const old = at(2026, 1, 1).toISOString();
  patch(saved.id, { anchorAt: old });
  scheduler.save({ id: saved.id, name: "b", prompt: "x", rule: { kind: "daily", time: "09:00" } });
  assert.equal(read()[0]!.anchorAt, old);
  assert.equal(read()[0]!.name, "b");
  scheduler.save({ id: saved.id, name: "b", prompt: "x", rule: { kind: "daily", time: "10:00" } });
  assert.notEqual(read()[0]!.anchorAt, old);
});

test("follow-up keeps its chat, compares stable results and stops on verified completion", async () => {
 const dir = mkdtempSync(join(tmpdir(), "aiclaw-followup-"));
 const notes: string[] = [];
 const runs: ScheduledTask[] = [];
 let sequence = 0;
 const scheduler = new Scheduler(dir, {
  async runTask(task) { runs.push(task); return {sessionId: task.sessionId!, turnId: `turn_${++sequence}`}; },
  notify(_task, _ok, detail) { notes.push(detail); },
 });
 const task = scheduler.save({name: "watch", prompt: "check", rule: {kind: "interval", everyMinutes: 5}, mode: "followup", sessionId: "original", stopWhen: "all checks pass"});
 const report = (result: string, resultKey: string, complete = false) => {
  const run = runs.at(-1)!;
  scheduler.handleModelRequest({action: "report", id: task.id, sessionId: "original", runId: run.lastRunId, result, resultKey, complete});
  scheduler.onTurnCompleted("original", undefined, `turn_${sequence}`);
 };
 assert.equal(await scheduler.runNow(task.id), "original");
 report("still running", "running");
 assert.deepEqual(notes, ["still running"]);
 const restarted = new Scheduler(dir, {async runTask() { throw new Error("unused"); }, notify() {}});
 assert.equal(restarted.list()[0]!.lastResult, "still running");
 await scheduler.runNow(task.id); report("running, checked again", "running");
 assert.equal(notes.length, 1, "wording changes must stay quiet");
 await scheduler.runNow(task.id); report("checks passed", "passed", true);
 assert.deepEqual(notes, ["still running", "checks passed"]);
 const finished = scheduler.list()[0]!;
 assert.equal(finished.enabled, false);
 assert.equal(finished.stoppedReason, "all checks pass");
 assert.equal(finished.lastResult, "checks passed");
 assert.equal(new Set(runs.map(run => run.lastRunId)).size, 3, "each check needs a fresh identity");
});

test("follow-up rejects foreign/stale reports and duplicate dispatch, then pauses missing results", async () => {
 const dir = mkdtempSync(join(tmpdir(), "aiclaw-followup-"));
 let run: ScheduledTask | undefined;
 const notes: boolean[] = [];
 const scheduler = new Scheduler(dir, {
  async runTask(task) { run = task; return {sessionId: "original", turnId: "turn"}; },
  notify(_task, ok) { notes.push(ok); },
 });
 const task = scheduler.save({name: "watch", prompt: "check", rule: {kind: "interval", everyMinutes: 5}, mode: "followup", sessionId: "original"});
 await scheduler.runNow(task.id);
 await assert.rejects(scheduler.runNow(task.id), /已经在执行/);
 assert.throws(() => scheduler.handleModelRequest({action: "report", id: task.id, sessionId: "other", runId: run!.lastRunId, result: "bad", resultKey: "bad"}), /不属于/);
 assert.throws(() => scheduler.handleModelRequest({action: "report", id: task.id, sessionId: "original", runId: "old run", result: "bad", resultKey: "bad"}), /不属于/);
 scheduler.onTurnCompleted("original", undefined, "unrelated-user-turn");
 assert.equal(scheduler.list()[0]!.lastStatus, "running");
 scheduler.onTurnCompleted("original", undefined, "turn");
 assert.equal(scheduler.list()[0]!.lastStatus, "failed");
 assert.equal(scheduler.list()[0]!.enabled, false);
 assert.deepEqual(notes, [false]);
 assert.throws(() => scheduler.handleModelRequest({action: "report", id: task.id, sessionId: "original", runId: run!.lastRunId, result: "late", resultKey: "late"}), /不属于/);
});

test("restart pauses an uncertain execution rather than replaying it", async () => {
 const {scheduler, read} = fixture();
 const task = scheduler.save({name: "watch", prompt: "check", rule: {kind: "interval", everyMinutes: 5}, mode: "followup", sessionId: "original"});
 await scheduler.runNow(task.id);
 // Derive the persisted directory through a separate fixture for a real reopen.
 const dir = mkdtempSync(join(tmpdir(), "aiclaw-followup-restart-"));
 writeFileSync(join(dir, "schedules.json"), JSON.stringify(read()));
 let runs = 0;
 const reopened = new Scheduler(dir, {async runTask() { runs++; return "original"; }, notify() {}});
 assert.equal(reopened.list()[0]!.enabled, false);
 assert.equal(reopened.list()[0]!.lastStatus, "failed");
 await reopened.tick(new Date(Date.now() + 10 * 60_000));
 assert.equal(runs, 0);
});

test("completion arriving before the start receipt is reconciled to the right run", async () => {
 const dir = mkdtempSync(join(tmpdir(), "aiclaw-followup-early-"));
 const notes: string[] = [];
 const scheduler = new Scheduler(dir, {
  async runTask(task) {
   scheduler.bindRunSession(task.id, task.lastRunId!, "original");
   scheduler.handleModelRequest({action: "report", id: task.id, sessionId: "original", runId: task.lastRunId, result: "ready", resultKey: "ready"});
   scheduler.onTurnCompleted("original", undefined, "early");
   return {sessionId: "original", turnId: "early"};
  }, notify(_task, _ok, result) { notes.push(result); },
 });
 const task = scheduler.save({name: "watch", prompt: "check", rule: {kind: "interval", everyMinutes: 5}, mode: "followup", sessionId: "original"});
 await scheduler.runNow(task.id);
 assert.equal(scheduler.list()[0]!.lastStatus, "ok");
 assert.deepEqual(notes, ["ready"]);
});
