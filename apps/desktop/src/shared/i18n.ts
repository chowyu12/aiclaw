/**
 * 界面文案的国际化：简体中文与英文。
 *
 * **以中文原文为键**：源码里写 `t("新建对话")`，英文放在 locales/en/ 的词典里。
 * 这样源码照旧读得懂（看到的就是界面上的字），也不用给几百条文案起一套键名；
 * 漏翻的回退成中文，不会在界面上露出一串键名。参数用 `{name}` 占位：
 * `t("删除会话「{title}」？", { title })`。
 *
 * 词典按界面分文件（locales/en/<页面>.ts），在 locales/en/index.ts 里合起来。
 * scripts/test-i18n.ts 会扫一遍源码，所有 t("…") 都要有英文，缺了测试就失败。
 *
 * 主进程与渲染层共用这一份：主进程发的系统通知、弹的对话框同样按用户选的语言。
 * 发给模型的文字（工具说明、浏览器快照）不走这里——那是给模型读的，不是界面。
 */
import { en } from "./locales/en/index.js";

export type Locale = "en" | "zh-CN";

export const LOCALES: { id: Locale; label: string }[] = [
  { id: "en", label: "English" },
  { id: "zh-CN", label: "简体中文" },
];

/** 新装的默认英文。 */
export const DEFAULT_LOCALE: Locale = "en";

export function normalizeLocale(value: unknown): Locale {
  return value === "zh-CN" ? "zh-CN" : "en";
}

/**
 * 当前语言。纯函数模块（时间格式、规则描述）不方便层层传参，读这里；
 * 渲染层与主进程在读到配置时设一次。测试里不设，保持中文——现有断言都是中文。
 */
let current: Locale = "zh-CN";

export function setCurrentLocale(locale: Locale): void {
  current = locale;
}

export function currentLocale(): Locale {
  return current;
}

export type Params = Record<string, string | number>;

/** 按指定语言翻译一条文案并填参数。 */
export function translate(locale: Locale, source: string, params?: Params): string {
  const template = locale === "en" ? (en[source] ?? source) : source;
  if (!params) return template;
  return template.replace(/\{(\w+)\}/g, (whole, key: string) => (key in params ? String(params[key]) : whole));
}

/** 按当前语言翻译。 */
export function tr(source: string, params?: Params): string {
  return translate(current, source, params);
}
