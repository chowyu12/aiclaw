<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import { actions, store } from "../store";
import { draftFor } from "../drafts";
import { renderMarkdown } from "../markdown";
import { t } from "../i18n";
import FileReferences from "./FileReferences.vue";

const pane = ref<HTMLElement>();
const history = ref<HTMLElement>();
const input = ref<HTMLTextAreaElement>();
const id = computed(() => store.sideSessionId);
const record = computed(() => store.live[id.value]);
const draft = computed(() => draftFor(id.value));
const choices = computed(() => store.sessions.filter(session => !session.archivedAt));
const width = ref(420);
try { const saved = Number(localStorage.getItem("aiclaw.side-width")); if (Number.isFinite(saved) && saved >= 280) width.value = Math.min(800, saved); } catch { /* Storage can be unavailable. */ }
let dragging: { x: number; width: number } | undefined;
function resize(value: number): void {
  const available = pane.value?.parentElement?.clientWidth ?? 800;
  width.value = Math.max(280, Math.min(800, value, Math.max(280, available - 320)));
}
function move(event: PointerEvent): void { if (dragging) resize(dragging.width + dragging.x - event.clientX); }
function end(): void {
  if (!dragging) return;
  dragging = undefined;
  window.removeEventListener("pointermove", move); window.removeEventListener("pointerup", end); window.removeEventListener("pointercancel", end);
  try { localStorage.setItem("aiclaw.side-width", String(width.value)); } catch { /* Optional preference. */ }
}
function start(event: PointerEvent): void {
  if (event.button !== 0) return;
  event.preventDefault(); dragging = { x: event.clientX, width: pane.value?.clientWidth ?? width.value };
  window.addEventListener("pointermove", move); window.addEventListener("pointerup", end); window.addEventListener("pointercancel", end);
}
function keyResize(event: KeyboardEvent): void {
  if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
  event.preventDefault(); resize(width.value + (event.key === "ArrowLeft" ? 24 : -24));
  try { localStorage.setItem("aiclaw.side-width", String(width.value)); } catch { /* Optional preference. */ }
}
async function send(): Promise<void> {
  const sessionId = id.value, text = draft.value.text;
  if (!text.trim()) return;
  const target = draftFor(sessionId); target.text = "";
  if (!await actions.send(text, [], [], [], sessionId)) target.text = target.text ? `${text}\n${target.text}` : text;
}
function change(event: Event): void { void actions.openSideSession((event.target as HTMLSelectElement).value); }
function keySend(event: KeyboardEvent): void {
  if (event.key === "Enter" && !event.shiftKey && !event.isComposing) { event.preventDefault(); void send(); }
}
function scrollBottom(): void { if (history.value) history.value.scrollTop = history.value.scrollHeight; }
watch(() => record.value?.timeline.at(-1), async () => {
  const node = history.value, follow = !node || node.scrollHeight - node.scrollTop - node.clientHeight < 100;
  await nextTick(); if (follow) scrollBottom();
}, { deep: true });
watch(id, async () => { await nextTick(); scrollBottom(); input.value?.focus(); });
const observer = new ResizeObserver(() => {
  if ((pane.value?.parentElement?.clientWidth ?? 800) < 600) actions.closeSideSession();
  else resize(width.value);
});
onMounted(() => { if (pane.value?.parentElement) observer.observe(pane.value.parentElement); });
onUnmounted(() => { end(); observer.disconnect(); });
</script>

