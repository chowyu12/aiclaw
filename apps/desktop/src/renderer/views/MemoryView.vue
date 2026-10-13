<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from "vue";
import { store } from "../store";
import { t } from "../i18n";
import { describeError } from "../errors";
import { MAX_MEMORY_BYTES, memoryBytes, memoryEntries } from "../../shared/memory";
import type { MemoryView, MemoryDocumentView } from "../../shared/types";

const workspaces = computed(() => {
  const seen = new Set<string>();
  return store.sessions.filter(session => {
    if (!session.workdir || seen.has(session.workdir)) return false;
    seen.add(session.workdir); return true;
  });
});
const currentWorkspace = store.sessions.find(session => session.id === store.sessionId)?.workdir;
const selected = ref(workspaces.value.find(session => session.workdir === currentWorkspace)?.id ?? "");
const memory = ref<MemoryView | null>(null), loading = ref(false), error = ref("");
const editing = ref<{ scope: "global" | "workspace"; index: number | null; text: string } | null>(null);
const saving = ref(false);
let request = 0;
function documentFor(scope: "global" | "workspace"): MemoryDocumentView | null { return memory.value?.[scope] ?? null; }
function beginEdit(scope: "global" | "workspace", index: number | null): void {
  const document = documentFor(scope);
  if (!document || document.error || document.exists === null) return;
  error.value = "";
  editing.value = { scope, index, text: index === null ? document.content : memoryEntries(document.content)[index] ?? "" };
}
const draftContent = computed(() => {
  const editor = editing.value;
  if (!editor) return "";
  if (editor.index === null) return editor.text;
  const entries = memoryEntries(documentFor(editor.scope)?.content ?? "");
  entries[editor.index] = editor.text + (editor.text && !editor.text.endsWith("\n") ? "\n" : "");
  return entries.join("");
});
const draftBytes = computed(() => memoryBytes(draftContent.value));
async function save(scope: "global" | "workspace", content: string): Promise<void> {
  const document = documentFor(scope);
  if (!document || document.error || document.exists === null || saving.value) return;
  saving.value = true; error.value = "";
  try {
    const result = await window.aiclaw.memory.save({scope,sessionId:selected.value,content,expectedContent:document.content,expectedExists:document.exists}) as MemoryDocumentView;
    if (memory.value) memory.value[scope] = result;
    editing.value = null;
  } catch (err) { error.value = describeError(err); }
  finally { saving.value = false; }
}
async function removeEntry(scope: "global" | "workspace", index: number): Promise<void> {
  const document = documentFor(scope);
  if (!document || !window.confirm(t("删除这条记忆？保存后会影响以后打开的会话。"))) return;
  const entries = memoryEntries(document.content); entries.splice(index,1);
  await save(scope,entries.join(""));
}
function cancelEdit(): void { editing.value = null; error.value = ""; }
function capacity(content: string): string { return t("已使用 {bytes} 字节 / 32 KB",{bytes:memoryBytes(content)}); }

async function load(): Promise<void> {
  const ticket = ++request;
  loading.value = true; error.value = ""; memory.value = null;
  try {
    const result = await window.aiclaw.memory.read(selected.value) as MemoryView;
    if (ticket === request) memory.value = result;
  } catch (err) { if (ticket === request) error.value = describeError(err); }
  finally { if (ticket === request) loading.value = false; }
}
watch(selected, () => void load(), { immediate: true });
onUnmounted(() => { request++; });
const documents = computed(() => memory.value ? [
  { scope: "global" as const, label: t("全局记忆"), note: t("跨工作区的用户偏好和通用约定"), document: memory.value.global },
  ...(memory.value.workspace ? [{ scope: "workspace" as const, label: t("工作区记忆"), note: t("仅属于所选工作区的约定和经验"), document: memory.value.workspace }] : []),
] : []);
</script>
<template>
  <div class="page memory-page">
    <section>
      <header class="top"><div><h2>{{ t('记忆') }}</h2><p class="sub">{{ t('查看全局偏好和工作区记忆') }}</p></div><button :disabled="loading || saving || !!editing" @click="load">{{ t('刷新') }}</button></header>
      <label class="workspace-picker">{{ t('工作区记忆') }}<select v-model="selected" :disabled="saving || !!editing" :aria-label="t('记忆工作区')"><option value="">{{ t('仅查看全局记忆') }}</option><option v-for="session in workspaces" :key="session.id" :value="session.id">{{ session.workdir }}</option></select></label>
      <p class="hint">{{ t('这里显示已保存的记忆文件，不代表当前会话已加载的内容。记忆会在新建或重新打开会话时加载，每份最多加载 32 KB。') }}</p>
      <p v-if="loading" class="hint" role="status">{{ t('正在读取记忆…') }}</p>
      <p v-if="error" class="error" role="alert">{{ error }}</p>
      <article v-for="item in documents" :key="item.label" class="memory-document">
        <header><div class="document-heading"><h3>{{ item.label }}</h3><button :disabled="saving || !!editing || !!item.document.error" @click="beginEdit(item.scope,null)">{{ t('编辑全文') }}</button></div><p class="hint">{{ item.note }}</p><p class="path">{{ item.document.path }}</p><span class="hint">{{ capacity(item.document.content) }}</span><progress :value="Math.min(memoryBytes(item.document.content),MAX_MEMORY_BYTES)" :max="MAX_MEMORY_BYTES" /><p v-if="memoryBytes(item.document.content)>MAX_MEMORY_BYTES" class="error">{{ t('已超出 32 KB，请精简后保存。') }}</p></header>
        <p v-if="item.document.error" class="error" role="alert">{{ item.document.error }}</p>
        <div v-else-if="editing?.scope===item.scope" class="memory-editor">
          <label>{{ editing.index===null ? t('编辑全文') : t('编辑记忆条目') }}<textarea v-model="editing.text" :disabled="saving" :aria-label="t('记忆编辑内容')" rows="8" /></label>
          <p class="hint" :class="{error:draftBytes>MAX_MEMORY_BYTES}">{{ capacity(draftContent) }}</p>
          <p v-if="draftBytes>MAX_MEMORY_BYTES" class="error">{{ t('记忆不能超过 32 KB，请先精简内容。') }}</p>
          <div class="editor-actions"><button class="primary" :disabled="saving || draftBytes>MAX_MEMORY_BYTES" @click="save(item.scope,draftContent)">{{ saving ? t('保存中…') : t('保存') }}</button><button :disabled="saving" @click="cancelEdit">{{ t('取消') }}</button></div>
        </div>
        <div v-else-if="item.document.content.trim()" class="memory-entries">
          <div v-for="(entry,index) in memoryEntries(item.document.content)" :key="index" class="memory-entry"><template v-if="entry.trim()"><pre>{{ entry }}</pre><div class="entry-actions"><button :disabled="saving || !!editing" @click="beginEdit(item.scope,index)">{{ t('编辑') }}</button><button class="delete" :disabled="saving || !!editing" @click="removeEntry(item.scope,index)">{{ t('删除') }}</button></div></template></div>
        </div>
        <p v-else class="empty">{{ t('暂无记忆') }}<span v-if="item.document.exists === false"> · {{ t('记忆文件尚未创建') }}</span></p>
      </article>
      <p v-if="memory && !memory.workspace" class="hint">{{ t('选择一个工作区，查看该项目保存的记忆。') }}</p>
    </section>
  </div>
