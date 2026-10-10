<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { actions, store } from "../store";
import { locale, t } from "../i18n";
import { describeError } from "../errors";
import { workspaceBuckets, workspaceName } from "../workspace-buckets";
import BrandLogo from "./BrandLogo.vue";
import SessionRename from "./SessionRename.vue";
import type { SessionSummaryView } from "../../shared/types";

/**
 * 左侧会话栏：新建、按工作区展示、切换、删除。
 *
 * 工作区来自会话存档；旧分组数据只用于读取定时任务来源和保存折叠状态。
 */

/** 设置态下左栏显示的导航。与会话行同一套样式，是同一个列表位置上的两种内容。 */
const NAV = computed(
  () =>
    [
      { id: "settings", label: t("配置"), note: t("默认模型、审批档位、数据") },
      { id: "providers", label: t("模型服务"), note: t("端点、Key、模型清单") },
      { id: "search", label: t("搜索引擎"), note: t("Tavily、SerpAPI、阿里云 IQS，启用后模型能联网搜") },
      { id: "plugins", label: t("插件"), note: t("邮件、computer use、微信、企业微信，以及从目录装的") },
      { id: "mcp", label: "MCP", note: t("第三方 MCP server，stdio 或 HTTP") },
      { id: "skills", label: t("技能"), note: t("本地 SKILL.md，也认 Claude Code / Codex 的") },
      { id: "schedules", label: t("定时任务"), note: t("定时执行或在原会话持续跟进") },
      { id: "archived", label: t("已归档"), note: t("收起来的会话，可以恢复或彻底删除") },
      { id: "usage", label: t("用量"), note: t("token、模型调用、工具与技能") },
    ] as const,
);

/**
 * 搜索关键词。防抖 200ms 再打内核——每个按键都查一次的话，
 * 打「销售」两个字期间会发出四五次请求，而结果只有最后一次有用。
 */
/** 这个会话里有没有等你回答的问题。 */
function waiting(sessionId: string): boolean {
  return store.questions.some((question) => question.sessionId === sessionId);
}

const keyword = ref("");
let searchTimer: ReturnType<typeof setTimeout> | undefined;
watch(keyword, (value) => {
  clearTimeout(searchTimer);
  searchTimer = setTimeout(() => void actions.searchSessions(value), 200);
});
const searching = computed(() => keyword.value.trim().length > 0);

const workspaceLabels = computed(() => workspaceBuckets(store.sessions));
function workspaceLabel(session: SessionSummaryView): string {
  const path = session.workdir?.trim();
  if (!path) return t("普通会话");
  const bucket = workspaceLabels.value.find(bucket => bucket.path === path);
  return bucket?.detail ? `${bucket.name} · ${bucket.detail}` : workspaceName(path);
}

const renamingSession = ref("");
watch(() => [store.view, keyword.value], () => { renamingSession.value = ""; });
watch(() => store.sessions.map(session => session.id), ids => {
  if (renamingSession.value && !ids.includes(renamingSession.value)) renamingSession.value = "";
});
interface Bucket {
  id: string;
  name: string;
  path?: string;
  detail?: string;
  sessions: SessionSummaryView[];
}

const pinned = ref<string[]>([]);
try {
  const saved = JSON.parse(localStorage.getItem("aiclaw:pinned-workspaces") ?? "[]");
  if (Array.isArray(saved)) pinned.value = saved.filter(path => typeof path === "string");
} catch { /* Invalid saved preferences do not block the sidebar. */ }
const workspaceMenu = ref<Bucket | null>(null);
const menuPosition = ref({ left: "0px", top: "0px" });
const menuElement = ref<HTMLElement | null>(null);
let menuTrigger: HTMLElement | null = null;
const creatingWorkspace = ref("");
const archivingWorkspace = ref("");
watch(() => [store.view, keyword.value], () => { workspaceMenu.value = null; });

