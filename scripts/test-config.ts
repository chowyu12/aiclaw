import { strict as assert } from "node:assert";
import { test } from "node:test";

import {
  DEFAULT_CONFIG,
  DEFAULT_MODEL_BASE_URL,
  normalizeConfig,
} from "../apps/desktop/src/main/config-defaults.ts";

/**
 * 配置默认值的归一化。
 *
 * 只有一件事值得钉住：**空串会盖掉默认值**。
 * `{ ...DEFAULT_CONFIG, ...raw }` 看起来「有默认值兜底」，但兜的是 key 不存在的
 * 情况；早于这个版本写下的 config.json 里这一项存的正好是空串，于是升级上来的
 * 用户拿到的是空地址，而装机的新用户拿到的是默认地址——两种人看到的应用行为不同，
 * 且报错发生在上游，指不回配置页。
 */

test("空地址折回默认值——旧 config.json 里存的就是空串", () => {
  const stored = { ...DEFAULT_CONFIG, modelBaseUrl: "" };
  const merged = { ...DEFAULT_CONFIG, ...stored };
  // 先确认问题真的存在：不归一化的话展开默认值救不了。
  assert.equal(merged.modelBaseUrl, "");

  const config = normalizeConfig(merged);
  assert.equal(config.modelBaseUrl, DEFAULT_MODEL_BASE_URL);
});

test("只有空白也算空——输入框里剩个空格不该变成一个打不通的地址", () => {
  const config = normalizeConfig({ ...DEFAULT_CONFIG, modelBaseUrl: "  " });
  assert.equal(config.modelBaseUrl, DEFAULT_MODEL_BASE_URL);
});

test("填了的地址原样保留", () => {
  const config = normalizeConfig({ ...DEFAULT_CONFIG, modelBaseUrl: "http://127.0.0.1:8080/v1" });
  assert.equal(config.modelBaseUrl, "http://127.0.0.1:8080/v1");
});

test("归一化只碰地址与沙箱开关，别的原样传下去", () => {
  const source = { ...DEFAULT_CONFIG, workdir: "/tmp/x", retentionDays: 7, model: "gpt-x" };
  const config = normalizeConfig(source);
  assert.equal(config.workdir, "/tmp/x");
  assert.equal(config.retentionDays, 7);
  assert.equal(config.model, "gpt-x");
  assert.equal(config.enableComputerUse, false);
});

test("默认值本身不为空——默认没填等于这一整套折回逻辑白写", () => {
  assert.ok(DEFAULT_CONFIG.modelBaseUrl.startsWith("https://"));
  // computer use 默认必须是关的，见 AGENTS.md。
  assert.equal(DEFAULT_CONFIG.enableComputerUse, false);
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
