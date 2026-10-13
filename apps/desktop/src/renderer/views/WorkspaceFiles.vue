<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { actions, store } from "../store";
import { t } from "../i18n";
import { describeError } from "../errors";
import { fileType } from "../file-card";
import type { WorkspaceFileEntry, WorkspaceFilesView } from "../../shared/types";

const query = ref(""), root = ref(""), error = ref(""), loading = ref(false), truncated = ref(false);
const folders = ref<Record<string, WorkspaceFileEntry[]>>({}), expanded = ref(new Set<string>()), pending = ref(new Set<string>()), results = ref<WorkspaceFileEntry[]>([]);
let generation = 0, searchTimer: ReturnType<typeof setTimeout> | undefined;
const sessionId = store.sessionId, workspace = store.sessionInfo?.workspace ?? "";
async function load(path = "", search = ""): Promise<void> {
  const ticket = generation;
  pending.value.add(path); error.value = "";
  if (!path) loading.value = true;
  try {
    const response = await window.aiclaw.files.browse(sessionId,path,search) as WorkspaceFilesView;
    if (ticket !== generation || store.sessionId !== sessionId || store.sessionInfo?.workspace !== workspace) return;
    root.value = response.root; truncated.value = truncated.value || response.truncated;
    if (search) results.value = response.entries; else folders.value[path] = response.entries;
  } catch (err) { if (ticket === generation) error.value = describeError(err); }
  finally { if (ticket === generation) { pending.value.delete(path); if (!path) loading.value = false; } }
}
function refresh(): void { truncated.value=false;clearTimeout(searchTimer);generation++;folders.value={};expanded.value=new Set();pending.value=new Set();results.value=[];void load("",query.value.trim()); }
watch(query, () => {
  generation++;truncated.value=false;clearTimeout(searchTimer);pending.value=new Set();results.value=[];loading.value=true;
  searchTimer=setTimeout(()=>void load("",query.value.trim()),200);
});
watch(() => store.sessionInfo?.workspace, value => { if (value !== workspace) actions.closeFileBrowser(); });
async function open(entry: WorkspaceFileEntry): Promise<void> {
  if (!entry.directory) { await actions.openFile(entry.absolutePath); return; }
  if (expanded.value.has(entry.path)) expanded.value.delete(entry.path);
  else { expanded.value.add(entry.path);if (!folders.value[entry.path]) await load(entry.path); }
}
const rows = computed(() => {
  if (query.value.trim()) return results.value.map(entry=>({entry,depth:0}));
  const list: {entry:WorkspaceFileEntry;depth:number}[]=[];
  function walk(path:string,depth:number):void {
    for (const entry of folders.value[path] ?? []) {
      list.push({entry,depth});if (entry.directory && expanded.value.has(entry.path)) walk(entry.path,depth+1);
    }
  }
  walk("",0);return list;
});
onMounted(()=>void load());
onUnmounted(()=>{generation++;clearTimeout(searchTimer);});
</script>
<template>
  <aside class="workspace-files" :aria-label="t('工作区文件')" @keydown.esc="actions.closeFileBrowser()">
    <header><div><strong>{{ t('工作区文件') }}</strong><small :title="root || workspace">{{ root || workspace }}</small></div><button :disabled="loading" @click="refresh">{{ t('刷新') }}</button><button :aria-label="t('关闭文件浏览')" @click="actions.closeFileBrowser()">×</button></header>
    <div class="search"><input v-model="query" :placeholder="t('搜索文件名或相对路径…')" :aria-label="t('搜索工作区文件')" /><button v-if="query" @click="query=''">{{ t('清除') }}</button></div>
    <p v-if="query.trim()" class="note">{{ t('搜索文件名和路径，跳过 .git、node_modules、dist、build、release 目录。') }}</p>
    <p v-if="error" class="error" role="alert">{{ error }}</p><p v-if="loading" class="note" role="status">{{ t('正在读取文件列表…') }}</p>
    <p v-if="truncated" class="note">{{ t('结果达到显示范围，请缩小搜索或展开具体目录。') }}</p>
    <div class="tree" :aria-label="t('文件列表')">
      <button v-for="row in rows" :key="row.entry.path" class="file-row" :style="{paddingLeft:(14+row.depth*18)+'px'}" :title="row.entry.path" :aria-expanded="row.entry.directory ? expanded.has(row.entry.path) : undefined" :disabled="pending.has(row.entry.path)" @click="open(row.entry)">
        <span class="file-symbol" aria-hidden="true">{{ row.entry.directory ? (expanded.has(row.entry.path)?'▾':'▸') : fileType(row.entry.name) }}</span><span class="name">{{ row.entry.name }}<small v-if="query.trim()">{{ row.entry.path }}</small></span>
      </button>
      <p v-if="!loading && !rows.length && !error" class="note">{{ query.trim() ? t('没有找到匹配的文件') : t('此目录没有可显示的文件') }}</p>
    </div>
  </aside>
</template>
<style scoped>
.workspace-files{width:640px;max-width:55%;flex:none;min-width:0;height:100%;display:flex;flex-direction:column;background:var(--surface);border-left:1px solid var(--rule)}header{display:flex;align-items:center;gap:8px;padding:14px 16px;border-bottom:1px solid var(--rule)}header>div{flex:1;min-width:0;display:flex;flex-direction:column;gap:6px}strong{font-size:13px}small{font-size:10px;color:var(--muted);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}button{border:0;background:transparent;color:var(--ink-2);font:inherit;font-size:12px;cursor:pointer;padding:5px 8px;border-radius:6px}button:hover{background:var(--accent-soft);color:var(--accent)}button:disabled{opacity:.5;cursor:default}.search{display:flex;gap:6px;padding:12px 16px}.search input{flex:1;min-width:0;border:1px solid var(--rule);border-radius:8px;background:var(--surface-2);color:var(--ink);padding:9px 10px;font:inherit;font-size:12px}.tree{flex:1;min-height:0;overflow:auto;padding:4px 8px 14px}.file-row{width:100%;text-align:left;display:flex;gap:9px;align-items:center;padding-top:8px;padding-bottom:8px}.file-symbol{width:30px;flex:none;text-align:center;font-size:9px;color:var(--accent)}.name{min-width:0;display:flex;flex-direction:column;gap:4px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.note,.error{padding:6px 16px;margin:0;font-size:11px;line-height:1.6;overflow-wrap:anywhere}.note{color:var(--muted)}.error{color:var(--danger)}button:focus-visible,input:focus-visible{outline:2px solid var(--accent);outline-offset:2px}
</style>
