import { strict as assert } from "node:assert";
import { test } from "node:test";

import {
  formatModelMark,
  modelHasRole,
  parseModelMark,
  roleCandidates,
} from "../apps/desktop/src/renderer/model-roles.ts";

/**
 * 模型能力标记。
 *
 * 与 Go 侧 protocol/roles_marks.go 是同一套规则，两边都有测试——解析不一致的
 * 表现是「界面上标了视觉，内核却仍走转述」，而那种错不会报出来。
 */

test("没有 # 的是旧记录，只做对话", () => {
  assert.deepEqual(parseModelMark("gpt-4.1"), { name: "gpt-4.1", roles: [] });
  assert.deepEqual(parseModelMark("  gpt-4.1  "), { name: "gpt-4.1", roles: [] });
});

test("解析能力标记，忽略大小写与空格", () => {
  assert.deepEqual(parseModelMark("qwen3-vl#vision"), { name: "qwen3-vl", roles: ["vision"] });
  assert.deepEqual(parseModelMark("m# VISION , image "), { name: "m", roles: ["vision", "image"] });
});

test("不认识的标记丢掉而不是报错——清单是手写的", () => {
  assert.deepEqual(parseModelMark("m#nonsense").roles, []);
  assert.deepEqual(parseModelMark("m#vision,nonsense").roles, ["vision"]);
  assert.deepEqual(parseModelMark("m#").roles, []);
});

test("写出来的顺序固定，否则配置文件会无谓地变动", () => {
  assert.equal(formatModelMark("m", ["image", "vision"]), "m#vision,image");
  assert.equal(formatModelMark("m", []), "m");
  assert.equal(formatModelMark("  m  ", ["tts"]), "m#tts");
});

test("往返不丢信息", () => {
  const entry = formatModelMark("qwen-tts", ["tts"]);
  assert.deepEqual(parseModelMark(entry), { name: "qwen-tts", roles: ["tts"] });
});

test("modelHasRole 只认标了的那个", () => {
  assert.equal(modelHasRole("m#vision", "vision"), true);
  assert.equal(modelHasRole("m#image", "vision"), false);
  assert.equal(modelHasRole("m", "vision"), false);
});

const providers = [
  { id: 1, name: "百炼", enabled: true, apiKeySet: true, models: ["qwen3-vl#vision", "wan#image", "chat"] },
  { id: 2, name: "没 Key", enabled: true, apiKeySet: false, models: ["x#vision"] },
  { id: 3, name: "停用", enabled: false, apiKeySet: true, models: ["y#vision"] },
];

test("候选只来自启用且配了 Key 的服务", () => {
  const vision = roleCandidates(providers, "vision");
  assert.deepEqual(vision.map((c) => `${c.providerName}/${c.model}`), ["百炼/qwen3-vl"]);
});

test("候选里的模型名不带标记——那是要发给服务的名字", () => {
  assert.equal(roleCandidates(providers, "image")[0]!.model, "wan");
});

test("没有模型担任某角色时是空，不做兜底", () => {
  assert.deepEqual(roleCandidates(providers, "stt"), []);
});
