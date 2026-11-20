<script setup lang="ts">
import { computed, ref, watch } from "vue";
import type { HistoryItemView, SessionSummaryView } from "../../shared/types";
import { t } from "../i18n";
import { restoreHistory, type TimelineEntry } from "../live-sessions";
import { store } from "../store";

const props = defineProps<{ sessionId: string }>();
const selected = ref("");
const history = ref<TimelineEntry[]>([]);
const error = ref(false);
const loading = ref(false);
const retry = ref(0);
const agents = computed(() => {
  const results: { session: SessionSummaryView; depth: number }[] = [];
  const seen = new Set([props.sessionId]);
  function add(parent: string, depth: number): void {
    for (const session of store.sessions) {
      if (session.parentId !== parent || seen.has(session.id)) continue;
      seen.add(session.id);
      results.push({ session, depth });
      add(session.id, depth + 1);
    }
  }
  add(props.sessionId, 0);
  return results;
});
const active = computed(() => agents.value.find(({ session }) => session.id === selected.value)?.session);
const live = computed(() => active.value && store.live[active.value.id]);
const timeline = computed(() => {
  const items = new Map(history.value.map(entry => [entry.id, entry]));
  for (const entry of live.value?.timeline ?? []) items.set(entry.id, entry);
  return [...items.values()];
});

function status(session: SessionSummaryView): string {
  const activity = store.live[session.id];
  if (activity?.busy) return t("正在执行");
  switch (activity?.turnStatus ?? session.agentStatus) {
    case "running": return t("正在执行");
    case "preparing": return t("正在准备");
    case "complete":
    case "completed": return t("已完成");
    case "failed":
    case "errored": return t("执行失败");
    case "interrupted": return t("已中断");
    default: return t(session.turnCount ? "已有执行记录" : "等待执行");
  }
}

watch([() => active.value?.id, () => live.value?.busy, retry], async ([id], prior, onCleanup) => {
  if (id !== prior?.[0]) history.value = [];
  error.value = false;
  loading.value = Boolean(id);
  if (!id) return;
  let current = true;
  onCleanup(() => { current = false; });
  try {
    const items = await window.aiclaw.session.history(id) as HistoryItemView[];
    if (current) history.value = restoreHistory(items);
  } catch {
    if (current) error.value = true;
  } finally {
    if (current) loading.value = false;
  }
});
</script>

<template>
  <section v-if="agents.length" class="session-agents">
    <details open>
      <summary>{{ t("子 Agent") }} <small>{{ agents.length }}</small></summary>
      <button
        v-for="{ session, depth } in agents"
        :key="session.id"
        type="button"
        class="agent-row"
        :style="{ paddingInlineStart: `${6 + depth * 12}px` }"
        :aria-pressed="selected === session.id"
        @click="selected = selected === session.id ? '' : session.id"
      >
        <span>{{ session.title.replace(/^↳\s*/, "") || t("未命名会话") }}</span>
        <small>{{ status(session) }}</small>
      </button>
    </details>
    <div v-if="active" class="agent-transcript" :aria-label="t('子 Agent 执行记录')">
      <p v-if="error" role="alert">{{ t("无法加载执行记录") }} <button type="button" @click="retry++">{{ t("重试") }}</button></p>
      <p v-else-if="loading && !timeline.length" role="status">{{ t("正在加载执行记录…") }}</p>
      <p v-else-if="!timeline.length">{{ t("暂无执行记录") }}</p>
      <template v-for="entry in timeline" :key="entry.id">
        <details v-if="entry.kind === 'step'">
          <summary>{{ entry.title }}</summary>
          <p v-if="entry.detail">{{ entry.detail }}</p>
        </details>
        <p v-else v-show="entry.text">{{ entry.text }}</p>
      </template>
    </div>
  </section>
</template>

<style scoped>
.session-agents { padding:12px 0; border-bottom:1px solid var(--rule); }
.session-agents:last-child { border-bottom:0; }
summary { padding:5px 6px; color:var(--muted); cursor:pointer; }
small { color:var(--muted); font-size:11px; }
button { border:0; border-radius:6px; background:transparent; color:inherit; font:inherit; cursor:pointer; }
button:focus-visible { outline:2px solid var(--accent); outline-offset:-2px; }
.agent-row { display:flex; width:100%; align-items:center; justify-content:space-between; gap:8px; padding:7px 6px; text-align:start; }
.agent-row:hover { background:var(--hover); }
.agent-row[aria-pressed="true"] { background:var(--active); }
.agent-row span { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.agent-row small { flex:none; }
.agent-transcript { max-height:320px; overflow:auto; margin-top:6px; padding:4px 8px; border-radius:6px; background:var(--surface-2); }
.agent-transcript p { margin:7px 0; white-space:pre-wrap; overflow-wrap:anywhere; }
.agent-transcript button { color:var(--accent); }
</style>
