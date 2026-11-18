<script setup lang="ts">
import { computed, ref, watch } from "vue";
import type { FileChange } from "@aiclaw/agent-client";
import { actions, store } from "../store";
import { t } from "../i18n";
import { describeRule, formatWhen } from "../../shared/schedule";
import { filename } from "../file-card";
import { fileDiff } from "../file-diff";
import { reportedFilePaths } from "../markdown";
import { groupTurns, turnSources } from "../turns";

const props = defineProps<{ sessionId: string }>();
const emit = defineEmits<{ close: [] }>();
const limit = 3;
const showAll = ref({ tasks: false, outputs: false, sources: false });
const changes = ref<FileChange[]>([]);
const attachments = ref<Awaited<ReturnType<typeof window.aiclaw.files.listAttachments>>>([]);
const failed = ref(false);
const loading = ref(true);
const loaded = ref(false);
const timeline = computed(() => store.sessionId === props.sessionId ? store.timeline : []);
const tasks = computed(() => store.schedules.filter(task =>
  task.mode === "followup" ? task.sessionId === props.sessionId : task.lastSessionId === props.sessionId,
));
const workspace = computed(() => store.sessionInfo?.sessionId === props.sessionId
  ? store.sessionInfo.workspace : store.sessions.find(session => session.id === props.sessionId)?.workdir);
const project = computed(() => store.groups.groups.find(group =>
  group.id === store.groups.assignments[props.sessionId] && Array.isArray(group.directories),
));
// Artifact paths are often workspace-relative while the change log stores absolute paths.
function fileKey(path: string): string {
  const slashes = path.replaceAll("\\", "/");
  const full = /^(?:\/|[a-z]:\/|~\/)/i.test(slashes) || !workspace.value
    ? slashes : `${workspace.value.replaceAll("\\", "/")}/${slashes}`;
  const root = full.match(/^(?:\/\/[^/]+\/[^/]+\/|[a-z]:\/|\/|~\/)/i)?.[0] ?? "";
  const parts: string[] = [];
  for (const part of full.slice(root.length).split("/")) {
    if (!part || part === ".") continue;
    if (part !== "..") parts.push(part);
    else if (parts.length && parts.at(-1) !== "..") parts.pop();
    else if (!root) parts.push(part);
  }
  const key = root + parts.join("/");
  return /^[a-z]:\/|^\/\//i.test(root) ? key.toLowerCase() : key;
}

const recordedChanges = computed(() => {
  const files = new Map<string, FileChange>();
  for (const change of [...changes.value].sort((a, b) => a.createdAt - b.createdAt)) {
    if (change.state !== "applied") continue;
    const key = fileKey(change.path);
    const first = files.get(key);
    files.set(key, first ? { ...change, before: first.before, existed: first.existed } : change);
  }
  return new Map([...files].filter(([, change]) => change.before !== change.after));
});
const changeRows = computed(() => [...recordedChanges.value.values()].sort((a, b) => b.createdAt - a.createdAt).map(change => {
  const lines = fileDiff(change.before.replace(/\n$/, ""), change.after.replace(/\n$/, ""), 3001);
  const added = lines.filter(line => line.kind === "add").length;
  const removed = lines.filter(line => line.kind === "remove").length;
  return { change, added, removed, truncated: lines.length >= 3001 };
}));
const changeTotals = computed(() => changeRows.value.reduce((totals, row) => ({
  added: totals.added + row.added,
  removed: totals.removed + row.removed,
  truncated: totals.truncated || row.truncated,
}), { added: 0, removed: 0, truncated: false }));
const reportedOutputs = ref<string[]>([]);
const candidates = computed(() => {
  const paths = new Map<string, string>();
  for (const entry of [...timeline.value].reverse()) {
    if (entry.kind !== "agent" || entry.streaming) continue;
    for (const path of reportedFilePaths(entry.text)) {
      const key = fileKey(path);
      if (!paths.has(key)) paths.set(key, path);
      if (paths.size === 128) return [...paths.values()];
    }
  }
  return [...paths.values()];
});
watch([() => workspace.value, () => candidates.value.join("\0")], async ([_workspace, paths], _prior, onCleanup) => {
  let current = true;
  onCleanup(() => { current = false; });
  if (!paths) { reportedOutputs.value = []; return; }
  const files = await window.aiclaw.files.existingOutputs(props.sessionId, candidates.value).catch(() => []);
  if (current && store.sessionId === props.sessionId) reportedOutputs.value = files;
}, { immediate: true });
const outputs = computed(() => {
  const paths = new Map<string, string>();
  const add = (path: string) => {
    if (!path || path.split(/[\\/]/).some(part => part === ".archify" || part === ".aiclaw")) return;
    const key = fileKey(path);
    if (!recordedChanges.value.has(key) && !paths.has(key)) paths.set(key, path);
  };
  for (const path of reportedOutputs.value) add(path);
  for (const entry of [...timeline.value].reverse()) {
    if (entry.kind === "step" && entry.state === "done") for (const path of entry.artifacts ?? []) add(path);
  }
  return [...paths.values()];
});
const sources = computed(() => {
  const entries = new Map<string, { key: string; label: string; detail: string; url?: string; threadId?: string; attachmentId?: string }>();
  for (const item of [...attachments.value].reverse()) {
    entries.set(`file:${item.id}`, { key: `file:${item.id}`, label: item.name, detail: t("附件"), attachmentId: item.id });
  }
  for (const turn of groupTurns(timeline.value).reverse()) {
    for (const reference of turn.user?.references ?? []) {
      const key = `thread:${reference.id}`;
      if (!entries.has(key)) entries.set(key, { key, label: reference.title || reference.id, detail: t("会话"), threadId: reference.id });
    }
    for (const source of turnSources(turn)) {
      if (!entries.has(source.url)) entries.set(source.url, {
        key: source.url, label: source.title || new URL(source.url).hostname, detail: new URL(source.url).hostname, url: source.url,
      });
    }
  }
  return [...entries.values()];
});
const retry = ref(0);
watch([() => store.busy, () => store.loadingSession, retry], async ([busy, loadingId], _prior, onCleanup) => {
  if (store.sessionId !== props.sessionId || loadingId === props.sessionId) return;
  if (busy && loaded.value) { loading.value = false; return; }
  let current = true;
  onCleanup(() => { current = false; });
  const sessionId = props.sessionId;
  loading.value = true;
  const [files, uploads] = await Promise.allSettled([
    window.aiclaw.session.changes(sessionId),
    window.aiclaw.files.listAttachments(sessionId),
  ]);
  if (!current || store.sessionId !== sessionId) return;
  if (files.status === "fulfilled") changes.value = files.value;
  if (uploads.status === "fulfilled") attachments.value = uploads.value;
  failed.value = files.status === "rejected" || uploads.status === "rejected";
  loaded.value = true;
  loading.value = false;
}, { immediate: true });

