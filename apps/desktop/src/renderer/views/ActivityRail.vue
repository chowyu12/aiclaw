<script setup lang="ts">
import { computed, nextTick } from "vue";
import { actions, store } from "../store";
import { t } from "../i18n";
import BrandLogo from "./BrandLogo.vue";

defineProps<{ sessionsOpen: boolean }>();
defineEmits<{ sessions: [] }>();

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
  // 窄图标栏显示简短入口，完整版本和阶段保留在悬停说明里。
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

async function browseFiles(): Promise<void> {
  actions.setView("chat");
  await nextTick();
  actions.toggleFileBrowser();
}
</script>

<template>
  <nav class="activity-rail" :aria-label="t('应用导航')">
    <div class="rail-brand"><BrandLogo compact /></div>
    <button :aria-label="t('会话列表')" :title="t('会话列表')" :aria-expanded="sessionsOpen" aria-controls="session-sidebar" @click="$emit('sessions')">
      <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 4h13a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H8l-4 3V6a2 2 0 0 1 2-2Z"/><path d="M8 8h7M8 12h5"/></svg>
    </button>
    <button :aria-label="t('工作区文件')" :title="t('工作区文件')" :aria-pressed="store.view === 'chat' && (store.fileBrowser || !!store.filePreview)" :disabled="!store.sessionId" @click="browseFiles">
      <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M8 3h8l4 4v13H8Z M16 3v5h4M4 7v14h11"/></svg>
    </button>
    <button :aria-label="t('新对话')" :title="t('新对话')" :disabled="store.runtime.state !== 'ready'" @click="actions.newSession()">
      <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 4H5a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2h13a2 2 0 0 0 2-2v-7M10 14l1-4L19 2l3 3-8 8Z"/></svg>
    </button>
    <div class="rail-bottom">
      <button v-if="store.update?.hasUpdate" class="rail-update" :aria-label="updateTitle" :title="updateTitle" :disabled="store.updating || store.updateDownloading" @click="actions.installUpdate()">
        <svg viewBox="0 0 24 24" aria-hidden="true"><path v-if="store.updateReady && !store.updating && !store.updateDownloading" d="M20 7v5h-5M4 17v-5h5M6 7a7 7 0 0 1 12-1l2 3M4 15l2 3a7 7 0 0 0 12-1"/><path v-else d="M12 3v13m-5-5 5 5 5-5M4 17v4h16v-4"/></svg><span v-if="compactUpdateLabel" class="rail-update-label">{{ compactUpdateLabel }}</span>
      </button>
      <button data-view="settings" :aria-label="t('设置')" :title="t('设置')" :aria-pressed="store.view !== 'chat'" @click="actions.setView('settings')">
        <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 6h18M3 12h18M3 18h18"/><circle cx="8" cy="6" r="2"/><circle cx="16" cy="12" r="2"/><circle cx="10" cy="18" r="2"/></svg>
      </button>
    </div>
  </nav>
</template>

<style scoped>
.activity-rail{display:flex;flex-direction:column;align-items:center;gap:12px;flex:0 0 56px;width:56px;padding:17px 8px 10px;border-right:1px solid transparent;background:var(--sidebar);z-index:22}
.rail-brand{margin-bottom:8px}.activity-rail button{display:grid;place-items:center;flex:none;width:36px;height:36px;padding:8px;border:0;border-radius:9px;background:transparent;color:var(--ink-2);cursor:pointer}.activity-rail button:hover,.activity-rail button[aria-expanded=true],.activity-rail button[aria-pressed=true]{background:var(--accent-soft);color:var(--accent)}.activity-rail button:focus-visible{outline:2px solid var(--accent);outline-offset:2px}.activity-rail button:disabled{opacity:.4;cursor:default}.activity-rail svg{width:19px;height:19px;fill:none;stroke:currentColor;stroke-width:1.4;stroke-linecap:round;stroke-linejoin:round}.activity-rail circle{fill:var(--sidebar)}
.rail-bottom{margin-top:auto;display:flex;flex-direction:column;align-items:center;gap:10px}.activity-rail .rail-update{width:44px;padding:6px 0;height:auto;min-height:44px;gap:3px;color:var(--accent)}.activity-rail .rail-update:disabled{opacity:1;cursor:progress}.rail-update-label{font-size:10px;max-width:42px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
</style>
