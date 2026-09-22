<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { actions, store } from "../store";
import BrandLogo from "./BrandLogo.vue";
import type { SessionSummaryView } from "../../shared/types";

/**
 * 左侧会话栏：新建、按分组归档、切换、删除。
 *
 * 分组是用户自建的文件夹，归属存在宿主这边（session-groups.json），
 * 不进内核的会话存档——内核不该知道界面上有「文件夹」这回事。
 */

/** 设置态下左栏显示的导航。与会话行同一套样式，是同一个列表位置上的两种内容。 */
const NAV = [
  { id: "settings", label: "配置", note: "默认模型、审批档位、数据" },
  { id: "providers", label: "模型服务", note: "端点、Key、模型清单" },
  { id: "search", label: "搜索引擎", note: "Tavily、SerpAPI、阿里云 IQS，启用后模型能联网搜" },
  { id: "plugins", label: "插件", note: "computer use、微信、企业微信，以及从目录装的" },
  { id: "mcp", label: "MCP", note: "第三方 MCP server，stdio 或 HTTP" },
  { id: "skills", label: "技能", note: "本地 SKILL.md，也认 Claude Code / Codex 的" },
] as const;

/**
 * 搜索关键词。防抖 200ms 再打内核——每个按键都查一次的话，
 * 打「销售」两个字期间会发出四五次请求，而结果只有最后一次有用。
 */
const keyword = ref("");
let searchTimer: ReturnType<typeof setTimeout> | undefined;
watch(keyword, (value) => {
  clearTimeout(searchTimer);
  searchTimer = setTimeout(() => void actions.searchSessions(value), 200);
});
const searching = computed(() => keyword.value.trim().length > 0);

const renaming = ref("");
const renameDraft = ref("");
/** 展开哪个会话的「移动到」菜单。空串表示都收着。 */
const moving = ref("");

interface Bucket {
  id: string;
  name: string;
  sessions: SessionSummaryView[];
}

/**
 * 未分组这一档的哨兵 id。
 *
 * 不能用空串：renaming / moving 的「都收着」状态也是空串，撞上之后
 * 「未分组」的标题会被渲染成一个空的重命名输入框。用带前缀的字符串，
 * 与真实分组 id（g_ 前缀）也不会撞。
 */
const UNGROUPED = "__ungrouped__";

const buckets = computed<Bucket[]>(() => {
  const assignments = store.groups.assignments;
  const byGroup = new Map<string, SessionSummaryView[]>();
  for (const session of store.sessions) {
    const groupId = assignments[session.id] ?? UNGROUPED;
    const list = byGroup.get(groupId);
    if (list) list.push(session);
    else byGroup.set(groupId, [session]);
  }
  const result: Bucket[] = [];
  for (const group of store.groups.groups) {
    result.push({ id: group.id, name: group.name, sessions: byGroup.get(group.id) ?? [] });
  }
  // 未分组排最后：用户建了分组就是想先看见分组，没建时它是唯一一组，
  // 排哪儿都一样。
  const loose = byGroup.get(UNGROUPED) ?? [];
  if (loose.length > 0 || result.length === 0) {
    result.push({ id: UNGROUPED, name: "未分组", sessions: loose });
  }
  return result;
});

async function newGroup(): Promise<void> {
  await actions.createGroup(`分组 ${store.groups.groups.length + 1}`);
}

function startRename(groupId: string, current: string): void {
  renaming.value = groupId;
  renameDraft.value = current;
}

async function commitRename(): Promise<void> {
  const id = renaming.value;
  renaming.value = "";
  if (id) await actions.renameGroup(id, renameDraft.value);
}

async function removeGroup(groupId: string, name: string): Promise<void> {
  // 说清楚会话不会跟着没：这是用户在这里最怕的事。
  if (!confirm(`删除分组「${name}」？里面的会话会回到「未分组」，不会被删除。`)) return;
  await actions.deleteGroup(groupId);
}

