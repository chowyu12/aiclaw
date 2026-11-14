<script setup lang="ts">
import { onMounted, ref } from "vue";
import type { ReleaseNote } from "../../main/release-notes";
import { actions, store } from "../store";
import { renderMarkdown } from "../markdown";
import { t } from "../i18n";

const version = ref("");
const versionError = ref(false);
const releases = ref<ReleaseNote[]>([]);
const loading = ref(true);
const failed = ref(false);
const checking = ref(false);
async function loadReleases(): Promise<void> {
  loading.value = true;
  failed.value = false;
  try { releases.value = await window.aiclaw.update.releases() as ReleaseNote[]; }
  catch { failed.value = true; }
  finally { loading.value = false; }
}
async function checkUpdate(): Promise<void> {
  checking.value = true;
  try { await actions.checkUpdate(); }
  finally { checking.value = false; }
}
onMounted(() => {
  void window.aiclaw.update.version().then(value => { version.value = String(value); }).catch(() => { versionError.value = true; });
  void loadReleases();
});
</script>

<template>
  <div class="about-view">
    <section>
      <header><h2>{{ t("关于") }}</h2></header>
      <div class="version-row">
        <div><span class="label">{{ t("当前版本") }}</span><strong>{{ version || (versionError ? t("版本信息获取失败") : "…") }}</strong></div>
        <button :disabled="checking" @click="checkUpdate">{{ checking ? t("正在检查…") : t("检查更新") }}</button>
      </div>
      <p v-if="store.update?.error" class="muted">{{ t("暂时无法检查更新") }}：{{ store.update.error }}</p>
      <p v-else-if="store.update?.hasUpdate" class="muted">{{ t("发现新版本 {version}", { version: store.update.latest }) }}</p>
      <p v-else-if="store.update" class="muted">{{ t("当前已是最新版本") }}</p>
    </section>
    <section>
      <header><h2>{{ t("更新日志") }}</h2></header>
      <p v-if="loading" class="muted" role="status">{{ t("正在加载更新日志…") }}</p>
      <div v-else-if="failed" class="release-error" role="alert"><span>{{ t("暂时无法加载更新日志") }}</span><button @click="loadReleases">{{ t("重试") }}</button></div>
      <p v-else-if="!releases.length" class="muted">{{ t("暂无已发布的更新日志") }}</p>
      <article v-for="release in releases" v-else :key="release.tag" class="release">
        <div class="release-header"><strong>{{ release.tag }}</strong><time :datetime="release.publishedAt">{{ release.publishedAt.slice(0, 10) }}</time><a :href="release.url" target="_blank" rel="noopener noreferrer">{{ t("查看发布页") }}</a></div>
        <div v-if="release.body" class="release-body prose" v-html="renderMarkdown(release.body)"></div>
        <p v-else class="muted">{{ t("暂无更新说明") }}</p>
      </article>
    </section>
  </div>
</template>

<style scoped>
.about-view { height: 100%; overflow-y: auto; padding: 28px 28px 64px; }
section { display: flex; flex-direction: column; gap: 16px; max-width: 620px; margin: 0 auto 18px; padding: 20px 22px; border: 1px solid var(--rule); border-radius: var(--r-lg); background: var(--surface); box-shadow: var(--shadow-1); }
h2 { margin: 0; font-size: 15px; }
.version-row, .release-header, .release-error { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; }
.version-row { justify-content: space-between; }
.version-row > div { display: flex; flex-direction: column; gap: 5px; }
.label, .muted, time { color: var(--muted); font-size: 12px; }
.muted { margin: 0; }
button { min-height: 34px; padding: 7px 12px; border: 1px solid var(--rule); border-radius: 8px; background: var(--surface); color: var(--ink); cursor: pointer; font: inherit; font-size: 13px; }
button:hover:not(:disabled) { border-color: var(--accent); }
button:disabled { opacity: .55; cursor: wait; }
button:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
.release + .release { border-top: 1px solid var(--rule); padding-top: 16px; }
.release-header strong { font-size: 14px; }
.release-header a { margin-left: auto; color: var(--accent); font-size: 12px; }
.release-body { font-size: 13px; overflow-wrap: anywhere; }
@media (max-width: 500px) { .about-view { padding: 18px 16px 48px; } section { padding: 18px; } }
</style>