async function openWorkspaceMenu(bucket: Bucket, event: MouseEvent): Promise<void> {
  if (workspaceMenu.value?.id === bucket.id) { closeWorkspaceMenu(); return; }
  menuTrigger = event.currentTarget as HTMLElement;
  const rect = menuTrigger.getBoundingClientRect();
  menuPosition.value = { left: `${Math.max(8, rect.right - 192)}px`, top: `${Math.min(rect.bottom + 4, window.innerHeight - 140)}px` };
  workspaceMenu.value = bucket;
  await nextTick();
  menuElement.value?.querySelector<HTMLButtonElement>("button")?.focus();
}
function closeWorkspaceMenu(): void {
  workspaceMenu.value = null;
  menuTrigger?.focus();
}
function pinWorkspace(bucket: Bucket): void {
  if (!bucket.path) return;
  const updated = pinned.value.includes(bucket.path) ? pinned.value.filter(path => path !== bucket.path) : [...pinned.value, bucket.path];
  try {
    localStorage.setItem("aiclaw:pinned-workspaces", JSON.stringify(updated));
    pinned.value = updated;
    closeWorkspaceMenu();
  } catch (error) { actions.showError(describeError(error)); }
}
async function newWorkspaceChat(bucket: Bucket): Promise<void> {
  if (!bucket.path || creatingWorkspace.value) return;
  creatingWorkspace.value = bucket.id;
  try {
    await actions.newSession(bucket.path);
    await actions.collapseGroup(bucket.id, false);
  } catch (error) { actions.showError(describeError(error)); }
  finally { creatingWorkspace.value = ""; }
}
async function archiveWorkspace(bucket: Bucket): Promise<void> {
  if (archivingWorkspace.value) return;
  closeWorkspaceMenu();
  const count = bucket.sessions.reduce((total, session) => total + 1 + descendants(session.id).length, 0);
  if (!confirm(t("归档工作区「{name}」的 {n} 个会话？正在执行的任务会停止，可在设置中的已归档恢复。", { name: bucket.name, n: count }))) return;
  archivingWorkspace.value = bucket.id;
  try {
    for (const session of bucket.sessions) await actions.archiveSession(session.id);
  } catch (error) { actions.showError(describeError(error)); }
  finally { archivingWorkspace.value = ""; }
}

/** Preserve the existing ordinary-chat collapse key. */
const UNGROUPED = "__ungrouped__";
const SCHEDULED = "__scheduled__";

function sourceLabel(session: SessionSummaryView): string {
  if (isChannelSession(session)) return t("渠道会话");
  return store.groups.assignments[session.id] === SCHEDULED ? t("定时任务") : "";
}

function isChannelSession(session: SessionSummaryView): boolean {
  return session.id.startsWith("c_");
}

/**
 * 子 agent（spawn_agent 开出来的会话）挂在父会话下面，不单独占一行：一次调研开出
 * 三四个子 agent 很常见，平铺的话侧边栏一下子就被它们挤满了。父会话不在列表里
 * （删了、或者搜索没搜到它）时，子会话照常平铺，免得找不到。
 */
const present = computed(() => new Set(store.sessions.map((session) => session.id)));

function isNestedChild(session: SessionSummaryView): boolean {
  return Boolean(session.parentId && present.value.has(session.parentId));
}

/**
 * 子 agent 列表默认折叠：一次调研开出三四个很常见，默认展开会把会话列表撑得很长。
 * 折叠状态与分组存在一起（session-groups.json 的 collapsed，键为 agents:<会话 id>）。
 */
function agentsKey(id: string): string {
  return `agents:${id}`;
}

function agentsCollapsed(id: string): boolean {
  return store.groups.collapsed?.[agentsKey(id)] ?? true;
}

function toggleAgents(id: string): void {
  void actions.collapseGroup(agentsKey(id), !agentsCollapsed(id));
}

/** 折叠着的子 agent 里有没有在跑的、有没有正在看的：开关上亮点、高亮。 */
function agentsBusy(id: string): boolean {
  return descendants(id).some(({ session }) => store.live[session.id]?.busy || waiting(session.id));
}

function agentsHoldCurrent(id: string): boolean {
  return descendants(id).some(({ session }) => session.id === store.sessionId);
}

async function archiveSession(session: SessionSummaryView): Promise<void> {
  await actions.archiveSession(session.id);
}

/** 某个会话下面的子 agent，连同孙子一起按层展开。 */
function descendants(id: string, depth = 1): { session: SessionSummaryView; depth: number }[] {
  const children = store.sessions
    .filter((session) => session.parentId === id)
    .sort((a, b) => a.createdAt.localeCompare(b.createdAt));
  return children.flatMap((child) => [{ session: child, depth }, ...descendants(child.id, depth + 1)]);
}

const buckets = computed<Bucket[]>(() => {
  const roots = store.sessions.filter(session => !isNestedChild(session));
  const result: Bucket[] = workspaceBuckets(roots).sort((a, b) => Number(pinned.value.includes(b.path)) - Number(pinned.value.includes(a.path)));
  const loose = roots.filter(session => !session.workdir?.trim())
    .sort((a, b) => b.updatedAt.localeCompare(a.updatedAt));
  if (loose.length) result.push({ id: UNGROUPED, name: t("普通会话"), sessions: loose });
  return result;
});

/** 工作区默认展开，已保存的折叠状态继续生效。 */
function isCollapsed(bucket: Bucket): boolean {
  return store.groups.collapsed?.[bucket.id] ?? false;
}

