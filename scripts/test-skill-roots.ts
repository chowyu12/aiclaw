import { strict as assert } from "node:assert";
import { mkdirSync, mkdtempSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import {
  dedupeByName,
  discoverSkills,
  scanRoot,
  skillRoots,
} from "../apps/desktop/src/main/skill-roots.ts";

/**
 * 技能发现。
 *
 * 这套逻辑的坑全在文件系统的细节上，而错了之后**界面只是少几个技能**——
 * 没有报错、没有日志，用户只会觉得「怎么没认出来」。所以这里按真实布局造
 * 临时目录逐条钉：软链、多来源重复、带版本的 CLI 缓存、@scope 的 npm 包。
 */

function skill(dir: string, name: string): string {
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, "SKILL.md"), `---\nname: ${name}\ndescription: 用于${name}\n---\n正文`);
  return dir;
}

/** 造一个和真机一样的家目录布局。 */
function fakeHome(): string {
  const home = mkdtempSync(join(tmpdir(), "aiclaw-skills-"));

  // npx 装的 CLI 把技能放进缓存，再软链到 Claude 与 Codex 下——真机就是这样。
  const cache = join(home, ".example-cli", "skill-cache");
  skill(join(cache, "example-ops-gitlab", "1.0.4"), "gitlab-old");
  skill(join(cache, "example-ops-gitlab", "1.0.8"), "gitlab");

  mkdirSync(join(home, ".claude", "skills"), { recursive: true });
  mkdirSync(join(home, ".codex", "skills"), { recursive: true });
  symlinkSync(
    join(cache, "example-ops-gitlab", "1.0.8"),
    join(home, ".claude", "skills", "example-ops-gitlab"),
  );
  symlinkSync(
    join(cache, "example-ops-gitlab", "1.0.8"),
    join(home, ".codex", "skills", "example-ops-gitlab"),
  );
  skill(join(home, ".claude", "skills", "code-review"), "代码评审");
  skill(join(home, ".codex", "skills", "release-notes"), "发版说明");

  return home;
}

test("软链进来的技能要认出来——真机上大多数技能就是软链", () => {
  // Dirent.isDirectory() 对软链返回 false。按它筛的话这条会是 0 个。
  const home = fakeHome();
  const found = scanRoot({
    path: join(home, ".claude", "skills"),
    label: "Claude Code",
    writable: false,
    layout: "flat",
  });
  const names = found.map((f) => f.dirName).sort();
  assert.deepEqual(names, ["code-review", "example-ops-gitlab"]);
});

test("同一个缓存目录被两处软链，只算一个", () => {
  const home = fakeHome();
  const ownDir = join(home, ".aiclaw", "skills");
  const found = discoverSkills({ ownDir, home });
  const gitlab = found.filter((f) => f.dir.includes("example-ops-gitlab"));
  assert.equal(gitlab.length, 1, `应当去重成一个，实际 ${gitlab.length} 个`);
  // 留下的是优先级最高的那条路径：Claude 排在 Codex 与 CLI 缓存前面。
  assert.equal(gitlab[0]!.rootLabel, "Claude Code");
});

test("CLI 缓存按 <名字>/<版本> 摆放，取最新的版本", () => {
  const home = fakeHome();
  const found = scanRoot({
    path: join(home, ".example-cli", "skill-cache"),
    label: "内部平台 CLI",
    writable: false,
    layout: "versioned",
  });
  assert.equal(found.length, 1);
  // 1.0.8 > 1.0.4；按字符串比会选错（"1.0.4" > "1.0.8" 为 false，但
  // 10 与 9 这种就会翻车），所以按数字段比。
  assert.ok(found[0]!.dir.endsWith("1.0.8"), found[0]!.dir);
});

test("版本号按数字段比，不是按字符串", () => {
  const home = mkdtempSync(join(tmpdir(), "aiclaw-ver-"));
  const cache = join(home, "cache");
  skill(join(cache, "x", "1.0.9"), "九");
  skill(join(cache, "x", "1.0.10"), "十");
  const found = scanRoot({ path: cache, label: "CLI", writable: false, layout: "versioned" });
  assert.ok(found[0]!.dir.endsWith("1.0.10"), `按字符串比会选成 1.0.9：${found[0]!.dir}`);
});

test("npm 全局包：普通包与 @scope 包都要认", () => {
  const root = mkdtempSync(join(tmpdir(), "aiclaw-npm-"));
  skill(join(root, "opencli"), "opencli");
  skill(join(root, "@jackwener", "opencli"), "scoped-opencli");
  mkdirSync(join(root, "no-skill"), { recursive: true });

  const found = scanRoot({ path: root, label: "npm 全局", writable: false, layout: "npm" });
  const names = found.map((f) => f.dirName).sort();
  assert.deepEqual(names, ["@jackwener/opencli", "opencli"]);
});

test("只有自己的目录可写，别处的都不可写", () => {
  const home = fakeHome();
  const ownDir = join(home, ".aiclaw", "skills");
  skill(join(ownDir, "mine"), "我的技能");

  const found = discoverSkills({ ownDir, home });
  const mine = found.find((f) => f.dirName === "mine");
  assert.ok(mine);
  assert.equal(mine.writable, true);
  for (const other of found.filter((f) => f !== mine)) {
    assert.equal(other.writable, false, `${other.dir} 不该可写`);
  }
});

test("自己的目录排最前，项目级排在用户级之前", () => {
  const home = mkdtempSync(join(tmpdir(), "aiclaw-order-"));
  const workdir = join(home, "proj");
  const roots = skillRoots({ ownDir: join(home, ".aiclaw", "skills"), home, workdir, env: {} });
  const labels = roots.map((r) => r.label);
  assert.equal(labels[0], "内部平台");
  assert.ok(
    labels.indexOf("项目") < labels.indexOf("Claude Code"),
    `项目级应当排在用户级之前：${labels.join(" > ")}`,
  );
});

test("没有工作目录时不去找项目级目录", () => {
  const home = mkdtempSync(join(tmpdir(), "aiclaw-nowork-"));
  const roots = skillRoots({ ownDir: join(home, "own"), home, env: {} });
  assert.ok(!roots.some((r) => r.label === "项目"));
});

test("不存在的根安静跳过，不抛", () => {
  const home = mkdtempSync(join(tmpdir(), "aiclaw-empty-"));
  const found = discoverSkills({ ownDir: join(home, "own"), home, env: {} });
  assert.deepEqual(found, []);
});

test("目录里没有 SKILL.md 就不是技能", () => {
  const root = mkdtempSync(join(tmpdir(), "aiclaw-nomd-"));
  mkdirSync(join(root, "just-a-dir"), { recursive: true });
  writeFileSync(join(root, "README.md"), "不是技能");
  const found = scanRoot({ path: root, label: "x", writable: false, layout: "flat" });
  assert.deepEqual(found, []);
});

test("同名技能只留优先级最高的那个", () => {
  // Claude 下是真目录、Codex 下是指向 CLI 缓存的软链：路径不同、名字相同。
  // 两个一起挂给模型，load_skill 取到哪一个就成了运气。
  const items = [
    { name: "gitlab", from: "Claude Code" },
    { name: "gitlab", from: "Codex" },
    { name: "代码评审", from: "Claude Code" },
  ];
  const kept = dedupeByName(items, (item) => item.name);
  assert.deepEqual(
    kept.map((item) => `${item.name}@${item.from}`),
    ["gitlab@Claude Code", "代码评审@Claude Code"],
  );
});
