import { strict as assert } from "node:assert";
import { test } from "node:test";

import { parseFrontmatter } from "../apps/desktop/src/main/skill-frontmatter.ts";

/**
 * 技能页上显示的名字与说明。
 *
 * 与内核 internal/skills 的 splitFrontmatter 是同一套规则、同一组用例：
 * 两边不一致的表现是「技能页上的说明和模型看到的不一样」，而那种错不会报出来。
 */

const wrap = (body: string): string => `---\n${body}---\n正文\n`;

test("折叠式块标量（>）：Codex 装的技能就这么写", () => {
  const meta = parseFrontmatter(
    wrap(
      [
        "name: storage-analyzer",
        "description: >",
        "  macOS / Windows 只读存储分析助手。扫描整机磁盘占用，找出",
        "  占空间大户，把每一项分成三级并给出可执行处置方案。",
        "",
        '  使用：用户说"存储分析""磁盘满了"时。',
        "",
      ].join("\n"),
    ),
  );
  assert.equal(meta.name, "storage-analyzer");
  assert.notEqual(meta.description, ">", "引子不该成为值");
  assert.match(meta.description, /找出 占空间大户/, "相邻行用空格连起来");
  assert.match(meta.description, /用户说"存储分析"/, "后面几行也要收进来");
});

test("字面式与 chomping 指示符", () => {
  assert.equal(parseFrontmatter(wrap("description: |\n  第一行\n  第二行\n")).description, "第一行\n第二行");
  assert.equal(parseFrontmatter(wrap("description: >-\n  第一行\n  第二行\n")).description, "第一行 第二行");
  assert.equal(parseFrontmatter(wrap("description: |-\n  只有一行\n")).description, "只有一行");
});

test("单行写法照旧；> 只有作为引子时才算块标量", () => {
  const cases: Record<string, string> = {
    "description: 一句话说明": "一句话说明",
    'description: "带引号的说明"': "带引号的说明",
    "description: 用 > 表示大于": "用 > 表示大于",
    "description: >不是块标量的写法": ">不是块标量的写法",
  };
  for (const [line, want] of Object.entries(cases)) {
    assert.equal(parseFrontmatter(wrap(`name: n\n${line}\n`)).description, want, line);
  }
});

test("块标量后面的键照常读到", () => {
  const meta = parseFrontmatter(wrap("description: >\n  说明第一行\n  第二行\nname: 后写的名字\n"));
  assert.equal(meta.description, "说明第一行 第二行");
  assert.equal(meta.name, "后写的名字");
});

test("没有 frontmatter 就是空的", () => {
  assert.deepEqual(parseFrontmatter("# 只有正文\n"), { name: "", description: "" });
});