/**
 * 折叠就是全收起来。早先折叠时还露出当前正在看的那个会话，而应用启动时恢复的
 * 常常正是一个渠道会话——于是分组看上去怎么也折不起来。当前会话在折叠的分组里时，
 * 改由分组标题高亮来提示。
 */
function visibleSessions(bucket: Bucket): SessionSummaryView[] {
  return isCollapsed(bucket) ? [] : bucket.sessions;
}

/** 当前正在看的会话在这个分组里。 */
function holdsCurrent(bucket: Bucket): boolean {
  return bucket.sessions.some((session) => session.id === store.sessionId);
}

/** 折叠着的分组里，上次看过之后有新消息的会话数。 */
function unread(bucket: Bucket): number {
  const seen = store.groups.seenAt?.[bucket.id] ?? 0;
  return bucket.sessions.filter((session) => Date.parse(session.updatedAt) > seen).length;
}

/** 分组里有会话正在执行或在等回答：折叠时标题上亮一个点。 */
function active(bucket: Bucket): boolean {
  return bucket.sessions.some((session) => store.live[session.id]?.busy || waiting(session.id));
}

function toggleBucket(bucket: Bucket): void {
  void actions.collapseGroup(bucket.id, !isCollapsed(bucket));
}

async function removeSession(session: SessionSummaryView): Promise<void> {
  // 子 agent 跟着父会话一起删：留下一串孤儿子会话，谁也说不清它们是干什么的。
  const children = descendants(session.id).length;
  const title = session.title || t("未命名");
  const question =
    children > 0
      ? t("删除会话「{title}」？连同它开出的 {count} 个子 agent 一起，对话记录会从本机移除，不可恢复。", { title, count: children })
      : t("删除会话「{title}」？对话记录会从本机移除，不可恢复。", { title });
  if (!confirm(question)) {
    return;
  }
  await actions.deleteSession(session.id);
}

/** 底部那个更新按钮上写什么；没有新版本时是空串，按钮不显示。 */
const updateLabel = computed(() => {
  const update = store.update;
  if (!update?.hasUpdate) return "";
  if (store.updating) return t("正在更新…");
  if (store.updateDownloading) {
    return store.updateProgress >= 0 ? t("下载 {percent}%", { percent: store.updateProgress }) : t("下载中…");
  }
  // 侧边栏只有两百来像素宽：按钮文字要短，版本号放在悬停说明里。
  if (store.updateReady) return t("重启更新");
  return update.canInstall ? t("更新到 {version}", { version: update.latest }) : t("新版 {version}", { version: update.latest });
});

/** 悬停时的完整说明：按钮上只放得下几个字。 */
const updateTitle = computed(() => {
  const update = store.update;
  if (!update?.hasUpdate) return "";
  if (store.updateReady) return t("新版本 {version} 已下载好，点一下替换并重启", { version: update.latest });
  if (store.updateDownloading) return t("正在后台下载 {version}", { version: update.latest });
  return update.canInstall
    ? t("当前 {current}，新版本 {latest}：{action}", { current: update.current, latest: update.latest, action: update.installLabel ?? "" })
    : t("当前 {current}，新版本 {latest}：打开发布页下载", { current: update.current, latest: update.latest });
});

const runtimeLabel = computed(() => {
  switch (store.runtime.state) {
    case "ready":
      return t("运行中");
    case "starting":
      return t("启动中");
    case "failed":
      return t("启动失败");
    default:
      return t("未启动");
  }
});

/** 相对时间。列表里绝对时间戳太占地方，也不是用户关心的。 */
function when(iso: string): string {
  const then = new Date(iso).getTime();
  if (!Number.isFinite(then)) return "";
  const minutes = Math.floor((Date.now() - then) / 60000);
  if (minutes < 1) return t("刚刚");
  if (minutes < 60) return t("{n} 分钟前", { n: minutes });
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return t("{n} 小时前", { n: hours });
  const days = Math.floor(hours / 24);
  if (days < 30) return t("{n} 天前", { n: days });
  return new Date(then).toLocaleDateString(locale.value === "en" ? "en-US" : "zh-CN");
}
</script>

