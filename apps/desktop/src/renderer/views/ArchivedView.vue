<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import SessionRename from "./SessionRename.vue";
import { actions, store } from "../store";
import { describeError } from "../errors";
import { t } from "../i18n";
import { formatWhen } from "../../shared/schedule";

/**
 * 设置 → 已归档：从侧边栏收起来的会话。参照 Codex 的「已归档对话」：能恢复（回到侧边栏
 * 原来的分组），也能彻底删除。
 *
 * 子 agent 跟着父会话一起归档、一起恢复，这里只列顶层的那个，后面写上带着几个子 agent，
 * 免得一次归档在这里摊成一长串。
 */

type Row = (typeof store.archived)[number];

const keyword = ref("");
const renamingSession = ref("");
const busy = reactive<Record<string, string>>({});

onMounted(() => void actions.loadArchived());

const ids = computed(() => new Set(store.archived.map((session) => session.id)));

function childCount(id: string): number {
  let count = 0;
  for (const session of store.archived) {
    if (session.parentId === id) count += 1 + childCount(session.id);
  }
  return count;
}

/** 只列顶层：父会话也在归档里的子 agent 跟着父会话走。 */
const rows = computed(() => {
  const needle = keyword.value.trim().toLowerCase();
  return store.archived.filter((session) => {
    if (session.parentId && ids.value.has(session.parentId)) return false;
    return !needle || (session.title || "").toLowerCase().includes(needle);
  });
});

async function restore(session: Row): Promise<void> {
  busy[session.id] = t("恢复中…");
  try {
    await actions.restoreSession(session.id);
  } catch (error) {
    busy[session.id] = describeError(error);
    return;
  }
  delete busy[session.id];
}

async function remove(session: Row): Promise<void> {
  const children = childCount(session.id);
  const title = session.title || t("未命名会话");
  const question =
    children > 0
      ? t("彻底删除「{title}」？连同它开出的 {count} 个子 agent 一起，对话记录会从本机移除，不可恢复。", { title, count: children })
      : t("彻底删除「{title}」？对话记录会从本机移除，不可恢复。", { title });
  if (!confirm(question)) return;
  try {
    await actions.deleteSession(session.id);
    await actions.loadArchived();
  } catch (error) {
    busy[session.id] = describeError(error);
  }
}

function archivedText(session: Row): string {
  return session.archivedAt ? t("{when} 归档", { when: formatWhen(new Date(session.archivedAt)) }) : "";
}
</script>

<template>
  <div class="page">
    <section>
      <header>
        <h2>{{ t("已归档") }}</h2>
        <p class="sub">
          {{ t("在侧边栏会话上点归档，它就收到这里：不占侧边栏，也不出现在搜索里；还在跑的会先停下。") }}
          {{ t("恢复之后回到原来的分组，点开照常接着聊。开出过子 agent 的会话，子 agent 跟着一起归档、一起恢复。") }}
        </p>
      </header>

      <input v-if="store.archived.length > 0" v-model="keyword" class="search" :placeholder="t(`按标题找`)" />

      <p v-if="store.archived.length === 0" class="note">{{ t("没有归档的会话。") }}</p>
      <p v-else-if="rows.length === 0" class="note">{{ t("没有标题里含「{keyword}」的。", { keyword }) }}</p>

      <article v-for="session in rows" :key="session.id" class="row">
        <div class="main">
          <div class="title">{{ session.title || t("未命名会话") }}</div>
          <div class="meta">
            {{ archivedText(session) }}
            <template v-if="session.turnCount"> · {{ session.turnCount === 1 ? t("1 轮") : t("{n} 轮", { n: session.turnCount }) }}</template>
            <template v-if="childCount(session.id)"> · {{ t("带着 {n} 个子 agent", { n: childCount(session.id) }) }}</template>
            <template v-if="session.model"> · {{ session.model }}</template>
          </div>
          <p v-if="busy[session.id]" class="hint">{{ busy[session.id] }}</p>
        </div>
        <SessionRename v-if="renamingSession === session.id" :session-id="session.id" :title="session.title || ''" @close="renamingSession = ''" />
        <div class="actions">
          <button class="ghost small" :disabled="!!renamingSession" @click="renamingSession = session.id">{{ t("重命名会话") }}</button>
          <button class="ghost small" @click="restore(session)">{{ t("恢复") }}</button>
          <button class="ghost small danger" @click="remove(session)">{{ t("删除") }}</button>
        </div>
      </article>
    </section>
  </div>
</template>

<style scoped>
.page {
  height: 100%;
  overflow-y: auto;
  padding: 28px 28px 64px;
}

section {
  display: flex;
  flex-direction: column;
  gap: 12px;
  max-width: 660px;
  margin: 0 auto;
}

header {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

h2 {
  margin: 0;
  font-size: 15px;
  font-weight: 650;
}

.sub,
.note,
.hint {
  margin: 0;
  color: var(--muted);
  font-size: 12px;
  line-height: 1.7;
}

.search {
  max-width: 280px;
}

.row {
  position: relative;
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 11px 14px;
  border: 1px solid var(--rule);
  border-radius: var(--r-lg);
  background: var(--surface);
}

.main {
  flex: 1;
  min-width: 0;
}

.title {
  font-size: 13px;
  font-weight: 500;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.meta {
  margin-top: 2px;
  color: var(--muted);
  font-size: 11.5px;
}

.actions {
  display: flex;
  flex: 0 0 auto;
  gap: 6px;
}

button.small {
  padding: 3px 10px;
  font-size: 11.5px;
}

button.danger:hover {
  color: var(--danger);
  border-color: var(--danger);
}
</style>