<template>
  <section ref="pane" class="side-conversation" :style="{ width: `${width}px` }" :aria-label="t('并排会话')">
    <div class="side-resize" role="separator" tabindex="0" aria-orientation="vertical" :aria-label="t('调整会话宽度')" :aria-valuenow="Math.round(width)" :aria-valuemin="280" :aria-valuemax="800" @pointerdown="start" @keydown="keyResize"></div>
    <header>
      <select :value="id" :aria-label="t('并排会话')" @change="change"><option v-for="session in choices" :key="session.id" :value="session.id">{{ session.title || t('未命名会话') }}</option></select>
      <button :title="t('在主区域打开')" @click="actions.openSession(id)">↗</button>
      <button :title="t('聚焦输入框')" @click="input?.focus()">⌨</button>
      <button :aria-label="t('关闭并排会话')" @click="actions.closeSideSession()">×</button>
    </header>
    <div v-if="store.sideLoading" class="side-notice" role="status">{{ t('载入中…') }}</div>
    <div v-if="store.sideError" class="side-notice" role="alert">{{ store.sideError }} <button @click="actions.openSideSession(id)">{{ t('重试') }}</button></div>
    <div ref="history" class="side-history">
      <template v-for="entry in record?.timeline" :key="entry.id">
        <div v-if="entry.kind === 'user'" class="side-user">{{ entry.text }}<span v-if="entry.queued"> · {{ t('已排队') }}</span><img v-for="image in entry.images" :key="image" :src="image" :alt="t('附件')" /></div>
        <FileReferences v-else-if="entry.kind === 'agent'" class="prose" :html="renderMarkdown(entry.text)" :session-id="id" system-only @loaded="scrollBottom" />
        <details v-else class="side-step" :class="entry.state"><summary>{{ entry.title }}</summary><pre>{{ entry.detail }}</pre></details>
      </template>
    </div>
    <footer>
      <textarea ref="input" v-model="draft.text" :placeholder="t('发送消息…')" :disabled="!store.sideSessionReady || store.sideLoading" @keydown="keySend"></textarea>
      <div><span v-if="record?.busy" role="status">{{ t('正在处理…') }}</span><button v-if="record?.busy" @click="actions.interrupt(id)">{{ t('停止') }}</button><button :disabled="!store.sideSessionReady || store.sideLoading || !draft.text.trim()" @click="send">{{ record?.busy ? t('排队发送') : t('发送') }}</button></div>
    </footer>
  </section>
</template>

<style scoped>
.side-conversation { position:relative; display:flex; flex-direction:column; flex:none; min-width:280px; max-width:calc(100% - 320px); min-height:0; background:var(--surface); border-left:1px solid var(--rule); }
.side-resize { position:absolute; left:-4px; top:0; bottom:0; width:8px; cursor:col-resize; z-index:3; touch-action:none; }
.side-resize:hover,.side-resize:focus-visible { background:var(--hover); outline:none; }
header { display:flex; align-items:center; gap:4px; padding:10px 12px; border-bottom:1px solid var(--rule); }
header select { min-width:0; flex:1; color:var(--ink); background:transparent; border:0; font:inherit; }
button { border:0; border-radius:6px; background:var(--surface-2); color:var(--ink); padding:5px 8px; cursor:pointer; } button:disabled { opacity:.45; cursor:default; }
.side-history { flex:1; min-height:0; overflow:auto; padding:16px; overflow-wrap:anywhere; }
.side-user { white-space:pre-wrap; background:var(--surface-2); border-radius:12px; padding:10px 12px; margin:12px 0; }
.side-user img { display:block; max-width:100%; max-height:200px; margin-top:8px; }
.prose { margin:14px 0; font-size:var(--conversation-font-size,14px); }
.side-step { margin:8px 0; font-size:12px; color:var(--muted); } .side-step.failed { color:var(--danger); }
.side-step pre { white-space:pre-wrap; overflow-wrap:anywhere; max-height:260px; overflow:auto; }
.side-notice { padding:10px 16px; font-size:12px; color:var(--muted); }
footer { margin:10px; padding:10px; border:1px solid var(--rule); border-radius:12px; }
textarea { width:100%; min-height:65px; resize:vertical; border:0; background:transparent; color:var(--ink); font:inherit; outline:none; }
footer > div { display:flex; justify-content:flex-end; align-items:center; gap:8px; font-size:12px; color:var(--muted); }
@media(max-width:800px) { .side-conversation { max-width:60%; min-width:260px; } }
</style>
