/**
 * 英文词典：中文原文 → 英文。按界面分文件，这里合起来。
 *
 * 新加文案时在对应页面的文件里补一条；scripts/test-i18n.ts 会检查有没有漏。
 * 同一句中文在几个文件里各有一条时，后面的覆盖前面的——意思相同的话应当译成一样的。
 */
import { chat } from "./chat.js";
import { common } from "./common.js";
import { mainTools } from "./main-tools.js";
import { plugins } from "./plugins.js";
import { settings } from "./settings.js";
import { shell } from "./shell.js";

export const en: Record<string, string> = {
  ...common,
  ...chat,
  ...settings,
  ...plugins,
  ...shell,
  ...mainTools,
};
