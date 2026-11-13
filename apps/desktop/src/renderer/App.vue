<script setup lang="ts">
import { computed, defineAsyncComponent, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import { actions, store } from "./store";
import ChatView from "./views/ChatView.vue";
import SessionSidebar from "./views/SessionSidebar.vue";
import ActivityRail from "./views/ActivityRail.vue";
import ApprovalDialog from "./views/ApprovalDialog.vue";
import { FONTS, normalizeFont, normalizeFontSize } from "../shared/typography";
import { normalizeTheme } from "../shared/themes";
import { locale, t } from "./i18n";

/** 「配完了」= 选好了默认模型（它所属的模型服务在库里带着端点与 Key）。 */
const SideConversation = defineAsyncComponent(() => import("./views/SideConversation.vue"));
const WorkspaceFiles = defineAsyncComponent(() => import("./views/WorkspaceFiles.vue"));
const FilePreview = defineAsyncComponent(() => import("./views/FilePreview.vue"));
const SettingsShell = defineAsyncComponent(() => import("./views/SettingsShell.vue"));
watch(() => [store.sessionId, store.view], () => { actions.closeFileBrowser(); actions.closeFilePreview(); });
watch(() => store.view === 'chat' && (store.fileBrowser || !!store.filePreview), open => {
  void window.aiclaw.files.previewLayout(open).catch(() => undefined);
});

watch(() => [store.config?.theme, store.config?.font, store.config?.fontSize] as const, ([theme, font, fontSize]) => {
  const root = document.documentElement;
  root.dataset.theme = normalizeTheme(theme);
  root.style.setProperty("--ui-font", FONTS.find(item => item.id === normalizeFont(font))!.family);
  root.style.setProperty("--conversation-font-size", `${normalizeFontSize(fontSize)}px`);
}, { immediate: true });

watch(() => store.fileBrowser || !!store.filePreview, open => { if (open) actions.closeSideSession(); });
function openSide(): void {
  if (store.sideSessionId) { actions.closeSideSession(); return; }
  const target = store.sessions.find(session => session.id !== store.sessionId && !session.archivedAt);
  if (target) void actions.openSideSession(target.id);
}
const configured = computed(() => Boolean(store.config?.providerId && store.config.model));
const sessionsOpen = ref(true);
const isMac = navigator.userAgent.includes("Macintosh");
function toggleSessions(): void {
  if (store.view !== "chat") { actions.setView("chat"); sessionsOpen.value = true; }
  else sessionsOpen.value = !sessionsOpen.value;
  if (sessionsOpen.value) void nextTick(() => document.querySelector<HTMLElement>(".sidebar .head .new")?.focus());
}
function closeSessions(): void {
  if (store.view !== "chat" || !sessionsOpen.value) return;
  sessionsOpen.value = false;
  void nextTick(() => document.querySelector<HTMLButtonElement>('.sidebar-toggle')?.focus());
}

onMounted(async () => {
  await actions.bootstrap();
  // 运行时总是先拉起来：模型服务的清单在内核那边的库里，配置页要靠它才能读写。
  // 开着应用第一件事总是点那个「启动」按钮，那这个按钮就不该存在——
  // 失败时会退回带重试按钮的提示页，不会卡在一个没有出口的界面上。
  await actions.startRuntime();
  if (store.runtime.state !== "ready") return;
  // 没有能用的模型就落到模型服务页，省得用户在对话页对着一个不能用的输入框。
  if (!(await actions.ensureDefaultModel())) {
    actions.setView(store.providers.length === 0 ? "providers" : "settings");
    return;
  }
  await actions.resumeLatest();
});

// 开应用之后查一次，之后每 6 小时一次。
//
// 不做得更勤：这是内部工具，一天发不了几个版本，而一个总在弹的提示条
// 只会被学会无视。启动那次延后几秒，别和拉起运行时抢网络与注意力。
onMounted(() => {
  const timer = setTimeout(() => void actions.checkUpdate(), 8000);
  const interval = setInterval(() => void actions.checkUpdate(), 6 * 60 * 60 * 1000);
  onUnmounted(() => {
    clearTimeout(timer);
    clearInterval(interval);
  });
});

</script>

<template>
  <!-- 按语言设 key：切换语言时整棵树重新挂载一次，纯函数算出来的文案也跟着换。 -->
  <div :key="locale" class="shell" :class="{ mac: isMac, 'sidebar-collapsed': !sessionsOpen && store.view === 'chat' }">
    <div v-if="isMac" class="window-titlebar" aria-hidden="true"></div>
    <SessionSidebar v-show="sessionsOpen || store.view !== 'chat'" id="session-sidebar" @keydown.esc="closeSessions"><ActivityRail v-show="store.view === 'chat'" /></SessionSidebar>

    <main class="pane">
      <header class="workspace-toolbar">
        <button class="sidebar-toggle" :aria-label="t('会话列表')" :title="t('会话列表')" :aria-expanded="sessionsOpen && store.view === 'chat'" aria-controls="session-sidebar" @click="toggleSessions">
          <svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="4" width="18" height="16" rx="3"/><path d="M9 4v16"/></svg>
          <span v-if="Object.values(store.live).some(session => session.unread)" class="rail-unread" role="status" :aria-label="t('未读')" :title="t('未读')"></span>
        </button>
        <span class="workspace-mode">{{ store.view === 'chat' ? t('工作台') : t('设置') }}</span>
        <button v-if="!sessionsOpen && store.view === 'chat'" class="toolbar-settings" :aria-label="t('设置')" :title="t('设置')" @click="actions.setView('settings')"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 6h18M3 12h18M3 18h18 M8 3v6M16 9v6M10 15v6"/></svg></button>
        <button v-if="store.view === 'chat' && store.sessions.some(session => session.id !== store.sessionId && !session.archivedAt)" class="side-toggle" :aria-label="t('并排会话')" :title="t('并排会话')" :aria-pressed="!!store.sideSessionId" @click="openSide"><svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="4" width="18" height="16" rx="3"/><path d="M12 4v16"/></svg></button>
      </header>
      <div v-if="store.error" class="error-bar">
        <span>{{ store.error }}</span>
        <button @click="actions.clearError()">{{ t("知道了") }}</button>
      </div>


      <div class="content">
        <template v-if="store.view === 'chat'">
          <ChatView :configured="configured" />
          <SideConversation v-if="store.sideSessionId" />
          <WorkspaceFiles v-if="store.fileBrowser" />
          <FilePreview v-else-if="store.filePreview && store.filePreview.sessionId === store.sessionId" :preview="store.filePreview" />
        </template>
        <SettingsShell v-else />
      </div>
    </main>

    <ApprovalDialog
      v-if="store.approvals.length > 0"
      :request="store.approvals[0]!"
      @respond="
        (accept: boolean, scope?: 'once' | 'session') =>
          actions.respondApproval(String(store.approvals[0]!.id), accept, scope)
      "
    />
  </div>
</template>

<style scoped>
.shell {
  position: relative;
  display: flex;
  height: 100vh;
  background: var(--sidebar);
}

.shell.mac { padding-top: 0; }
.shell.mac.sidebar-collapsed .workspace-toolbar { padding-left:84px; }
.shell.mac :deep(.sidebar) { padding-top: 36px; }
.window-titlebar { position:absolute; left:0; top:0; width:78px; height:34px; z-index:30; user-select:none; -webkit-app-region:drag; }
.workspace-toolbar { display:flex; align-items:center; gap:10px; height:38px; flex:none; padding:0 12px; color:var(--muted); -webkit-app-region:drag; }
.workspace-toolbar button { position:relative; display:grid; place-items:center; width:28px; height:28px; padding:5px; border:0; border-radius:6px; background:none; color:var(--muted); cursor:pointer; -webkit-app-region:no-drag; }
.workspace-toolbar button:hover { background:var(--hover); color:var(--ink); }
.workspace-toolbar svg { width:16px; height:16px; fill:none; stroke:currentColor; stroke-width:1.4; stroke-linecap:round; }
.workspace-mode { margin:auto; padding:2px 9px; border-radius:var(--r-full); background:var(--surface-2); color:var(--ink-2); font-size:10px; }
.workspace-toolbar::after { content:""; width:28px; }
.workspace-toolbar:has(.side-toggle)::after { display:none; }
.workspace-toolbar:has(.toolbar-settings)::after { display:none; }
.rail-unread { position:absolute; right:1px; top:1px; width:6px; height:6px; border-radius:50%; background:#0878e8; }


.pane {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
  min-height: 0;
  margin: 2px 3px 3px 0;
  border: 1px solid var(--chrome-rule);
  border-radius: 14px;
  background: var(--surface);
  box-shadow: 0 2px 12px rgb(16 22 26 / 4%);
  overflow: hidden;
}

.content {
  display: flex;
  position: relative;
  flex: 1;
  min-width: 0;
  min-height: 0;
}
.content > :first-child { flex: 1; min-width: 0; }
.content > .file-preview, .content > .workspace-files { flex: none; }







.error-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 9px 18px;
  background: var(--danger-soft);
  color: var(--danger);
  font-size: 13px;
}

.error-bar span {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  max-height: 84px;
  overflow-y: auto;
}

.error-bar button {
  flex-shrink: 0;
  border: 1px solid currentColor;
  background: transparent;
  color: inherit;
  border-radius: 5px;
  padding: 3px 10px;
  cursor: pointer;
  font-size: 12px;
}
</style>
