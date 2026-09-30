#!/usr/bin/env node
/**
 * 列出文件里还没进 t() / tr() 的中文：node scripts/i18n-leftovers.mjs <文件>...
 *
 * 只是辅助翻译时找漏，不是测试：注释、给模型看的文字（工具说明、提示词）本来就该留中文，
 * 这里一并列出来，由人判断。跳过 // 与 * 开头的注释行、<!-- --> 注释，以及已经包在
 * t("…") / tr("…") 里的那一段。
 */
import { readFileSync } from "node:fs";

for (const file of process.argv.slice(2)) {
  const lines = readFileSync(file, "utf8").split("\n");
  let inHtmlComment = false;
  lines.forEach((line, index) => {
    let text = line;
    if (inHtmlComment) {
      if (!text.includes("-->")) return;
      text = text.slice(text.indexOf("-->") + 3);
      inHtmlComment = false;
    }
    text = text.replace(/<!--.*?-->/g, "");
    if (text.includes("<!--")) {
      inHtmlComment = true;
      text = text.slice(0, text.indexOf("<!--"));
    }
    if (/^\s*(\*|\/\/|\/\*)/.test(text)) return;
    text = text.replace(/\/\/.*$/, "");
    text = text
      .replace(/\b(?:t|tr)\(\s*"(?:[^"\\]|\\.)*"/g, "")
      .replace(/\b(?:t|tr)\(\s*'(?:[^'\\]|\\.)*'/g, "")
      .replace(/\b(?:t|tr)\(\s*`(?:[^`\\]|\\.)*`/g, "");
    if (/[一-鿿]/.test(text)) console.log(`${file}:${index + 1}: ${line.trim()}`);
  });
}
