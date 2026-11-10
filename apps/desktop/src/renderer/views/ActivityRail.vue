<script setup lang="ts">
import { computed } from "vue";
import { actions, store } from "../store";
import { t } from "../i18n";


const SHORTCUTS = [
  { view: "providers", label: "模型服务", icon: "M8 3v3m8-3v3M8 18v3m8-3v3M3 8h3m-3 8h3m12-8h3m-3 8h3M6 6h12v12H6ZM9 9h6v6H9Z" },
  { view: "skills", label: "技能", icon: "m12 3 2.5 6.5L21 12l-6.5 2.5L12 21l-2.5-6.5L3 12l6.5-2.5Z" },
  { view: "schedules", label: "定时任务", icon: "M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0ZM12 7v5l3 2" },
] as const;

/** 底部那个更新按钮上写什么；没有新版本时是空串，按钮不显示。 */
const updateLabel = computed(() => {
  const update = store.update;
  if (!update?.hasUpdate) return "";
  if (store.updating || store.updateDownloading) {
    if (store.updatePhase === "checking") return t("检查中…");
    if (store.updatePhase === "verifying") return t("校验中…");
    if (store.updatePhase === "installing") return t("安装中…");
    if (store.updateProgress >= 0) return t("下载 {percent}%", { percent: store.updateProgress });
    return store.updateReceived > 0 ? t("下载 {size} MB", { size: (store.updateReceived / 1048576).toFixed(1) }) : t("连接中…");
  }
  // 就绪后保留图标入口，完整版本和阶段在悬停说明里。
  if (store.updateReady) return t("重启更新");
  return update.canInstall ? t("更新到 {version}", { version: update.latest }) : t("新版 {version}", { version: update.latest });
});

const compactUpdateLabel = computed(() => {
  if ((store.updating || store.updateDownloading) && store.updatePhase === "downloading") {
    if (store.updateProgress >= 0) return `${store.updateProgress}%`;
    if (store.updateReceived > 0) return `${(store.updateReceived / 1048576).toFixed(1)} MB`;
  }
  return store.updating || store.updateDownloading ? updateLabel.value : store.updateReady ? "" : t("更新");
});

/** 悬停时的完整说明：按钮上只放得下几个字。 */
const updateTitle = computed(() => {
  const update = store.update;
  if (!update?.hasUpdate) return "";
  if (store.updating || store.updateDownloading) return t("正在更新到 {version}：{stage}", { version: update.latest, stage: updateLabel.value });
  if (store.updateReady) return t("新版本 {version} 已下载好，点一下替换并重启", { version: update.latest });
  return update.canInstall
    ? t("当前 {current}，新版本 {latest}：{action}", { current: update.current, latest: update.latest, action: update.installLabel ?? "" })
    : t("当前 {current}，新版本 {latest}：打开发布页下载", { current: update.current, latest: update.latest });
});

</script>

<template>
  <nav class="activity-rail" :aria-label="t('应用导航')">
    <button v-for="item in SHORTCUTS" :key="item.view" :data-view="item.view" :aria-label="t(item.label)" :title="t(item.label)" :aria-pressed="store.view === item.view" @click="actions.setView(item.view)">
      <svg viewBox="0 0 24 24" aria-hidden="true"><path :d="item.icon"/></svg><span>{{ t(item.label) }}</span>
    </button>
    <div class="rail-bottom">
      <button v-if="store.update?.hasUpdate" class="rail-update" :aria-label="updateTitle" :title="updateTitle" :disabled="store.updating || store.updateDownloading" @click="actions.installUpdate()">
        <svg viewBox="0 0 24 24" aria-hidden="true"><path v-if="store.updateReady && !store.updating && !store.updateDownloading" d="M20 7v5h-5M4 17v-5h5M6 7a7 7 0 0 1 12-1l2 3M4 15l2 3a7 7 0 0 0 12-1"/><path v-else d="M12 3v13m-5-5 5 5 5-5M4 17v4h16v-4"/></svg><span v-if="compactUpdateLabel" class="rail-update-label">{{ compactUpdateLabel }}</span>
      </button>
      <button data-view="settings" :aria-label="t('设置')" :title="t('设置')" :aria-pressed="store.view !== 'chat' && !SHORTCUTS.some(item => item.view === store.view)" @click="actions.setView('settings')">
        <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 6h18M3 12h18M3 18h18"/><circle cx="8" cy="6" r="2"/><circle cx="16" cy="12" r="2"/><circle cx="10" cy="18" r="2"/></svg>
        <span>{{ t('设置') }}</span>
      </button>
    </div>
  </nav>
</template>

<style scoped>
.activity-rail { display:flex; flex-direction:column; gap:2px; flex:none; width:100%; padding:9px 9px 12px; border-top:1px solid var(--chrome-rule); background:transparent; }
.activity-rail button { display:flex; align-items:center; gap:10px; width:100%; min-height:31px; padding:6px 10px; border:0; border-radius:7px; background:transparent; color:var(--ink-2); cursor:pointer; text-align:left; font-size:12px; }
.activity-rail button:hover,.activity-rail button[aria-pressed=true] { background:var(--hover); color:var(--ink); }
.activity-rail button:focus-visible { outline:2px solid var(--accent); outline-offset:-2px; }
.activity-rail button:disabled { opacity:.5; cursor:default; }
.activity-rail svg { width:15px; height:15px; flex:none; fill:none; stroke:currentColor; stroke-width:1.4; stroke-linecap:round; stroke-linejoin:round; }
.activity-rail circle { fill:var(--sidebar); }
.rail-bottom { display:flex; flex-direction:column; gap:2px; }
.activity-rail .rail-update { color:var(--accent); }
.activity-rail .rail-update:disabled { opacity:1; cursor:progress; }
.rail-update-label { font-size:11px; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
</style>