async function removeSession(session: SessionSummaryView): Promise<void> {
  if (!confirm(`删除会话「${session.title || "未命名"}」？对话记录会从本机移除，不可恢复。`)) {
    return;
  }
  await actions.deleteSession(session.id);
}

async function moveTo(sessionId: string, groupId: string | null): Promise<void> {
  moving.value = "";
  await actions.assignSession(sessionId, groupId);
}

const runtimeLabel = computed(() => {
  switch (store.runtime.state) {
    case "ready":
      return "运行中";
    case "starting":
      return "启动中";
    case "failed":
      return "启动失败";
    default:
      return "未启动";
  }
});

/** 相对时间。列表里绝对时间戳太占地方，也不是用户关心的。 */
function when(iso: string): string {
  const then = new Date(iso).getTime();
  if (!Number.isFinite(then)) return "";
  const minutes = Math.floor((Date.now() - then) / 60000);
  if (minutes < 1) return "刚刚";
  if (minutes < 60) return `${minutes} 分钟前`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} 小时前`;
  const days = Math.floor(hours / 24);
  if (days < 30) return `${days} 天前`;
  return new Date(then).toLocaleDateString("zh-CN");
}
</script>

<template>
  <aside class="sidebar">
    <div class="brand"><BrandLogo /></div>

    <div class="head">
      <template v-if="store.view === 'chat'">
        <button class="new" :disabled="store.runtime.state !== 'ready'" @click="actions.newSession()">
          <span class="plus">+</span> 新对话
        </button>
        <button class="icon" title="新建分组" @click="newGroup()">▤</button>
      </template>
      <button v-else class="new" @click="actions.setView('chat')">
        <span class="plus">‹</span> 返回对话
      </button>
    </div>

    <!-- 搜索只在对话态出现：设置态那个列表是固定的四项，搜它没有意义。 -->
    <div v-if="store.view === 'chat'" class="search">
      <input v-model="keyword" placeholder="搜索会话与内容" />
      <button v-if="keyword" class="icon tiny" title="清空" @click="keyword = ''">×</button>
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
      <p v-if="store.sessionSearch.loading" class="empty">搜索中…</p>
      <p v-else-if="store.sessionSearch.results.length === 0" class="empty">
        没有匹配「{{ keyword }}」的会话。标题和对话正文都找过了。
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
            <span v-if="store.live[session.id]?.busy" class="running" title="正在执行"></span>
            {{ session.title || "未命名会话" }}
          </div>
          <!-- 命中片段是搜索结果里最有用的一行：一列「未命名会话」挑不出来，
               看见命中的那句话就能认出是哪次。 -->
          <div v-if="session.snippet" class="snippet">{{ session.snippet }}</div>
          <div class="meta">
            {{ when(session.updatedAt) }}
            <template v-if="session.turnCount"> · {{ session.turnCount }} 轮</template>
          </div>
        </div>
      </div>
    </div>

    <div v-else class="list">
      <p v-if="store.sessions.length === 0" class="empty">
        {{
          store.runtime.state === "ready"
            ? "还没有会话。"
            : "运行时未启动。启动后这里会列出本机的会话记录。"
        }}
      </p>

      <section
        v-for="bucket in buckets"
        v-show="store.sessions.length > 0"
        :key="bucket.id"
        class="bucket"
      >
        <header class="bucket-head">
          <input
            v-if="renaming === bucket.id && bucket.id !== UNGROUPED"
            v-model="renameDraft"
            class="rename"
            autofocus
            @keydown.enter="commitRename()"
            @keydown.esc="renaming = ''"
            @blur="commitRename()"
          />
          <template v-else>
            <span class="bucket-name" @dblclick="bucket.id !== UNGROUPED && startRename(bucket.id, bucket.name)">
              {{ bucket.name }}
            </span>
            <span class="count">{{ bucket.sessions.length }}</span>
            <button
              v-if="bucket.id !== UNGROUPED"
              class="icon tiny"
              title="删除分组（会话会回到未分组）"
              @click="removeGroup(bucket.id, bucket.name)"
            >
              ×
            </button>
          </template>
        </header>

        <div
          v-for="session in bucket.sessions"
          :key="session.id"
          class="item"
          :class="{ active: session.id === store.sessionId }"
          @click="actions.openSession(session.id)"
        >
          <div class="item-main">
            <div class="title">
              <!-- 后台还在跑的会话点亮一个点：切走之后它没停，用户得看得见它在哪。 -->
              <span v-if="store.live[session.id]?.busy" class="running" title="正在执行"></span>
              {{ session.title || "未命名会话" }}
            </div>
            <div class="meta">
              {{ when(session.updatedAt) }}
              <template v-if="session.turnCount"> · {{ session.turnCount }} 轮</template>
              <template v-if="session.model"> · {{ session.model }}</template>
            </div>
          </div>
          <div class="item-actions" @click.stop>
            <button
              class="icon tiny"
              title="移动到分组"
              @click="moving = moving === session.id ? '' : session.id"
            >
              ⤴
            </button>
            <button class="icon tiny" title="删除会话" @click="removeSession(session)">×</button>
          </div>

          <div v-if="moving === session.id" class="move" @click.stop>
            <button
              v-for="group in store.groups.groups"
              :key="group.id"
              class="move-option"
              @click="moveTo(session.id, group.id)"
            >
              {{ group.name }}
            </button>
            <button class="move-option" @click="moveTo(session.id, null)">未分组</button>
            <p v-if="store.groups.groups.length === 0" class="move-hint">
              还没有分组，先用右上角的 ▤ 建一个。
            </p>
          </div>
        </div>
      </section>
    </div>

    <!-- 底部：运行状态 + 设置。插件与配置都是低频的全局设置，收在这里
         比在顶上常驻一整行合适。 -->
    <footer class="foot">
      <span class="status" :data-state="store.runtime.state">
        <span class="dot" />
        {{ runtimeLabel }}
      </span>
      <button
        class="icon gear"
        :class="{ on: store.view !== 'chat' }"
        title="设置"
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
  margin-top: 12px;
}

.bucket-head {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 4px 9px;
  color: var(--muted);
  font-size: 11px;
  font-weight: 500;
  letter-spacing: 0.04em;
}

.bucket-name {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  cursor: default;
}

.count {
  font-variant-numeric: tabular-nums;
}

.rename {
  flex: 1;
  border: 1px solid var(--accent);
  border-radius: 5px;
  padding: 2px 5px;
  font-size: 11px;
  background: var(--surface);
  color: inherit;
}

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
.running {
  display: inline-block;
  width: 7px;
  height: 7px;
  margin-right: 5px;
  border-radius: 50%;
  background: var(--accent, #3b82f6);
  vertical-align: 1px;
  animation: breathe 1.4s ease-in-out infinite;
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

.item:hover .item-actions,
.item.active .item-actions {
  opacity: 1;
}

.move {
  position: absolute;
  right: 6px;
  top: 30px;
  z-index: 5;
  display: flex;
  flex-direction: column;
  min-width: 128px;
  padding: 4px;
  border: 1px solid var(--rule);
  border-radius: var(--r-md);
  background: var(--surface);
  box-shadow: var(--shadow-3);
}

.move-option {
  padding: 5px 8px;
  border: none;
  border-radius: var(--r-sm);
  background: none;
  text-align: left;
  font-size: 12px;
  color: inherit;
  cursor: pointer;
}

.move-option:hover {
  background: var(--hover);
}

.move-hint {
  margin: 4px 8px;
  color: var(--muted);
  font-size: 11px;
  line-height: 1.5;
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

.status {
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: 11px;
  color: var(--muted);
  font-family: var(--mono);
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
