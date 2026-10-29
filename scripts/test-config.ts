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

test("默认没选模型服务", () => {
  assert.equal(DEFAULT_CONFIG.providerId, 0);
  assert.equal(DEFAULT_CONFIG.model, "");
});

test("桌面只保留默认、严格和全部放行；旧无人值守迁回默认", () => {
  assert.equal(DEFAULT_CONFIG.profile, "auto");
  for (const profile of ["on-write", "never", "unknown", undefined, null]) {
    assert.equal(normalizeConfig({ ...DEFAULT_CONFIG, profile } as never).profile, "auto");
  }
  for (const profile of ["auto", "always", "bypass"] as const) {
    assert.equal(normalizeConfig({ ...DEFAULT_CONFIG, profile }).profile, profile);
  }
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

test("prompt presets migrate safely and preserve bounded explicit slot contents", async () => {
  const {normalizePresets, presetCandidates, applyPreset} = await import("../apps/desktop/src/shared/presets.ts");
  assert.deepEqual(normalizePresets(null),[]);
  assert.deepEqual(normalizeConfig({...DEFAULT_CONFIG,presets:undefined} as never).presets,[]);
  const list = normalizePresets([{slot:2,title:"Review",text:"Check code\nReport bugs"},{slot:2,title:"duplicate",text:"ignore"},{slot:11,title:"bad",text:"ignore"},{slot:1,title:"SQL",text:"Explain SQL"}]);
  assert.deepEqual(list.map(p => p.slot),[1,2]);
  assert.equal(presetCandidates("preset2",list)[0]?.title,"Review");
  assert.equal(presetCandidates("review",list).length,1);
  assert.deepEqual(applyPreset("/preset2 keep this draft",0,8,list[1]!),{text:"Check code\nReport bugs keep this draft",caret:22});
  const long=normalizePresets([{slot:1,title:"a".repeat(100),text:"x".repeat(25000)}])[0]!;
  assert.equal(long.title.length,80);assert.equal(long.text.length,20000);
});

// Desktop session creation, background tasks and workspace reset share this fallback.
test("未选择工作区时使用 Home 下的 aiclaw 目录", async () => {
  const { resolveWorkspace } = await import("../apps/desktop/src/main/workspace-defaults.ts");
  const { homedir } = await import("node:os");
  const { join } = await import("node:path");
  for (const workspace of [undefined, "", "   "]) {
    assert.equal(resolveWorkspace(workspace), join(homedir(), "aiclaw"));
  }
  assert.equal(resolveWorkspace(" /custom/project "), "/custom/project");
});

test("project configuration preserves ordered multiple directories and validates input", async () => {
 const { normalizeProject }=await import("../apps/desktop/src/shared/projects.ts");
 assert.deepEqual(normalizeProject({name:" Project ",directories:[" /first ","/second","/first",""]}),{id:undefined,name:"Project",directories:["/first","/second"]});
 assert.deepEqual(normalizeProject({name:"Default",directories:[]}).directories,[]);
 assert.throws(()=>normalizeProject({name:" ",directories:[]}));
 assert.throws(()=>normalizeProject({name:"Bad",directories:null} as never));
 assert.throws(()=>normalizeProject({name:"Bad",directories:[1]} as never));
});

test("new project chats choose only configured directories with a Home fallback", async () => {
 const {projectWorkspace,resolveWorkspace}=await import("../apps/desktop/src/main/workspace-defaults.ts");
 const project={directories:["/first","/second"]};
 assert.equal(projectWorkspace(project),"/first");
 assert.equal(projectWorkspace(project," /second "),"/second");
 assert.equal(projectWorkspace({directories:[]}),resolveWorkspace());
 assert.equal(projectWorkspace(undefined),resolveWorkspace());
 assert.equal(projectWorkspace(undefined,"/explicit"),"/explicit");
 assert.throws(()=>projectWorkspace(project,"/outside"));
});
