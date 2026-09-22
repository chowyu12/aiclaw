import { strict as assert } from "node:assert";
import { test } from "node:test";

import {
  DEFAULT_CLAW_URL,
  DEFAULT_CONFIG,
  DEFAULT_MODEL_BASE_URL,
  normalizeConfig,
  parseCapabilityChoices,
} from "../apps/desktop/src/main/config-defaults.ts";

/**
 * 配置默认值的归一化。
 *
 * 只有一件事值得钉住：**空串会盖掉默认值**。
 * `{ ...DEFAULT_CONFIG, ...raw }` 看起来「有默认值兜底」，但兜的是 key 不存在的
 * 情况；早于这个版本写下的 config.json 里这两项存的正好是空串，于是升级上来的
 * 用户拿到的是空地址，而装机的新用户拿到的是默认地址——两种人看到的应用行为不同，
 * 且报错发生在上游，指不回配置页。
 */

test("空地址折回默认值——旧 config.json 里存的就是空串", () => {
  const stored = { ...DEFAULT_CONFIG, modelBaseUrl: "", clawUrl: "" };
  const merged = { ...DEFAULT_CONFIG, ...stored };
  // 先确认问题真的存在：不归一化的话展开默认值救不了。
  assert.equal(merged.modelBaseUrl, "");

  const config = normalizeConfig(merged);
  assert.equal(config.modelBaseUrl, DEFAULT_MODEL_BASE_URL);
  assert.equal(config.clawUrl, DEFAULT_CLAW_URL);
});

test("只有空白也算空——输入框里剩个空格不该变成一个打不通的地址", () => {
  const config = normalizeConfig({ ...DEFAULT_CONFIG, modelBaseUrl: "  ", clawUrl: "\t" });
  assert.equal(config.modelBaseUrl, DEFAULT_MODEL_BASE_URL);
  assert.equal(config.clawUrl, DEFAULT_CLAW_URL);
});

test("填了的地址原样保留", () => {
  const config = normalizeConfig({
    ...DEFAULT_CONFIG,
    modelBaseUrl: "http://127.0.0.1:8080/v1",
    clawUrl: "http://127.0.0.1:9000",
  });
  assert.equal(config.modelBaseUrl, "http://127.0.0.1:8080/v1");
  assert.equal(config.clawUrl, "http://127.0.0.1:9000");
});

test("归一化只碰这两个字段，别的原样传下去", () => {
  const source = { ...DEFAULT_CONFIG, workdir: "/tmp/x", retentionDays: 7, model: "gpt-x" };
  const config = normalizeConfig(source);
  assert.equal(config.workdir, "/tmp/x");
  assert.equal(config.retentionDays, 7);
  assert.equal(config.model, "gpt-x");
  assert.equal(config.enableComputerUse, false);
});

test("默认值本身不为空——默认没填等于这一整套折回逻辑白写", () => {
  assert.ok(DEFAULT_CONFIG.modelBaseUrl.startsWith("https://"));
  assert.ok(DEFAULT_CONFIG.clawUrl.startsWith("https://"));
  // computer use 默认必须是关的，见 AGENTS.md。
  assert.equal(DEFAULT_CONFIG.enableComputerUse, false);
});

// ---------- 能力开关的存档格式 ----------
//
// 唯一的难点是兼容旧格式，而兼容写错了**不会报错**：升级上来的用户会发现
// 自己关掉的能力一次性全开回来，还以为是自动同步把它们打开的。

test("旧格式（关掉的键数组）读成一组 false", () => {
  assert.deepEqual(parseCapabilityChoices(["data-api:1", "corpus:7"]), {
    "data-api:1": false,
    "corpus:7": false,
  });
});

test("新格式双向选择原样读出来", () => {
  // 记双向是因为联网搜索那类里有些候选默认是关的，手动打开也要记住。
  assert.deepEqual(parseCapabilityChoices({ "web-search:12": true, "data-api:1": false }), {
    "web-search:12": true,
    "data-api:1": false,
  });
});

test("坏数据当成没有选择过，不要让一个坏文件把能力全关掉", () => {
  assert.deepEqual(parseCapabilityChoices(null), {});
  assert.deepEqual(parseCapabilityChoices("nonsense"), {});
  assert.deepEqual(parseCapabilityChoices({ a: "yes", b: 1, c: true }), { c: true });
  assert.deepEqual(parseCapabilityChoices([1, "data-api:1"]), { "data-api:1": false });
});

// ---------- 沙箱开关的缺省 ----------

test("老配置里没有沙箱这一项时，算它开着", () => {
  // 安全开关的缺省必须落在安全那一侧。展开默认值能兜住「key 不存在」，
  // 兜不住显式写进去的 undefined/null——升级路径上出现过。
  const stored = { ...DEFAULT_CONFIG } as Record<string, unknown>;
  delete stored.sandboxCommands;
  assert.equal(normalizeConfig(stored as never).sandboxCommands, true);

  const nulled = { ...DEFAULT_CONFIG, sandboxCommands: undefined } as Record<string, unknown>;
  assert.equal(normalizeConfig(nulled as never).sandboxCommands, true);
});

test("明确关掉的要保持关着——不然这个开关等于没有", () => {
  assert.equal(normalizeConfig({ ...DEFAULT_CONFIG, sandboxCommands: false }).sandboxCommands, false);
});