<template>
  <aside class="sidebar">
    <div class="brand"><BrandLogo /></div>

    <div class="head">
      <template v-if="store.view === 'chat'">
        <button class="new" :disabled="store.runtime.state !== 'ready'" @click="actions.newSession()">
          <span class="plus">+</span> {{ t("新对话") }}
        </button>
      </template>
      <button v-else class="new" @click="actions.setView('chat')">
        <span class="plus">‹</span> {{ t("返回对话") }}
      </button>
    </div>

    <!-- 搜索只在对话态出现：设置态那个列表是固定的四项，搜它没有意义。 -->
    <div v-if="store.view === 'chat'" class="search">
      <input v-model="keyword" :placeholder="t(`搜索会话与内容`)" />
      <button v-if="keyword" class="icon tiny" :title="t(`清空`)" @click="keyword = ''">×</button>
    </div>

    <!-- 同一个列表位置，两种内容：对话态是会话，设置态是导航。
         导航复用会话行的样式，因为它们在视觉上就该是一回事。 -->
    <div v-if="store.view !== 'chat'" class="list">
      <div
        v-for="nav in NAV"
        :key="nav.id"
        class="item"
        :class="{ active: store.view === nav.id }"
        @click="actions.setView(nav.id)"
      >
        <div class="item-main">
          <div class="title">{{ nav.label }}</div>
          <div class="meta">{{ nav.note }}</div>
        </div>
      </div>
    </div>

    <!-- 搜索态下不分组：分组是「平时怎么归档」，而搜索是「现在要找哪一条」，
         把 3 个命中拆进 5 个分组里反而更难看清。 -->
    <div v-else-if="searching" class="list">
      <p v-if="store.sessionSearch.loading" class="empty">{{ t("搜索中…") }}</p>
      <p v-else-if="store.sessionSearch.results.length === 0" class="empty">
        {{ t("没有匹配「{keyword}」的会话。标题和对话正文都找过了。", { keyword }) }}
      </p>
      <div
        v-for="session in store.sessionSearch.results"
        :key="session.id"
        class="item"
        :class="{ active: session.id === store.sessionId }"
        @click="actions.openSession(session.id)"
      >
        <div class="item-main">
          <div class="title">
            <span v-if="waiting(session.id)" class="asking" :title="t(`模型在等你回答一个问题`)">{{ t("待回答") }}</span>
            <span v-else-if="store.live[session.id]?.busy" class="running" :title="t(`正在执行`)"></span>
            <span v-if="sourceLabel(session)" class="source-label">{{ sourceLabel(session) }}</span>
            {{ session.title || t("未命名会话") }}
          </div>
          <!-- 命中片段是搜索结果里最有用的一行：一列「未命名会话」挑不出来，
               看见命中的那句话就能认出是哪次。 -->
          <div v-if="session.snippet" class="snippet">{{ session.snippet }}</div>
          <div class="meta workspace-label" :title="session.workdir">{{ workspaceLabel(session) }}</div>
          <div class="meta">
            {{ when(session.updatedAt) }}
            <template v-if="session.turnCount"> · {{ session.turnCount === 1 ? t("1 轮") : t("{n} 轮", { n: session.turnCount }) }}</template>
          </div>
        </div>
        <div class="item-actions" @click.stop>
            <button class="icon tiny" :title="t(`重命名会话`)" :aria-label="t(`重命名会话`)" :disabled="!!renamingSession" @click="renamingSession = session.id">
              <svg viewBox="0 0 16 16" width="12" height="12" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"><path d="m10 2 4 4-7.5 7.5-4.5.5.5-4.5Z M8.5 3.5l4 4" /></svg>
            </button>
        </div>
        <SessionRename v-if="renamingSession === session.id" :session-id="session.id" :title="session.title || ''" @close="renamingSession = ''" />
      </div>
    </div>

    <div v-else class="list">
      <p v-if="store.sessions.length === 0" class="empty">
        {{
          store.runtime.state === "ready"
            ? t("还没有会话。")
            : t("运行时未启动。启动后这里会列出本机的会话记录。")
        }}
      </p>

      <section
        v-for="bucket in buckets"
        v-show="store.sessions.length > 0"
        :key="bucket.id"
        class="bucket" :data-bucket-id="bucket.id"
      >
        <header class="bucket-head" :class="{ collapsed: isCollapsed(bucket), current: isCollapsed(bucket) && holdsCurrent(bucket) }">
          <button
            class="fold"
            :title="isCollapsed(bucket) ? t(`展开`) : t(`折叠`)"
            :aria-expanded="!isCollapsed(bucket)"
            @click="toggleBucket(bucket)"
          >
            <svg class="folder-icon" viewBox="0 0 16 16" aria-hidden="true"><path d="M1.5 5V3.5a1 1 0 0 1 1-1h3l1.5 2h6.5a1 1 0 0 1 1 1V7M1.5 6h12a1 1 0 0 1 1 1l-1.5 5a1 1 0 0 1-1 .7h-10a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1Z" /></svg>
            <span class="folder-arrow" aria-hidden="true">{{ isCollapsed(bucket) ? "▸" : "▾" }}</span>
          </button>
            <span
              class="bucket-name"
              :title="bucket.path || (isCollapsed(bucket) && holdsCurrent(bucket) ? t(`正在看的会话在这个分组里`) : undefined)"
              @click="toggleBucket(bucket)"
            >
              {{ bucket.name }}
            </span>
            <span v-if="bucket.path && pinned.includes(bucket.path)" class="workspace-pin" :title="t('已置顶')"><svg viewBox="0 0 16 16" width="12" height="12" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"><path d="m9 2 5 5-2 1-2 3-2-2-4 4 M8 9 5 6l3-2Z" /></svg></span>
            <span class="count">{{ bucket.sessions.length }}</span>
            <div v-if="bucket.path" class="workspace-actions" :class="{ open: workspaceMenu?.id === bucket.id }">
              <button class="icon tiny" :title="t('工作区更多操作')" :aria-label="t('工作区更多操作')" :aria-expanded="workspaceMenu?.id === bucket.id" @click.stop="openWorkspaceMenu(bucket, $event)">⋯</button>
              <button class="icon tiny" :title="t('在此工作区新建会话')" :aria-label="t('在此工作区新建会话')" :disabled="store.runtime.state !== 'ready' || !!creatingWorkspace || !!archivingWorkspace" @click.stop="newWorkspaceChat(bucket)">
                <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"><path d="M8.5 2.5h-5a1 1 0 0 0-1 1v9a1 1 0 0 0 1 1h9a1 1 0 0 0 1-1v-5 M8 8l5-5-1.5-1.5-5 5-.5 2Z" /></svg>
              </button>
            </div>
            <!-- 折叠时：有新消息给个数，有会话在跑 / 在等回答亮个点。 -->
            <span v-if="isCollapsed(bucket) && unread(bucket) > 0" class="unread" :title="t(`上次看过之后有 {n} 个会话有新消息`, { n: unread(bucket) })">
              {{ t("{n} 新", { n: unread(bucket) }) }}
            </span>
            <span v-if="isCollapsed(bucket) && active(bucket)" class="running" :title="t(`有会话正在执行或在等你回答`)"></span>
        </header>
        <div v-if="bucket.detail" class="bucket-detail" :title="bucket.path">{{ bucket.detail }}</div>

        <template v-for="session in visibleSessions(bucket)" :key="session.id">
        <div
          class="item"
          :class="{ active: session.id === store.sessionId }"
          :title="[session.title || t('未命名会话'), when(session.updatedAt), session.turnCount ? t('{n} 轮', { n: session.turnCount }) : '', session.model].filter(Boolean).join(' · ')"
          @click="actions.openSession(session.id)"
        >
          <div class="item-main">
            <div class="title">
              <!-- 后台还在跑的会话点亮一个点：切走之后它没停，用户得看得见它在哪。 -->
              <span v-if="waiting(session.id)" class="asking" :title="t(`模型在等你回答一个问题`)">{{ t("待回答") }}</span>
            <span v-else-if="store.live[session.id]?.busy" class="running" :title="t(`正在执行`)"></span>
              <span v-if="sourceLabel(session)" class="source-label">{{ sourceLabel(session) }}</span>
            {{ session.title || t("未命名会话") }}
            </div>
            <!-- 开出过子 agent 的会话：一个折叠开关，默认收着。 -->
            <button
              v-if="descendants(session.id).length > 0"
              class="agents-toggle"
              :class="{ current: agentsCollapsed(session.id) && agentsHoldCurrent(session.id) }"
              :aria-expanded="!agentsCollapsed(session.id)"
              @click.stop="toggleAgents(session.id)"
            >
              {{ agentsCollapsed(session.id) ? "▸" : "▾" }} {{ t("{n} 个子 agent", { n: descendants(session.id).length }) }}
              <span v-if="agentsCollapsed(session.id) && agentsBusy(session.id)" class="running" :title="t(`有子 agent 正在执行`)"></span>
            </button>
          </div>
          <div class="item-actions" @click.stop>
            <button class="icon tiny" :title="t(`重命名会话`)" :aria-label="t(`重命名会话`)" :disabled="!!renamingSession" @click="renamingSession = session.id">
              <svg viewBox="0 0 16 16" width="12" height="12" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"><path d="m10 2 4 4-7.5 7.5-4.5.5.5-4.5Z M8.5 3.5l4 4" /></svg>
            </button>
            <button class="icon tiny" :title="t(`归档（设置 → 已归档里能恢复）`)" :aria-label="t(`归档`)" @click="archiveSession(session)">
              <svg viewBox="0 0 16 16" width="12" height="12" aria-hidden="true">
                <path fill="currentColor" d="M2 2.5h12a1 1 0 0 1 1 1v2a1 1 0 0 1-1 1H2a1 1 0 0 1-1-1v-2a1 1 0 0 1 1-1Zm0 5h12v5.5a1 1 0 0 1-1 1H3a1 1 0 0 1-1-1V7.5Zm4 1.8v.9h4v-.9H6Z" />
              </svg>
            </button>
            <button class="icon tiny" :title="t(`删除会话`)" @click="removeSession(session)">×</button>
          </div>

          <SessionRename v-if="renamingSession === session.id" :session-id="session.id" :title="session.title || ''" @close="renamingSession = ''" />

        </div>
        <!-- 这个会话开出去的子 agent：缩进挂在下面，跑着的亮点。 -->
        <div
          v-for="child in agentsCollapsed(session.id) ? [] : descendants(session.id)"
          :key="child.session.id"
          class="item child"
          :class="{ active: child.session.id === store.sessionId }"
          :style="{ paddingLeft: `${32 + child.depth * 14}px` }"
          :title="child.session.title"
          @click="actions.openSession(child.session.id)"
        >
          <div class="item-main">
            <div class="title">
              <span v-if="waiting(child.session.id)" class="asking" :title="t(`模型在等你回答一个问题`)">{{ t("待回答") }}</span>
              <span v-else-if="store.live[child.session.id]?.busy" class="running" :title="t(`正在执行`)"></span>
              {{ child.session.title || t("子 agent") }}
            </div>
          </div>
          <div class="item-actions" @click.stop>
            <button class="icon tiny" :title="t(`重命名会话`)" :aria-label="t(`重命名会话`)" :disabled="!!renamingSession" @click="renamingSession = child.session.id">
              <svg viewBox="0 0 16 16" width="12" height="12" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"><path d="m10 2 4 4-7.5 7.5-4.5.5.5-4.5Z M8.5 3.5l4 4" /></svg>
            </button>
            <button class="icon tiny" :title="t(`归档`)" :aria-label="t(`归档`)" @click="archiveSession(child.session)">
              <svg viewBox="0 0 16 16" width="12" height="12" aria-hidden="true">
                <path fill="currentColor" d="M2 2.5h12a1 1 0 0 1 1 1v2a1 1 0 0 1-1 1H2a1 1 0 0 1-1-1v-2a1 1 0 0 1 1-1Zm0 5h12v5.5a1 1 0 0 1-1 1H3a1 1 0 0 1-1-1V7.5Zm4 1.8v.9h4v-.9H6Z" />
              </svg>
            </button>
            <button class="icon tiny" :title="t(`删除会话`)" @click="removeSession(child.session)">×</button>
          </div>
          <SessionRename v-if="renamingSession === child.session.id" :session-id="child.session.id" :title="child.session.title || ''" @close="renamingSession = ''" />
        </div>
        </template>
      </section>
    </div>

    <template v-if="workspaceMenu">
      <div class="workspace-menu-backdrop" @click="closeWorkspaceMenu()" />
      <div ref="menuElement" class="workspace-menu" :style="menuPosition" role="group" :aria-label="t('工作区更多操作')" @keydown.esc.prevent.stop="closeWorkspaceMenu()">
        <button @click="pinWorkspace(workspaceMenu)">{{ workspaceMenu.path && pinned.includes(workspaceMenu.path) ? t('取消置顶') : t('置顶') }}</button>
        <button @click="actions.revealWorkspace(workspaceMenu.path!); closeWorkspaceMenu()">{{ t('在 Finder 中显示') }}</button>
        <button :disabled="!!archivingWorkspace" @click="archiveWorkspace(workspaceMenu)">{{ t('归档工作区会话') }}</button>
      </div>
    </template>

    <!-- 底部：运行状态 + 设置。插件与配置都是低频的全局设置，收在这里
         比在顶上常驻一整行合适。 -->
    <footer class="foot">
      <span class="status" :data-state="store.runtime.state">
        <span class="dot" />
        {{ runtimeLabel }}
        <em v-if="store.appVersion" class="version">v{{ store.appVersion }}</em>
      </span>
      <!-- 更新提示放在版本号旁边：一眼看到「我现在是几、有没有新的」，
           而不是在对话区顶上横一条，挤着正在看的内容。 -->
      <button
        v-if="updateLabel"
        class="update"
        :class="{ ready: store.updateReady }"
        :disabled="store.updating || store.updateDownloading"
        :title="updateTitle"
        @click="actions.installUpdate()"
      >
        {{ updateLabel }}
      </button>
      <button
        class="icon gear"
        :class="{ on: store.view !== 'chat' }"
        :title="t(`设置`)"
        @click="actions.setView('settings')"
      >
        ⚙
      </button>
    </footer>
  </aside>