function openSource(source: typeof sources.value[number]): void {
  if (source.threadId) void actions.openSession(source.threadId);
  if (source.attachmentId) void window.aiclaw.files.saveAttachment(props.sessionId, source.attachmentId).catch(() => {
    actions.showError(t("无法保存附件原件"));
  });
}
</script>

<template>
  <aside id="session-details" class="session-details" :aria-label="t('会话详情')" @keydown.esc.stop="emit('close')">
    <header>
      <strong>{{ t("会话详情") }}</strong>
      <button class="close-details" type="button" :aria-label="t('关闭会话详情')" @click="emit('close')">×</button>
    </header>
    <div class="details-list">
      <section v-if="tasks.length">
        <details class="scheduled-tasks" open>
          <summary>{{ t("定时任务") }}</summary>
          <button v-for="task in tasks.slice(0, showAll.tasks ? undefined : limit)" :key="task.id" type="button" class="detail-row scheduled-detail" @click="actions.editScheduleFromDetails(task.id)">
            <span class="detail-name">{{ task.name }}<small>{{ describeRule(task.rule) }}</small></span>
            <span class="detail-meta">
              {{ !task.enabled ? t("已暂停") : task.lastStatus === "running" ? t("正在处理…") : t("已开启") }}
              <small v-if="task.enabled && task.nextRunAt">{{ formatWhen(new Date(task.nextRunAt)) }}</small>
            </span>
          </button>
          <button v-if="tasks.length > limit" class="show-all" :aria-expanded="showAll.tasks" @click="showAll.tasks = !showAll.tasks">{{ showAll.tasks ? t("收起") : t("查看全部") }}</button>
        </details>
      </section>

      <section v-if="project || workspace">
        <h3>{{ t("工作区") }}</h3>
        <div class="workspace-label" :title="workspace">
          <strong>{{ project?.name || filename(workspace || "") }}</strong>
          <small v-if="workspace">{{ workspace }}</small>
        </div>
      </section>

      <section v-if="changeRows.length">
        <details class="file-changes">
          <summary class="change-summary">
            <svg viewBox="0 0 20 20" aria-hidden="true"><path d="M5 3h6l4 4v10H5z M11 3v4h4 M8 10h4 M8 13h4"/></svg>
            <span>{{ t("变更") }} <small>· {{ changeRows.length }}</small></span>
            <small v-if="changeTotals.truncated" class="detail-meta">{{ t("改动较多") }}</small>
            <span v-else class="line-count"><span class="added">+{{ changeTotals.added }}</span> <span class="removed">−{{ changeTotals.removed }}</span></span>
          </summary>
          <button v-for="row in changeRows" :key="row.change.id" type="button" class="detail-row changed-file" :title="row.change.path" @click="actions.previewChange(row.change)">
            <span class="detail-name">{{ filename(row.change.path) }}</span>
            <span v-if="row.truncated" class="detail-meta">{{ t("改动较多") }}</span>
            <span v-else class="line-count"><span class="added">+{{ row.added }}</span> <span class="removed">−{{ row.removed }}</span></span>
          </button>
        </details>
      </section>

      <section v-if="outputs.length">
        <details class="file-outputs" open>
          <summary>{{ t("输出内容") }} <small>{{ outputs.length }}</small></summary>
          <button v-for="path in outputs.slice(0, showAll.outputs ? undefined : limit)" :key="path" type="button" class="detail-row output-file" :title="path" @click="actions.openFile(path, false, props.sessionId)">
            <span class="detail-name">{{ filename(path) }}</span>
          </button>
          <button v-if="outputs.length > limit" class="show-all" :aria-expanded="showAll.outputs" @click="showAll.outputs = !showAll.outputs">{{ showAll.outputs ? t("收起") : t("查看全部") }}</button>
        </details>
      </section>

      <section v-if="sources.length">
        <details class="detail-sources" open>
          <summary>{{ t("来源") }} <small>{{ sources.length }}</small></summary>
          <template v-for="source in sources.slice(0, showAll.sources ? undefined : limit)" :key="source.key">
            <a v-if="source.url" class="detail-row source-link" :href="source.url" target="_blank" rel="noopener noreferrer" :title="source.label">
              <span class="detail-name">{{ source.label }}<small>{{ source.detail }}</small></span>
            </a>
            <button v-else type="button" class="detail-row source-item" :title="source.label" @click="openSource(source)">
              <span class="detail-name">{{ source.label }}<small>{{ source.attachmentId ? t("保存原件") : source.detail }}</small></span>
            </button>
          </template>
          <button v-if="sources.length > limit" class="show-all" :aria-expanded="showAll.sources" @click="showAll.sources = !showAll.sources">{{ showAll.sources ? t("收起") : t("查看全部") }}</button>
        </details>
      </section>

      <p v-if="loading" role="status">{{ t("正在加载会话详情…") }}</p>
      <p v-else-if="failed" role="alert">{{ t("部分会话详情加载失败") }} <button class="show-all" @click="retry++">{{ t("重试") }}</button></p>
      <p v-else-if="!tasks.length && !workspace && !project && !changeRows.length && !outputs.length && !sources.length">{{ t("当前会话暂无详情") }}</p>
    </div>
  </aside>
