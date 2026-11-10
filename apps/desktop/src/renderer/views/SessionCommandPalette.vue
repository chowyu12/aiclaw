<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import { actions, store } from "../store";
import { t } from "../i18n";
import { describeError } from "../errors";
import { workspaceBuckets, workspaceName } from "../workspace-buckets";
import SessionRename from "./SessionRename.vue";
import type { SessionSummaryView } from "../../shared/types";

const props = defineProps<{ returnFocus?: HTMLElement; navigation: readonly { id: typeof store.view; label: string; note: string }[] }>();
const emit = defineEmits<{ close: []; newChat: []; newProject: [] }>();
const query = ref(""), active = ref(0), busy = ref(false), error = ref(""), renaming = ref(false);
const panel = ref<HTMLElement>(), input = ref<HTMLInputElement>();
const previousFocus = props.returnFocus ?? document.activeElement as HTMLElement | null;
const current = computed(() => store.sessions.find(session => session.id === store.sessionId));
const labels = computed(() => workspaceBuckets(store.sessions));
function workspaceLabel(session: SessionSummaryView): string {
  const path = session.workdir?.trim();
  if (!path) return t("普通会话");
  const bucket = labels.value.find(item => item.path === path);
  return bucket?.detail ? `${bucket.name} · ${bucket.detail}` : workspaceName(path);
}
const icons: Record<string, string> = {
  new: "M12 4H5a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2h13a2 2 0 0 0 2-2v-7M10 14l1-4L19 2l3 3-8 8Z",
  rename: "m15 4 5 5-12 12-5 1 1-5Z M13 6l5 5",
  archive: "M3 4h18v4H3z M5 8v13h14V8 M10 12h4",
  project: "M3 7h7l2 2h9v11H3z M16 3v6 M13 6h6",
  settings: "M4 6h16 M4 12h16 M4 18h16 M8 3v6 M16 9v6 M10 15v6",
  chat: "M21 11a8 8 0 0 1-8 8H7l-4 3V7a4 4 0 0 1 4-4h10a4 4 0 0 1 4 4z",
};
interface Command { id: string; group: string; label: string; detail: string; icon: string; disabled?: boolean; run: () => void | Promise<void> }
const commands = computed<Command[]>(() => [
  { id: "new-chat", group: t("当前"), label: t("新对话"), detail: t("在当前项目中开始新对话"), icon: "new", disabled: store.runtime.state !== "ready", run: () => emit("newChat") },
  ...(current.value ? [
    { id: "rename", group: t("当前"), label: t("重命名会话"), detail: current.value.title || t("未命名会话"), icon: "rename", run: () => { renaming.value = true; } },
    { id: "archive", group: t("当前"), label: t("归档会话"), detail: t("归档后可在设置中恢复"), icon: "archive", run: () => actions.archiveSession(store.sessionId) },
  ] : []),
  { id: "new-project", group: t("导航"), label: t("新建项目"), detail: t("为项目配置工作区目录"), icon: "project", run: () => emit("newProject") },
  ...props.navigation.map(nav => ({ id: `view-${nav.id}`, group: t("应用"), label: nav.label, detail: nav.note, icon: "settings", run: () => actions.setView(nav.id) })),
]);
const loading = computed(() => !!query.value.trim() && (store.sessionSearch.keyword !== query.value || store.sessionSearch.loading));
const rows = computed<Command[]>(() => {
  const needle = query.value.trim().toLocaleLowerCase();
  const matching = commands.value.filter(command => !needle || `${command.label} ${command.detail}`.toLocaleLowerCase().includes(needle));
  if (!needle || loading.value) return matching;
  return [...matching, ...store.sessionSearch.results.map(session => ({
    id: `session-${session.id}`, group: t("会话"), label: session.title || t("未命名会话"), detail: workspaceLabel(session), icon: "chat",
    run: () => actions.openSession(session.id),
  }))];
});
let timer: ReturnType<typeof setTimeout> | undefined;
watch(query, value => {
  clearTimeout(timer);
  active.value = 0;
  void actions.searchSessions("");
  if (value.trim()) timer = setTimeout(() => void actions.searchSessions(value), 200);
});
watch(() => rows.value.map(row => row.id).join("|"), () => { active.value = 0; });
onMounted(() => input.value?.focus());
onUnmounted(() => {
  clearTimeout(timer);
  void actions.searchSessions("");
  if (previousFocus?.isConnected && (document.activeElement === document.body || panel.value?.contains(document.activeElement))) previousFocus.focus();
});
async function perform(command: Command): Promise<void> {
  if (busy.value || command.disabled) return;
  busy.value = true; error.value = "";
  try { await command.run(); if (!renaming.value) emit("close"); }
  catch (reason) { error.value = describeError(reason); }
  finally { busy.value = false; }
}
function keydown(event: KeyboardEvent): void {
  if (event.isComposing) return;
  if (((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") || event.key === "Escape") { event.preventDefault(); event.stopPropagation(); if (!busy.value) emit("close"); return; }
  if (event.key === "Tab") {
    const elements = [...(panel.value?.querySelectorAll<HTMLElement>('input:not([disabled]),button:not([disabled]):not([tabindex="-1"])') ?? [])];
    const index = elements.indexOf(document.activeElement as HTMLElement);
    if (elements.length && ((event.shiftKey && index <= 0) || (!event.shiftKey && index === elements.length - 1))) {
      event.preventDefault(); elements[event.shiftKey ? elements.length - 1 : 0]?.focus();
    }
    return;
  }
  if (event.target !== input.value || renaming.value || busy.value) return;
  if (event.key === "ArrowDown" || event.key === "ArrowUp") {
    event.preventDefault();
    if (rows.value.length) active.value = (active.value + (event.key === "ArrowDown" ? 1 : -1) + rows.value.length) % rows.value.length;
    void nextTick(() => panel.value?.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: "nearest" }));
  } else if (event.key === "Enter") {
    event.preventDefault(); const command = rows.value[active.value]; if (command) void perform(command);
  }
}
</script>

<template>
  <Teleport to="body">
    <div class="command-backdrop" @click.self="!busy && $emit('close')" @keydown="keydown">
      <section ref="panel" class="session-command-palette" role="dialog" aria-modal="true" :aria-label="t('搜索与快捷操作')">
        <template v-if="renaming && current">
          <div class="rename-heading">{{ t('重命名会话') }}</div>
          <SessionRename :session-id="current.id" :title="current.title || ''" @close="$emit('close')" />
        </template>
        <template v-else>
          <div class="command-search">
            <svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 5 5"/></svg>
            <input ref="input" v-model="query" role="combobox" aria-autocomplete="list" aria-expanded="true" aria-controls="command-results" :aria-activedescendant="rows[active] ? `command-${active}` : undefined" :aria-label="t('搜索会话与内容')" :placeholder="t('搜索会话或操作…')" :disabled="busy" />
            <button class="command-close" :aria-label="t('关闭')" :disabled="busy" @click="$emit('close')"><kbd>Esc</kbd></button>
          </div>
          <div id="command-results" class="command-results" role="listbox" :aria-label="t('搜索与快捷操作')" :aria-busy="loading || busy">
            <template v-for="(command, index) in rows" :key="command.id">
              <div v-if="index === 0 || rows[index - 1]?.group !== command.group" class="command-group">{{ command.group }}</div>
              <button :id="`command-${index}`" class="command-row" :data-command="command.id" role="option" :aria-selected="active === index" :disabled="busy || command.disabled" tabindex="-1" @pointermove="active = index" @click="perform(command)">
                <svg class="command-icon" viewBox="0 0 24 24" aria-hidden="true"><path :d="icons[command.icon]" /></svg>
                <span class="command-copy"><span class="command-label">{{ command.label }}</span><span class="command-detail" :class="{ 'workspace-label': command.id.startsWith('session-') }">{{ command.detail }}</span></span>
                <span class="command-return" aria-hidden="true">↵</span>
              </button>
            </template>
            <p v-if="loading" class="command-empty" role="status">{{ t('搜索中…') }}</p>
            <p v-else-if="!rows.length && !store.sessionSearch.error && !error" class="command-empty" role="status">{{ t('没有匹配的会话或操作') }}</p>
            <p v-if="store.sessionSearch.error || error" class="command-error" role="alert">{{ error || store.sessionSearch.error }}</p>
          </div>
          <footer class="command-footer"><span>↑↓ {{ t('选择') }}</span><span>↵ {{ t('打开') }}</span><span>Esc {{ t('关闭') }}</span></footer>
        </template>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.command-backdrop { position: fixed; inset: 0; z-index: 100; display: flex; justify-content: center; align-items: flex-start; padding: min(16vh, 128px) 24px 24px; background: #0003; backdrop-filter: blur(2px); }
.session-command-palette { width: min(680px, 100%); min-width: 0; overflow: hidden; border: 1px solid var(--rule); border-radius: 20px; background: var(--surface); color: var(--ink); box-shadow: 0 24px 80px #0003; }
.command-search { display: flex; align-items: center; gap: 12px; padding: 17px 20px; border-bottom: 1px solid var(--rule); }
.command-search svg { width: 18px; height: 18px; fill: none; stroke: var(--muted); stroke-width: 1.5; flex-shrink: 0; }
.command-search input { flex: 1; min-width: 0; padding: 0; border: 0; border-radius: 0; background: transparent; font: inherit; font-size: 15px; box-shadow: none; outline: none; }
.command-close { padding: 2px; border: 0; background: none; color: var(--muted); cursor: pointer; }
.command-close kbd { padding: 3px 5px; border: 1px solid var(--rule); border-radius: 5px; font-size: 10px; font-family: inherit; }
.command-results { max-height: min(55vh, 520px); overflow-y: auto; padding: 8px; }
.command-group { padding: 12px 10px 6px; color: var(--muted); font-size: 12px; }
.command-row { display: flex; align-items: center; gap: 12px; width: 100%; min-width: 0; padding: 10px 12px; border: 0; border-radius: 10px; background: transparent; color: var(--ink); text-align: left; font: inherit; cursor: pointer; }
.command-row[aria-selected="true"] { background: var(--hover); }
.command-row:disabled { opacity: .45; cursor: default; }
.command-icon { width: 18px; height: 18px; flex-shrink: 0; fill: none; stroke: var(--muted); stroke-width: 1.5; stroke-linecap: round; stroke-linejoin: round; }
.command-copy { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 3px; }
.command-label { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 14px; }
.command-detail { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--muted); font-size: 12px; }
.command-return { opacity: 0; color: var(--muted); font-size: 13px; }
.command-row[aria-selected="true"] .command-return { opacity: 1; }
.command-empty { margin: 0; padding: 24px 12px; text-align: center; color: var(--muted); font-size: 13px; }
.command-error { padding: 8px 12px; color: var(--danger); font-size: 13px; }
.command-footer { display: flex; gap: 16px; padding: 10px 20px; border-top: 1px solid var(--rule); color: var(--muted); font-size: 11px; }
.rename-heading { padding: 20px 20px 0; font-size: 15px; }
.session-command-palette :deep(.session-rename) { margin: 12px 20px 20px; }
@media (max-height: 600px) { .command-backdrop { padding-top: 24px; } .command-results { max-height: calc(100vh - 160px); } }
</style>