</template>

<style scoped>
.sidebar {
  display: flex;
  flex-direction: column;
  width: 248px;
  flex: 0 0 248px;
  min-height: 0;
  border-right: 1px solid var(--rule);
  background: var(--sidebar);
}

/* 品牌放侧边栏顶上，顶栏整条去掉了——窗口自己有标题栏，再挂一条就是两层。 */
.brand {
  padding: 13px 12px 8px;
}

.head {
  display: flex;
  gap: 6px;
  padding: 6px 10px 10px;
}

.new {
  flex: 1;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 7px 10px;
  border: 1px solid var(--rule-strong);
  border-radius: var(--r-md);
  background: var(--surface);
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  box-shadow: var(--shadow-1);
  transition: border-color 0.12s, color 0.12s;
}

.new:hover:not(:disabled) {
  border-color: var(--accent);
  color: var(--accent);
}

.new:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.plus {
  font-size: 15px;
  line-height: 1;
}

.icon {
  border: 1px solid var(--rule-strong);
  border-radius: var(--r-md);
  background: var(--surface);
  width: 32px;
  cursor: pointer;
  color: var(--muted);
  box-shadow: var(--shadow-1);
  transition: border-color 0.12s, color 0.12s;
}

.icon:hover {
  color: var(--accent);
  border-color: var(--accent);
}