</template>

<style scoped>
.session-details { display:flex; flex-direction:column; align-self:flex-start; flex:none; width:clamp(265px, 21vw, 320px); min-height:0; max-height:calc(100% - 20px); margin:10px 10px 10px 0; overflow:hidden; background:var(--surface); border:1px solid var(--rule); border-radius:var(--r-md); color:var(--ink); font-size:12px; }
header { display:flex; align-items:center; justify-content:space-between; min-height:43px; flex:none; padding:0 14px; border-bottom:1px solid var(--rule); }
header strong { font-size:13px; }
.details-list { padding:0 12px 12px; overflow-y:auto; }
section { padding:12px 0; border-bottom:1px solid var(--rule); }
section:last-child { border-bottom:0; }
h3, summary { display:flex; align-items:center; gap:6px; margin:0 0 5px; padding:0 6px; font-size:11px; font-weight:600; color:var(--muted); }
summary { min-height:26px; border-radius:6px; cursor:pointer; list-style:none; }
summary::-webkit-details-marker { display:none; }
summary::after { content:""; width:5px; height:5px; margin-left:auto; margin-right:4px; border-right:1px solid currentColor; border-bottom:1px solid currentColor; transform:rotate(-45deg); }
details[open] > summary::after { transform:rotate(45deg); }
summary:hover { background:var(--hover); }
.change-summary { min-height:32px; gap:8px; color:var(--ink); background:var(--surface-2); }
.change-summary::after { display:none; }
.change-summary svg { width:16px; height:16px; flex:none; fill:none; stroke:currentColor; stroke-linecap:round; stroke-linejoin:round; stroke-width:1.3; }
.change-summary .line-count, .change-summary .detail-meta { margin-left:auto; }
small, p, .detail-meta { color:var(--muted); font-size:11px; }
.workspace-label { padding:4px 6px; }
.workspace-label small, .detail-name small { display:block; margin-top:3px; overflow:hidden; text-overflow:ellipsis; }
button, a { color:inherit; font:inherit; background:transparent; border:0; cursor:pointer; }
button:focus-visible, a:focus-visible, summary:focus-visible { outline:2px solid var(--accent); outline-offset:-2px; }
.close-details { width:25px; height:25px; border-radius:6px; color:var(--muted); font-size:18px; }
.detail-row { display:flex; align-items:center; justify-content:space-between; gap:10px; width:100%; min-height:31px; padding:6px; border-radius:6px; text-align:left; text-decoration:none; }
.detail-row:hover, .close-details:hover { background:var(--hover); }
.detail-name { min-width:0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.detail-meta { text-align:right; flex:none; }
.detail-meta small { display:block; margin-top:3px; }
.line-count { flex:none; font:11px var(--mono); }
.added { color:var(--ok); }
.removed { color:var(--danger); }
.show-all { padding:5px 6px; color:var(--accent); }
</style>
