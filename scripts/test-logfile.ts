/**
 * 落盘日志的保留策略。
 *
 * 删文件的逻辑写错了**没人会发现**：要么日志一直堆着（磁盘慢慢满），
 * 要么把今天的也删了（真出事时什么都没有）。两种都要到出事那天才暴露，
 * 所以这里逐条钉。
 *
 * 跑法：make test-renderer
 */
import { strict as assert } from "node:assert";
import { test } from "node:test";

import { RETENTION_DAYS, expiredLogs, fileNameFor } from "../apps/desktop/src/main/logfile-rules.ts";

const NOW = new Date(2026, 8, 22); // 2026-09-22

test("文件名按天，本地时间", () => {
  assert.equal(fileNameFor(new Date(2026, 8, 3)), "aiclaw-2026-09-03.log");
  assert.equal(fileNameFor(new Date(2026, 11, 31)), "aiclaw-2026-12-31.log");
});

test("保留最近 7 天，更早的删掉", () => {
  const names = [
    "aiclaw-2026-09-22.log", // 今天
    "aiclaw-2026-09-16.log", // 第 7 天，保留
    "aiclaw-2026-09-15.log", // 第 8 天，删
    "aiclaw-2026-08-30.log",
  ];
  assert.deepEqual(expiredLogs(names, NOW), ["aiclaw-2026-09-15.log", "aiclaw-2026-08-30.log"]);
});

test("今天的绝不能删——真出事时要的就是它", () => {
  assert.deepEqual(expiredLogs(["aiclaw-2026-09-22.log"], NOW), []);
});

test("跨月跨年也按天数算，不是按月份编号", () => {
  const newYear = new Date(2027, 0, 2); // 2027-01-02
  const names = ["aiclaw-2026-12-28.log", "aiclaw-2026-12-26.log"];
  // 12-28 距 01-02 是 5 天，保留；12-26 是 7 天，刚好出界。
  assert.deepEqual(expiredLogs(names, newYear), ["aiclaw-2026-12-26.log"]);
});

test("不是我们的文件一律不动", () => {
  // 这个目录是用户能打开的，里面可能有他自己放的东西。
  const names = ["notes.txt", "aiclaw.log", "aiclaw-2026-09-15.log.bak", "aiclaw-2026-09-15.log"];
  assert.deepEqual(expiredLogs(names, NOW), ["aiclaw-2026-09-15.log"]);
});

test("保留天数就是 7", () => {
  assert.equal(RETENTION_DAYS, 7);
});