.icon.tiny {
  width: 22px;
  height: 22px;
  border: none;
  background: none;
  font-size: 13px;
  line-height: 1;
  padding: 0;
}

.search {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 0 10px 8px;
}

.search input {
  flex: 1;
  min-width: 0;
  padding: 5px 9px;
  font-size: 12px;
}

.workspace-actions { display: flex; gap: 2px; opacity: 0; pointer-events: none; }
.bucket-head:hover .workspace-actions, .bucket-head:focus-within .workspace-actions, .workspace-actions.open { opacity: 1; pointer-events: auto; }
.workspace-pin { color: var(--muted); font-size: 13px; }
.workspace-menu-backdrop { position: fixed; inset: 0; z-index: 50; }
.workspace-menu { position: fixed; z-index: 51; width: 192px; padding: 5px; border: 1px solid var(--rule); border-radius: 10px; background: var(--surface); box-shadow: var(--shadow-3); }
.workspace-menu button { display: block; width: 100%; padding: 8px 10px; border: 0; border-radius: 6px; background: transparent; color: var(--ink); font: inherit; font-size: 12px; text-align: left; cursor: pointer; }
.workspace-menu button:hover, .workspace-menu button:focus-visible { background: var(--hover); }
.workspace-menu button:disabled { opacity: .5; cursor: default; }

