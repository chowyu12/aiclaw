/**
 * 渲染层的 t()：按配置里的语言翻译（见 shared/i18n.ts）。
 *
 * 语言放在一个响应式的 ref 里，模板里调 t() 的地方切换语言时会跟着重绘；
 * 纯函数模块（时间格式之类）读的是 shared 里那个普通变量，App 根节点按语言
 * 设 key，切换时整棵树重新挂载一次，那些地方也就跟着换了。
 */
import { ref } from "vue";
import { DEFAULT_LOCALE, setCurrentLocale, translate, type Locale, type Params } from "../shared/i18n";

export const locale = ref<Locale>(DEFAULT_LOCALE);
setCurrentLocale(DEFAULT_LOCALE);

export function setLocale(next: Locale): void {
  locale.value = next;
  setCurrentLocale(next);
  document.documentElement.lang = next === "en" ? "en" : "zh-CN";
}

export function t(source: string, params?: Params): string {
  return translate(locale.value, source, params);
}
