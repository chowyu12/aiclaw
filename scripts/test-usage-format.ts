import { strict as assert } from "node:assert";
import { test } from "node:test";

import { formatCount, formatDuration, sourceLabel } from "../apps/desktop/src/renderer/usage-format.ts";

/** 用量页上的数字怎么写。 */

test("大数写成 K / M，一万以下带千分位", () => {
  assert.equal(formatCount(0), "0");
  assert.equal(formatCount(9876), "9,876");
  assert.equal(formatCount(12_345), "12.3K");
  assert.equal(formatCount(120_000), "120K");
  assert.equal(formatCount(1_234_567), "1.23M");
  assert.equal(formatCount(2_000_000_000), "2B");
  assert.equal(formatCount(Number.NaN), "0");
});

test("耗时", () => {
  assert.equal(formatDuration(0), "—");
  assert.equal(formatDuration(850), "850ms");
  assert.equal(formatDuration(1200), "1.2s");
  assert.equal(formatDuration(125_000), "2m 5s");
  assert.equal(formatDuration(120_000), "2m");
});

test("工具来源写成中文，代码模式里调的要标出来", () => {
  assert.equal(sourceLabel("builtin"), "内置");
  assert.equal(sourceLabel("mcp:aihot"), "MCP · aihot");
  assert.equal(sourceLabel("exec:mcp:aihot"), "代码模式 · MCP · aihot");
  assert.equal(sourceLabel("exec:builtin"), "代码模式 · 内置");
});