</template>
<style scoped>
.page{height:100%;overflow-y:auto;padding:28px 28px 64px}section{max-width:800px;margin:0 auto;display:flex;flex-direction:column;gap:16px}.top{display:flex;align-items:center;justify-content:space-between;gap:16px}h2{font-size:20px;margin:0 0 6px}h3{font-size:14px;margin:0}.sub,.hint{color:var(--muted);font-size:12px;line-height:1.6;margin:0}.workspace-picker{display:flex;align-items:center;gap:12px;font-size:12px}select{flex:1;min-width:0;padding:9px 12px;border:1px solid var(--rule);border-radius:8px;background:var(--surface);color:var(--ink);font:inherit}button{padding:7px 14px;border:1px solid var(--rule);border-radius:7px;background:var(--surface);color:var(--ink);font:inherit;font-size:12px;cursor:pointer}button:disabled{opacity:.5;cursor:default}.memory-document{border:1px solid var(--rule);border-radius:12px;background:var(--surface);overflow:hidden}.memory-document header{padding:16px 18px;display:flex;flex-direction:column;gap:6px;border-bottom:1px solid var(--rule)}.path{color:var(--muted);font:11px var(--mono,monospace);overflow-wrap:anywhere;margin:0}.memory-document pre{font:13px/1.8 var(--mono,monospace);white-space:pre-wrap;overflow-wrap:anywhere;margin:0;padding:0;min-width:0}.empty,.memory-document .error{padding:16px 18px;margin:0;font-size:12px;line-height:1.6}.empty{color:var(--muted)}.error{color:var(--danger);overflow-wrap:anywhere}select:focus-visible,button:focus-visible{outline:2px solid var(--accent);outline-offset:2px}
.document-heading{display:flex;justify-content:space-between;align-items:center;gap:12px}progress{width:100%;height:5px;accent-color:var(--accent)}.memory-entries{max-height:55vh;overflow:auto}.memory-entry{display:flex;align-items:flex-start;gap:14px;padding:14px 18px;border-bottom:1px solid var(--rule)}.memory-entry:empty{display:none}.memory-entry:last-child{border:0}.memory-entry pre{flex:1}.entry-actions{display:flex;gap:5px;flex:none}.entry-actions button{font-size:11px;padding:4px 8px;background:transparent}.entry-actions .delete{color:var(--danger)}.memory-editor{padding:16px 18px;display:flex;flex-direction:column;gap:10px}.memory-editor label{font-size:12px;display:flex;flex-direction:column;gap:8px}.memory-editor textarea{width:100%;resize:vertical;min-height:160px;box-sizing:border-box;border:1px solid var(--rule);border-radius:8px;padding:12px;background:var(--surface-2);color:var(--ink);font:13px/1.7 var(--mono,monospace)}.editor-actions{display:flex;gap:8px}.editor-actions .primary{background:var(--accent-soft);color:var(--accent)}textarea:focus-visible{outline:2px solid var(--accent);outline-offset:2px}
</style>
