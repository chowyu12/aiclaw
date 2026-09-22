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

test("没有 # 的是旧记录：只做对话、窗口未知", () => {
  assert.deepEqual(parseModelMark("gpt-4.1"), { name: "gpt-4.1", roles: [], context: 0 });
  assert.deepEqual(parseModelMark("  gpt-4.1  "), { name: "gpt-4.1", roles: [], context: 0 });
});

test("解析能力标记，忽略大小写与空格", () => {
  assert.deepEqual(parseModelMark("qwen3-vl#vision"), {
    name: "qwen3-vl",
    roles: ["vision"],
    context: 0,
  });
  assert.deepEqual(parseModelMark("m# VISION , image "), {
    name: "m",
    roles: ["vision", "image"],
    context: 0,
  });
});

test("解析上下文窗口", () => {
  assert.deepEqual(parseModelMark("m@131072"), { name: "m", roles: [], context: 131072 });
  assert.deepEqual(parseModelMark("m#vision@200000"), {
    name: "m",
    roles: ["vision"],
    context: 200000,
  });
});

test("窗口不是正整数时当作没写——一个 0 以外的怪值会让内核提前压缩", () => {
  assert.equal(parseModelMark("m@abc").context, 0);
  assert.equal(parseModelMark("m@-5").context, 0);
  assert.equal(parseModelMark("m@").context, 0);
});

test("不认识的标记丢掉而不是报错——清单是手写的", () => {
  assert.deepEqual(parseModelMark("m#nonsense").roles, []);
  assert.deepEqual(parseModelMark("m#vision,nonsense").roles, ["vision"]);
  assert.deepEqual(parseModelMark("m#").roles, []);
});

test("写出来的顺序固定，否则配置文件会无谓地变动", () => {
  assert.equal(formatModelMark({ name: "m", roles: ["image", "vision"] }), "m#vision,image");
  assert.equal(formatModelMark({ name: "m", roles: [], context: 8192 }), "m@8192");
  assert.equal(
    formatModelMark({ name: "m", roles: ["vision"], context: 8192 }),
    "m#vision@8192",
  );
  assert.equal(formatModelMark({ name: "m", roles: [] }), "m");
  assert.equal(formatModelMark({ name: "  m  ", roles: ["tts"] }), "m#tts");
});

test("往返不丢信息", () => {
  const entry = formatModelMark({ name: "qwen-tts", roles: ["tts"], context: 32768 });
  assert.deepEqual(parseModelMark(entry), { name: "qwen-tts", roles: ["tts"], context: 32768 });
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

test("候选带上窗口：选了之后那一格要能自动填", () => {
  const withWindow = [
    { id: 1, name: "百炼", enabled: true, apiKeySet: true, models: ["qwen3-vl#vision@131072"] },
  ];
  assert.equal(roleCandidates(withWindow, "vision")[0]!.context, 131072);
});

test("没有模型担任某角色时是空，不做兜底", () => {
  assert.deepEqual(roleCandidates(providers, "stt"), []);
});
