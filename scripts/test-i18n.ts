import { strict as assert } from "node:assert";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";

import { en } from "../apps/desktop/src/shared/locales/en/index.ts";
import { translate } from "../apps/desktop/src/shared/i18n.ts";

/**
 * 国际化的防漏检查：源码里每一处 t("…") / tr("…") 都要有英文词条。
 *
 * 漏了不会报错——界面上只是冒出一句中文，英文用户看不懂、我们也不会发现。
 * 所以在这里扫一遍源码，缺哪条列出来。只认字面量参数（t("原文") 或 t("原文", { … })），
 * 拼出来的字符串不该传给 t()。
 */

const SRC = join(import.meta.dirname, "..", "apps", "desktop", "src");

function files(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) return name === "locales" ? [] : files(path);
    return /\.(ts|vue|cts)$/.test(name) ? [path] : [];
  });
}

/** 取出 t("…") / tr("…") 的第一个参数（双引号或不带 ${} 的反引号）。 */
function keysIn(source: string): string[] {
  // 注释里的示例不算（i18n.ts 的说明里就写着 t("新建对话")）。
  source = source
    .split("\n")
    .filter((line) => !/^\s*(\*|\/\/|\/\*)/.test(line))
    .join("\n");
  const keys: string[] = [];
  // 三种引号都认：模板属性里常写成 t('…')，漏认的话缺了英文也查不出来。
  const pattern = /\b(?:t|tr)\(\s*(?:"((?:[^"\\]|\\.)*)"|'((?:[^'\\]|\\.)*)'|`((?:[^`\\$]|\\.)*)`)/g;
  for (const match of source.matchAll(pattern)) {
    const raw = match[1] ?? match[2] ?? match[3] ?? "";
    keys.push(raw.replace(/\\(.)/g, "$1"));
  }
  return keys;
}

test("每一处 t(\"…\") 都有英文", () => {
  const missing: string[] = [];
  for (const file of files(SRC)) {
    for (const key of keysIn(readFileSync(file, "utf8"))) {
      if (/[一-鿿]/.test(key) && !(key in en)) missing.push(`${file.slice(SRC.length + 1)}: ${key}`);
    }
  }
  assert.deepEqual(missing, [], `缺英文词条：\n${missing.join("\n")}`);
});

test("英文词条的占位符与原文一致", () => {
  const bad: string[] = [];
  const holes = (text: string) => [...text.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort().join(",");
  for (const [source, target] of Object.entries(en)) {
    if (holes(source) !== holes(target)) bad.push(`${source} → ${target}`);
  }
  assert.deepEqual(bad, []);
});

test("翻译：中文原样、英文查词典、参数填进去、漏翻的回退中文", () => {
  assert.equal(translate("zh-CN", "知道了"), "知道了");
  assert.equal(translate("en", "知道了"), "Dismiss");
  assert.equal(translate("en", "没有这条的原文"), "没有这条的原文");
  assert.equal(translate("zh-CN", "删除「{name}」？", { name: "周报" }), "删除「周报」？");
});