.bucket-detail { margin: 0 9px 4px 32px; color: var(--muted); font-size: 10px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

.snippet {
  margin-top: 2px;
  color: var(--ink-2);
  font-size: 11.5px;
  line-height: 1.5;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.list {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 8px;
}

.empty {
  margin: 16px 8px;
  color: var(--muted);
  font-size: 12px;
  line-height: 1.6;
}

.bucket + .bucket {
  margin-top: 10px;
}

.bucket-head {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 6px 8px;
  color: var(--ink-2);
  font-size: 13px;
  font-weight: 500;
}

.bucket-name {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  cursor: default;
}

.count {
  color: var(--muted);
  font-size: 11px;
  font-variant-numeric: tabular-nums;
  opacity: 0;
}
.bucket-head:hover .count, .bucket-head:focus-within .count { opacity: 1; }

.bucket .item { min-height: 32px; padding: 6px 8px 6px 32px; align-items: center; }
.bucket .item .title { line-height: 20px; }
.bucket .item.active { background: var(--hover); }
.bucket .item.active .title { font-weight: 400; }
.bucket .item-actions { position: absolute; right: 6px; top: 5px; pointer-events: none; }
.bucket .item:hover, .bucket .item:focus-within { padding-right: 78px; }
.bucket .item:hover .item-actions, .bucket .item:focus-within .item-actions { pointer-events: auto; }
.bucket .item.child { min-height: 28px; }
.bucket .item.child:hover, .bucket .item.child:focus-within { padding-right: 78px; }

.item {
  position: relative;
  display: flex;
  align-items: flex-start;
  gap: 4px;
  padding: 7px 9px;
  border-radius: var(--r-md);
  cursor: pointer;
  transition: background-color 0.1s;
}

.item:hover {
  background: var(--hover);
}

/* 「▸ 2 个子 agent」开关：跟在会话的 meta 下面，小一号，不抢标题的注意力。 */
.agents-toggle {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  margin-top: 3px;
  padding: 1px 7px;
  border: none;
  border-radius: var(--r-full);
  background: var(--surface-2, transparent);
  color: var(--muted);
  font-size: 11px;
  cursor: pointer;
}

.agents-toggle:hover {
  color: var(--ink-2);
}

.agents-toggle.current {
  background: var(--accent-soft);
  color: var(--accent);
}

/* 子 agent：一行、字小一号，缩进由模板按层级给。 */
.item.child {
  padding-top: 4px;
  padding-bottom: 4px;
}

.source-label { margin-right: 5px; padding: 1px 4px; border-radius: 4px; background: var(--surface-2); color: var(--muted); font-size: 10px; font-weight: 400; }

.item.child .title {
  color: var(--ink-2);
  font-size: 12px;
}

.item.active {
  background: var(--active);
}

.item.active .title {
  color: var(--ink);
  font-weight: 500;
}

.item-main {
  flex: 1;
  min-width: 0;
}

.title {
  font-size: 13px;
  color: var(--ink-2);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.meta {
  margin-top: 2px;
  color: var(--muted);
  font-size: 11px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.item-actions {
  display: flex;
  gap: 2px;
  opacity: 0;
}

/* 正在执行的会话：标题前一个呼吸的小点。 */
.fold {
  display: grid;
  place-items: center;
  width: 16px;
  height: 18px;
  padding: 0;
  border: none;
  background: none;
  color: inherit;
  font-size: 11px;
  cursor: pointer;
}
.folder-icon { grid-area: 1 / 1; width: 16px; height: 16px; fill: none; stroke: currentColor; stroke-width: 1.1; stroke-linecap: round; stroke-linejoin: round; }
.folder-arrow { grid-area: 1 / 1; opacity: 0; }
.bucket-head:hover .folder-icon, .fold:focus-visible .folder-icon { opacity: 0; }
.bucket-head:hover .folder-arrow, .fold:focus-visible .folder-arrow { opacity: 1; }

.bucket-name {
  cursor: pointer;
}

/* 折叠着、但正在看的会话在里面：标题高亮，点开就能找到它。 */
.bucket-head.current {
  border-radius: var(--r-md);
  background: var(--accent-soft);
  color: var(--accent);
}

.unread {
  padding: 0 6px;
  border-radius: var(--r-full);
  background: var(--ok-soft);
  color: var(--ok);
  font-size: 10.5px;
  line-height: 16px;
}

.running {
  display: inline-block;
  width: 7px;
  height: 7px;
  margin-right: 5px;
  border-radius: 50%;
  background: var(--brand, var(--accent));
  vertical-align: 1px;
  animation: breathe 1.4s ease-in-out infinite;
}

/* 模型在等你回答问题（ask_user）的会话：比「正在执行」更要紧，替换掉那个点。 */
.asking {
  flex: 0 0 auto;
  margin-right: 5px;
  padding: 0 6px;
  border-radius: var(--r-full);
  background: var(--ok-soft);
  color: var(--ok);
  font-size: 10.5px;
  line-height: 16px;
}

@keyframes breathe {
  0%,
  100% {
    opacity: 1;
  }
  50% {
    opacity: 0.3;
  }
}

.item:focus-within .item-actions,
.item:hover .item-actions {
  opacity: 1;
}

/* ---------- 底部 ---------- */

.foot {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 8px 10px;
  border-top: 1px solid var(--rule);
}

.version {
  margin-left: 6px;
  color: var(--muted);
  font-style: normal;
  font-size: 11px;
}

/* 更新按钮：小，但颜色要够让人注意到；下载好之后换成强调色。 */
.update {
  margin-left: auto;
  margin-right: 6px;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  padding: 2px 8px;
  border: 1px solid var(--accent, #3b82f6);
  border-radius: 999px;
  background: transparent;
  color: var(--accent, #3b82f6);
  font-size: 11px;
  white-space: nowrap;
  cursor: pointer;
}

.update.ready {
  background: var(--brand, var(--accent));
  color: #fff;
}

.update:disabled {
  opacity: 0.7;
  cursor: default;
}

.status {
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: 11px;
  color: var(--muted);
  font-family: var(--mono);
  white-space: nowrap;
  flex-shrink: 0;
}

.dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--muted);
}

.status[data-state="ready"] .dot {
  background: var(--ok);
}

.status[data-state="starting"] .dot {
  background: var(--warn);
}

.status[data-state="failed"] .dot {
  background: var(--danger);
}

.gear {
  width: 28px;
  height: 28px;
  font-size: 14px;
  line-height: 1;
  padding: 0;
}

.gear.on {
  color: var(--accent);
  border-color: var(--accent);
}


</style>
