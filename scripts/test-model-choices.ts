import { strict as assert } from "node:assert";
import { test } from "node:test";

import { filterChoices, modelChoices, usable } from "../apps/desktop/src/renderer/model-choices.ts";

/**
 * 模型清单的筛选。
 *
 * 一个服务能列出上百个模型（Azure 那种），下拉里的搜索是唯一能用的找法——
 * 筛错了的表现是「我明明有这个模型却搜不到」，而用户会以为是没配上。
 */

const providers = [
  { id: 1, name: "百炼", enabled: true, apiKeySet: true, models: ["deepseek-v4.1-flash", "qwen3-max"] },
  { id: 2, name: "Azure", enabled: true, apiKeySet: true, models: ["AI21-Jamba-1.5-Large", "Codestral-2501"] },
  { id: 3, name: "没 Key", enabled: true, apiKeySet: false, models: ["x"] },
  { id: 4, name: "停用了", enabled: false, apiKeySet: true, models: ["y"] },
  { id: 5, name: "清单空", enabled: true, apiKeySet: true, models: [] },
];

test("只有启用、配了 Key、清单非空的服务才算能用", () => {
  assert.equal(usable(providers[0]!), true);
  assert.equal(usable(providers[2]!), false);
  assert.equal(usable(providers[3]!), false);
  assert.equal(usable(providers[4]!), false);
});

test("铺平成「服务 × 模型」的可选清单", () => {
  const choices = modelChoices(providers);
  assert.deepEqual(
    choices.map((c) => `${c.providerName}/${c.model}`),
    ["百炼/deepseek-v4.1-flash", "百炼/qwen3-max", "Azure/AI21-Jamba-1.5-Large", "Azure/Codestral-2501"],
  );
});

test("搜索匹配模型名，忽略大小写", () => {
  const choices = modelChoices(providers);
  assert.deepEqual(filterChoices(choices, "JAMBA").map((c) => c.model), ["AI21-Jamba-1.5-Large"]);
  assert.deepEqual(filterChoices(choices, "deepseek").map((c) => c.model), ["deepseek-v4.1-flash"]);
});

test("搜索也匹配服务名——「百炼下面有什么」是常见的找法", () => {
  const choices = modelChoices(providers);
  assert.equal(filterChoices(choices, "百炼").length, 2);
});

test("空关键词与纯空格返回全部，不是返回空", () => {
  const choices = modelChoices(providers);
  assert.equal(filterChoices(choices, "").length, 4);
  assert.equal(filterChoices(choices, "   ").length, 4);
});

test("搜不到就是空，不做模糊兜底——给出一个不匹配的结果比空列表更让人困惑", () => {
  assert.equal(filterChoices(modelChoices(providers), "zzz").length, 0);
});
