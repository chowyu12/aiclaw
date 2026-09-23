import { strict as assert } from "node:assert";
import { test } from "node:test";

import {
  formatExtract,
  formatSnapshot,
  INDEX_SCRIPT,
  MAX_ELEMENTS,
  MAX_EXTRACT_CHARS,
  UNTRUSTED_CLOSE,
  UNTRUSTED_OPEN,
  type RawSnapshot,
} from "../apps/desktop/src/main/browser-snapshot.ts";

/**
 * 浏览器工具给模型看的页面表示。
 *
 * 排版错了不会报错——模型只是点错编号、或者把网页里的一句话当成指令。
 * 所以这里钉住：编号、角色、文字、视口外标记、不可信边界、截断说明。
 */

const page: RawSnapshot = {
  url: "https://example.com/search?q=x",
  title: "搜索结果",
  scrollY: 0,
  viewportHeight: 800,
  pageHeight: 2400,
  total: 3,
  elements: [
    { index: 1, tag: "input", role: "textbox", text: "搜索", value: "x", inView: true },
    { index: 2, tag: "button", role: "button", text: "搜索一下", inView: true },
    { index: 3, tag: "a", role: "link", text: "下一页", href: "/search?q=x&p=2", inView: false },
  ],
};

test("快照列出编号、角色、文字、值与链接目标", () => {
  const text = formatSnapshot(page);
  assert.match(text, /页面：搜索结果/);
  assert.match(text, /\[1\] textbox "搜索" 值=x/);
  assert.match(text, /\[2\] button "搜索一下"/);
  assert.match(text, /\[3\] link "下一页" → \/search\?q=x&p=2 ↓视口外/);
  assert.match(text, /滚动位置：0%（整页约 3\.0 屏）/);
});

test("网页内容包在不可信边界里，编号有效期写明", () => {
  const text = formatSnapshot(page);
  const open = text.indexOf(UNTRUSTED_OPEN);
  const close = text.indexOf(UNTRUSTED_CLOSE);
  assert.ok(open > 0 && close > open, "边界标记要把元素列表夹在中间");
  assert.ok(text.indexOf("[1] textbox") > open && text.indexOf("[1] textbox") < close);
  assert.match(text, /编号只在这次快照里有效/);
});

test("元素超过上限时说明只列了前面的", () => {
  const many: RawSnapshot = { ...page, total: 400, elements: page.elements };
  assert.match(formatSnapshot(many), /400 个，只列前 3 个/);
  assert.ok(INDEX_SCRIPT.includes(`const MAX = ${MAX_ELEMENTS};`), "脚本里的上限要与常量一致");
});

test("没有可交互元素时也要说一句，不能是空白", () => {
  const empty: RawSnapshot = { ...page, elements: [], total: 0, pageHeight: 800 };
  const text = formatSnapshot(empty);
  assert.match(text, /没有找到可交互的元素/);
  assert.doesNotMatch(text, /滚动位置/, "一屏以内不显示滚动位置");
});

test("正文抽取带边界并截断", () => {
  const long = "字".repeat(MAX_EXTRACT_CHARS + 500);
  const text = formatExtract("https://example.com/a", long);
  assert.match(text, /只给前 20000 字符，共 20500/);
  assert.ok(text.includes(UNTRUSTED_OPEN) && text.includes(UNTRUSTED_CLOSE));
  assert.match(formatExtract("https://x", ""), /没有可读的正文/);
});

test("页面脚本只读 DOM：不含 fetch / XMLHttpRequest / require", () => {
  for (const forbidden of ["fetch(", "XMLHttpRequest", "require(", "import("]) {
    assert.ok(!INDEX_SCRIPT.includes(forbidden), `脚本里不该有 ${forbidden}`);
  }
});
