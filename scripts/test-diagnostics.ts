/**
 * 诊断报告的测试。
 *
 * 这里唯一严重的失败是**漏抹凭据**：报告的用途就是复制给别人，
 * 漏一次就是把等同登录态的 BFF Key 贴进了工单，而且当事人不会察觉。
 * 其余（环形缓冲、按行切）错了只是日志不好看。
 *
 * 跑法：make test-renderer
 */
import { strict as assert } from "node:assert";
import { test } from "node:test";

import {
  DiagnosticsLog,
  buildReport,
  redact,
} from "../apps/desktop/src/main/diagnostics.ts";

test("抹掉 sk- 开头的 Key", () => {
  assert.equal(redact("用了 sk-abc123XYZ_-9 这把"), "用了 sk-*** 这把");
});

test("抹掉 Authorization 头与 Bearer", () => {
  assert.match(redact("Authorization: Bearer eyJhbGciOi.xxx.yyy"), /\*\*\*/);
  assert.ok(!redact("Authorization: Bearer eyJhbGciOi.xxx.yyy").includes("eyJhbGciOi"));
});

test("抹掉 KEY=value 形式的凭据", () => {
  const text = redact("CLAW_TOKEN=abcdef123456 CLAW_URL=https://claw.example.internal");
  assert.ok(!text.includes("abcdef123456"), text);
  // 地址不是凭据，抹了反而失去排查价值。
  assert.ok(text.includes("https://claw.example.internal"), text);
});

test("抹掉 URL 上当钥匙用的查询参数", () => {
  // 真实例子：某个 MCP server 的 actor 参数就是一把钥匙。
  const text = redact("https://aihot.news/api/mcp?aihot_actor=000a8253-1726-45fb-953b-13cd");
  assert.ok(!text.includes("000a8253"), text);
  assert.ok(text.includes("https://aihot.news/api/mcp"), text);
});

test("报告里的每一处都过抹除，包括事实那几段", () => {
  const report = buildReport(
    {
      version: "0.1.4",
      electron: "38",
      node: "22",
      platform: "darwin",
      arch: "arm64",
      paths: { 配置: "/Users/x/Library/Application Support/aiclaw" },
      runtime: { 模型端点: "https://llm.example.internal/v1?key=verysecret123" },
      mounts: { aihot: "挂载失败：Authorization: Bearer topsecret" },
    },
    [{ at: 0, source: "kernel", text: "启动时 CLAW_TOKEN=leakedvalue" }],
  );
  for (const secret of ["verysecret123", "topsecret", "leakedvalue"]) {
    assert.ok(!report.includes(secret), `报告里漏了 ${secret}：\n${report}`);
  }
  // 该留的要留下，不然报告没用。
  assert.ok(report.includes("0.1.4"));
  assert.ok(report.includes("aihot"));
  assert.ok(report.includes("darwin arm64"));
});

test("日志环有上限，且按行切、丢空行", () => {
  const log = new DiagnosticsLog(3);
  log.push("kernel", "a\nb\n\n");
  log.push("kernel", "c\nd");
  const lines = log.all();
  assert.deepEqual(lines.map((line) => line.text), ["b", "c", "d"]);
  assert.ok(lines.every((line) => line.at > 0));
});
