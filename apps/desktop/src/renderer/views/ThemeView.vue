<script setup lang="ts">
import { ref } from "vue";
import type { AppConfigView } from "../../shared/types";
import { FONTS, FONT_SIZES, normalizeFont, normalizeFontSize } from "../../shared/typography";
import { THEMES, normalizeTheme } from "../../shared/themes";
import { actions, store } from "../store";
import { describeError } from "../errors";
import { t } from "../i18n";

const saving = ref(false);
async function saveAppearance(patch: Partial<Pick<AppConfigView, "theme" | "font" | "fontSize">>): Promise<void> {
  saving.value = true;
  try { await actions.saveConfig(patch); }
  catch (error) { actions.showError(t("保存失败：{error}", { error: describeError(error) })); }
  finally { saving.value = false; }
}
</script>

<template>
  <div class="theme-settings">
    <section v-if="store.config">
      <header>
        <h2>{{ t("主题") }}</h2>
        <p class="sub">{{ t("默认白色。切换后立即生效，并在下次启动时保留。") }}</p>
      </header>
      <div class="theme-options" role="group" :aria-label="t('主题颜色')">
        <button v-for="theme in THEMES" :key="theme.id" type="button" class="theme-option"
          :data-theme-option="theme.id" :aria-pressed="normalizeTheme(store.config.theme) === theme.id"
          :disabled="saving" @click="saveAppearance({ theme: theme.id })">
          <span class="theme-preview" aria-hidden="true"
            :style="{ '--preview-accent': theme.accent, '--preview-surface': theme.surface, '--preview-sidebar': theme.sidebar }">
            <span class="theme-preview-sidebar"></span><span class="theme-preview-line"></span><span class="theme-preview-action"></span>
          </span>
          <span class="theme-name">{{ t(theme.label) }}<span v-if="normalizeTheme(store.config.theme) === theme.id" aria-hidden="true">✓</span></span>
        </button>
      </div>
    </section>

    <section v-if="store.config">
      <header>
        <h2>{{ t("字体与字号") }}</h2>
        <p class="sub">{{ t("未安装所选字体时，将使用系统备用字体。") }}</p>
      </header>
      <div class="font-fields">
        <label>
          <span>{{ t("界面字体") }}</span>
          <select data-font-setting :disabled="saving" :value="normalizeFont(store.config.font)"
            @change="saveAppearance({ font: normalizeFont(($event.target as HTMLSelectElement).value) })">
            <option v-for="font in FONTS" :key="font.id" :value="font.id">{{ t(font.label) }}</option>
          </select>
        </label>
        <label>
          <span>{{ t("对话字号") }}</span>
          <select data-font-size-setting :disabled="saving" :value="normalizeFontSize(store.config.fontSize)"
            @change="saveAppearance({ fontSize: normalizeFontSize(Number(($event.target as HTMLSelectElement).value)) })">
            <option v-for="size in FONT_SIZES" :key="size" :value="size">{{ size }}px{{ size === 14 ? ` · ${t("默认")}` : '' }}</option>
          </select>
        </label>
      </div>
      <p class="sub">{{ t("字号用于对话正文和输入框，导航与按钮保持默认大小。") }}</p>
      <div class="font-preview" :aria-label="t('字体预览')">
        <p>{{ t("这是一段预览文字，用来感受阅读效果。") }}</p>
        <p>AIClaw · Aa Bb Cc · 0123456789</p>
      </div>
    </section>

    <section v-else>
      <h2>{{ t("配置未能加载") }}</h2>
      <button class="ghost" @click="actions.bootstrap()">{{ t("重试") }}</button>
    </section>
  </div>
</template>

<style scoped>
.theme-settings { height: 100%; overflow-y: auto; padding: 28px 28px 64px; }
section { display: flex; flex-direction: column; gap: 14px; max-width: 620px; margin: 0 auto 18px; padding: 20px 22px; border: 1px solid var(--rule); border-radius: var(--r-lg); background: var(--surface); box-shadow: var(--shadow-1); }
header { display: flex; flex-direction: column; gap: 3px; }
h2 { margin: 0; font-size: 14px; font-weight: 650; letter-spacing: .01em; }
.sub { margin: 0; color: var(--muted); font-size: 12px; line-height: 1.6; }
.theme-options { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 10px; }
.theme-option { min-width: 0; padding: 8px; background: var(--surface); border: 1px solid var(--rule); border-radius: var(--r-md); color: var(--ink); text-align: left; cursor: pointer; }
.theme-option:hover:not(:disabled) { background: var(--hover); }
.theme-option[aria-pressed="true"] { border-color: var(--accent); background: var(--accent-soft); }
.theme-option:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
.theme-option:disabled { cursor: wait; }
.theme-preview { display: block; height: 52px; position: relative; overflow: hidden; background: var(--preview-surface); border: 1px solid var(--rule); border-radius: 5px; }
.theme-preview-sidebar { position: absolute; inset: 0 auto 0 0; width: 28%; background: var(--preview-sidebar); }
.theme-preview-line { position: absolute; top: 12px; left: 38%; right: 12%; height: 4px; border-radius: 2px; background: var(--preview-accent); opacity: .65; }
.theme-preview-action { position: absolute; right: 10%; bottom: 9px; width: 18px; height: 12px; border-radius: 3px; background: var(--preview-accent); }
.theme-name { display: flex; justify-content: space-between; gap: 4px; margin-top: 6px; font-size: 12px; }
@media (max-width: 480px) { .theme-options { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
.font-fields { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 14px; }
.font-fields label { display: flex; flex-direction: column; gap: 6px; font-size: 13px; }
.font-preview { padding: 14px; border: 1px solid var(--rule); border-radius: var(--r-sm); font-size: var(--conversation-font-size, 14px); overflow-wrap: anywhere; }
.font-preview p { margin: 0; }
@media (max-width: 480px) { .font-fields { grid-template-columns: 1fr; } }
</style>
