import { strict as assert } from "node:assert";
import { test } from "node:test";

import { describeRule, dueState, formatWhen, nextRun, validateRule } from "../apps/desktop/src/shared/schedule.ts";

/**
 * 定时任务的时间规则。错了的表现是「该跑的没跑」或者「半夜跑了一堆」，
 * 都不会报错，所以按日历上容易出错的几处逐条钉：跨天、跨周、周末、错过补跑。
 * 时间都按本机时区构造，与调度器一致。
 */

const at = (y: number, m: number, d: number, h = 0, min = 0) => new Date(y, m - 1, d, h, min);

test("每天：今天还没到就是今天，过了就是明天；正好那一刻不算", () => {
  const rule = { kind: "daily" as const, time: "09:00" };
  assert.deepEqual(nextRun(rule, at(2026, 9, 30, 8, 59)), at(2026, 9, 30, 9, 0));
  assert.deepEqual(nextRun(rule, at(2026, 9, 30, 9, 0)), at(2026, 10, 1, 9, 0));
  assert.deepEqual(nextRun(rule, at(2026, 12, 31, 23, 0)), at(2027, 1, 1, 9, 0), "跨年");
});

test("工作日：周五之后是下周一", () => {
  const rule = { kind: "weekdays" as const, time: "18:30" };
  // 2026-10-02 是周五
  assert.deepEqual(nextRun(rule, at(2026, 10, 2, 19)), at(2026, 10, 5, 18, 30));
  assert.deepEqual(nextRun(rule, at(2026, 10, 3, 10)), at(2026, 10, 5, 18, 30), "周六");
});

test("每周几：挑最近的那一天", () => {
  const rule = { kind: "weekly" as const, time: "10:00", days: [1, 4] }; // 周一、周四
  assert.deepEqual(nextRun(rule, at(2026, 9, 30, 12)), at(2026, 10, 1, 10), "周三之后是周四");
  assert.deepEqual(nextRun(rule, at(2026, 10, 1, 11)), at(2026, 10, 5, 10), "周四过了是下周一");
  assert.equal(describeRule(rule), "每周一、周四 10:00");
  assert.equal(describeRule({ kind: "weekly", time: "10:00", days: [0, 6] }), "每周六、周日 10:00", "周日排在最后");
});

test("每隔 N 分钟：从建立那一刻算起，不对齐整点", () => {
  const rule = { kind: "interval" as const, everyMinutes: 30 };
  const anchor = at(2026, 9, 30, 9, 7);
  assert.deepEqual(nextRun(rule, anchor, anchor), at(2026, 9, 30, 9, 37));
  assert.deepEqual(nextRun(rule, at(2026, 9, 30, 10, 0), anchor), at(2026, 9, 30, 10, 7));
  assert.equal(describeRule({ kind: "interval", everyMinutes: 120 }), "每 2 小时");
  assert.match(validateRule({ kind: "interval", everyMinutes: 1 }), /至少 5 分钟/);
});

test("一次：过了就没有下一次", () => {
  const rule = { kind: "once" as const, at: at(2026, 10, 1, 8).toISOString() };
  assert.deepEqual(nextRun(rule, at(2026, 9, 30)), at(2026, 10, 1, 8));
  assert.equal(nextRun(rule, at(2026, 10, 1, 8)), null);
});

test("规则不合法时不排期", () => {
  assert.equal(nextRun({ kind: "daily", time: "25:00" }, at(2026, 9, 30)), null);
  assert.equal(nextRun({ kind: "weekly", time: "09:00", days: [] }, at(2026, 9, 30)), null);
  assert.match(validateRule({ kind: "daily", time: "9点" }), /09:00/);
});

test("错过 12 小时以内补跑一次，太久的不补", () => {
  const rule = { kind: "daily" as const, time: "23:00" };
  const lastRun = at(2026, 9, 28, 23, 0);
  assert.equal(dueState(rule, lastRun, at(2026, 9, 29, 22, 59)), "wait");
  assert.equal(dueState(rule, lastRun, at(2026, 9, 29, 23, 0)), "due");
  assert.equal(dueState(rule, lastRun, at(2026, 9, 30, 8, 0)), "due", "昨晚 11 点的，早上 8 点开机补");
  assert.equal(dueState(rule, lastRun, at(2026, 9, 30, 12, 0)), "missed", "13 小时前的不补");
});

test("时间写成人话", () => {
  const now = at(2026, 9, 30, 10);
  assert.equal(formatWhen(at(2026, 9, 30, 9, 5), now), "今天 09:05");
  assert.equal(formatWhen(at(2026, 10, 1, 9), now), "明天 09:00");
  assert.equal(formatWhen(at(2026, 10, 8, 9), now), "10月8日 09:00");
  assert.equal(formatWhen(at(2027, 1, 2, 9), now), "2027年1月2日 09:00");
});
