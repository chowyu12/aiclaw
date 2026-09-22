import { strict as assert } from "node:assert";
import { test } from "node:test";

import { DEFAULT_CONFIG, normalizeConfig } from "../apps/desktop/src/main/config-defaults.ts";

/**
 * 配置默认值的归一化。
 *
 * 展开默认值（`{ ...DEFAULT_CONFIG, ...raw }`）看起来「有默认值兜底」，但兜的是
 * key 不存在的情况；显式写进去的 undefined/null、类型不对的值都要在这里钉住，
 * 而且错了不会报错——用户只会发现「配了没用」或者安全开关自己关了。
 */

test("默认没选模型服务；computer use 默认关", () => {
  assert.equal(DEFAULT_CONFIG.providerId, 0);
  assert.equal(DEFAULT_CONFIG.model, "");
  // computer use 默认必须是关的，见 AGENTS.md。
  assert.equal(DEFAULT_CONFIG.enableComputerUse, false);
});

test("旧配置里没有 providerId 时算 0，不是 NaN", () => {
  const stored = { ...DEFAULT_CONFIG } as Record<string, unknown>;
  delete stored.providerId;
  assert.equal(normalizeConfig(stored as never).providerId, 0);
  const junk = { ...DEFAULT_CONFIG, providerId: "abc" } as unknown as typeof DEFAULT_CONFIG;
  assert.equal(normalizeConfig(junk).providerId, 0);
  // 负数与小数都不是合法主键。
  assert.equal(normalizeConfig({ ...DEFAULT_CONFIG, providerId: -3 }).providerId, 0);
  assert.equal(normalizeConfig({ ...DEFAULT_CONFIG, providerId: 7.9 }).providerId, 7);
});

test("归一化只碰 providerId 与沙箱开关，别的原样传下去", () => {
  const source = { ...DEFAULT_CONFIG, retentionDays: 7, model: "gpt-x", providerId: 3 };
  const config = normalizeConfig(source);
  assert.equal(config.retentionDays, 7);
  assert.equal(config.model, "gpt-x");
  assert.equal(config.providerId, 3);
  assert.equal(config.enableComputerUse, false);
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
