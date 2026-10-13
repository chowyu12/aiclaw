<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import { actions, store, type FilePreviewState } from "../store";
import { previewReference } from "../../shared/file-preview";
import { filename, fileType } from "../file-card";
import { highlightCode } from "../code-highlight";
import { renderPreviewMarkdown } from "../markdown";
import { textMatches } from "../file-find";
import { numberedDiff, splitDiff } from "../file-diff";
import { t } from "../i18n";

const props = defineProps<{ preview: FilePreviewState }>();
const mode = ref("source"), layout = ref("split"), copied = ref(false);
const closeButton = ref<HTMLButtonElement>();
const previousFocus = document.activeElement as HTMLElement | null;
const body = ref<HTMLElement>();
const findOpen = ref(false), findQuery = ref(""), findIndex = ref(0), findInput = ref<HTMLInputElement>();
const findCount = ref(0), findTruncated = ref(false), lineNumber = ref(""), activeLine = ref(0), lineError = ref("");
let ranges: Range[] = [];
function highlightFind(): void {
  CSS.highlights.delete("aiclaw-file-find");CSS.highlights.delete("aiclaw-file-active");
  findCount.value = 0; findTruncated.value = false; ranges = [];
  if (!findOpen.value || !findQuery.value || !body.value) return;
  const walker = document.createTreeWalker(body.value,NodeFilter.SHOW_TEXT);
  const parts: {node:Text;start:number;end:number}[]=[];let text="", block:Element|null=null;
  while (walker.nextNode()) {
    const node=walker.currentNode as Text;
    if (mode.value !== "rendered" && !node.parentElement?.closest("code")) continue;
    const nextBlock=node.parentElement?.closest("code,p,li,h1,h2,h3,h4,h5,h6,td,th,blockquote")??null;
    if(block!==nextBlock && text)text+="\n";
    block=nextBlock;
    parts.push({node,start:text.length,end:text.length+node.data.length});text+=node.data;
  }
  const found=textMatches(text,findQuery.value);findTruncated.value=found.truncated;
  for (const match of found.matches) {
    const first=parts.find(part=>part.start<=match.start && part.end>match.start),last=parts.find(part=>part.end>=match.start+match.length);
    if (!first || !last) continue;
    const range=document.createRange();range.setStart(first.node,match.start-first.start);range.setEnd(last.node,match.start+match.length-last.start);ranges.push(range);
  }
  findCount.value=ranges.length;findIndex.value=Math.min(findIndex.value,Math.max(0,ranges.length-1));
  CSS.highlights.set("aiclaw-file-find",new Highlight(...ranges));
  if (ranges[findIndex.value]) CSS.highlights.set("aiclaw-file-active",new Highlight(ranges[findIndex.value]!));
}
function moveFind(direction:number): void {
  if (!ranges.length || !body.value) return;
  findIndex.value=(findIndex.value+direction+ranges.length)%ranges.length;
  const range=ranges[findIndex.value]!;
  CSS.highlights.set("aiclaw-file-active",new Highlight(range));
  const rect=range.getBoundingClientRect(),pane=body.value.getBoundingClientRect();
  body.value.scrollTop+=rect.top-pane.top-body.value.clientHeight/3;
  if(rect.left<pane.left||rect.right>pane.right)body.value.scrollLeft+=rect.left-pane.left;
}
async function openFind(): Promise<void> { findOpen.value=true;await nextTick();findInput.value?.focus();findInput.value?.select(); }
function closeFind(): void {findOpen.value=false;lineError.value="";CSS.highlights.delete("aiclaw-file-find");CSS.highlights.delete("aiclaw-file-active");}
async function jumpLine(): Promise<void> {
  const value=Number(lineNumber.value),max=Math.min(3000,visibleLines.value.length);
  if (!Number.isInteger(value)||value<1||value>max) {lineError.value=t("请输入 1 到 {max} 的行号",{max});return;}
  lineError.value="";mode.value=props.preview.change ? (mode.value==="before"?"before":"after") : "source";
  await nextTick();await nextTick();activeLine.value=value;
  if(body.value)body.value.scrollTop=12+(value-1)*21;
}
function onFindKey(event:KeyboardEvent):void {
  if((event.metaKey||event.ctrlKey)&&event.key.toLowerCase()==="f"&&!event.altKey){event.preventDefault();void openFind();}
}
watch([findOpen,findQuery,mode,layout,()=>props.preview.request,()=>props.preview.content,()=>props.preview.loading],async()=>{await nextTick();highlightFind();if(findOpen.value&&findQuery.value)moveFind(0);},{flush:"post"});
watch(findQuery,()=>{findIndex.value=0;});
watch(()=>props.preview.request,()=>{findIndex.value=0;activeLine.value=0;lineError.value="";});
let restoring = false, restoreTicket = 0;
const positionKey = () => mode.value === "diff" ? `diff:${layout.value}` : mode.value;
function restorePosition(): void {
  const saved = props.preview.reading?.positions[positionKey()];
  if (body.value) { body.value.scrollTop = saved?.top ?? 0; body.value.scrollLeft = saved?.left ?? 0; }
}
function saveReading(scroll = false): void {
  if (restoring) return;
  const positions = { ...props.preview.reading?.positions };
  if (scroll && body.value) positions[positionKey()] = { top: body.value.scrollTop, left: body.value.scrollLeft };
  actions.savePreviewReading(props.preview.request, { mode: mode.value, layout: layout.value, positions });
}
watch(() => [props.preview.request, props.preview.loading], async () => {
  const ticket = ++restoreTicket;
  restoring = true;
  const saved = props.preview.reading;
  const allowed = ["source", ...(props.preview.change ? ["diff", "before", "after"] : []), ...(props.preview.language === "markdown" ? ["rendered"] : [])];
  mode.value = saved && allowed.includes(saved.mode) ? saved.mode : props.preview.change ? "diff" : props.preview.language === "markdown" ? "rendered" : "source";
  layout.value = saved?.layout ?? "split";
  copied.value = false;
  await nextTick();
  if (ticket === restoreTicket) { restorePosition(); restoring = false; }
}, { immediate: true });
watch([mode, layout], async () => {
  if (restoring) return;
  saveReading(); copied.value = false;
  const ticket = ++restoreTicket;
  restoring = true;
  await nextTick();
  if (ticket === restoreTicket) { restorePosition(); restoring = false; }
});
const content = computed(() => mode.value === "before" ? props.preview.change?.before ?? "" : mode.value === "after" ? props.preview.change?.after ?? "" : props.preview.content);
const visibleLines = computed(() => content.value.slice(0, 512 * 1024).split("\n", 3001));
const displayText = computed(() => visibleLines.value.slice(0, 3000).join("\n"));
const truncated = computed(() => content.value.length > 512 * 1024 || visibleLines.value.length > 3000);
const code = computed(() => highlightCode(displayText.value, props.preview.language));
const markup = computed(() => renderPreviewMarkdown(displayText.value));
const diff = computed(() => props.preview.change ? numberedDiff(props.preview.change.before, props.preview.change.after) : []);
const lines = computed(() => diff.value.slice(0, 3000));
const split = computed(() => splitDiff(lines.value));
const diffLanguage = computed(() => (props.preview.change?.before.length ?? 0) + (props.preview.change?.after.length ?? 0) > 50_000 ? "plaintext" : props.preview.language);
function onTabKey(event: KeyboardEvent, index: number): void {
  const count = store.filePreviews.length;
  let next = index;
  if (event.key === "ArrowRight") next = (index + 1) % count;
  else if (event.key === "ArrowLeft") next = (index + count - 1) % count;
  else if (event.key === "Home") next = 0;
  else if (event.key === "End") next = count - 1;
  else return;
  event.preventDefault();
  actions.activateFilePreview(store.filePreviews[next]!.request);
  void nextTick(() => document.querySelector<HTMLButtonElement>('.preview-file-tabs [aria-selected="true"]')?.focus());
}
function close(): void { actions.closeFilePreview(); previousFocus?.focus(); }
function onKey(event: KeyboardEvent): void {
  if (event.key === "Escape") { event.preventDefault(); if(findOpen.value)closeFind();else close(); }
}
async function copy(): Promise<void> {
  try { await navigator.clipboard.writeText(content.value); copied.value = true; }
  catch { actions.showError(t("复制失败")); }
}
function openReference(event: MouseEvent): void {
  const target = (event.target as HTMLElement)?.closest(".file-ref");
  if (target) { event.preventDefault(); void actions.openFile(previewReference(props.preview.path, target.getAttribute("data-file-path") ?? "")); }
}
onMounted(async () => { window.addEventListener("keydown",onFindKey);await nextTick(); closeButton.value?.focus(); });
onUnmounted(() => { window.removeEventListener("keydown",onFindKey);closeFind();previousFocus?.focus(); });
</script>
<template>
  <aside class="file-preview" :aria-label="t('文件预览')" @keydown="onKey">
    <header class="preview-header">
      <span class="preview-file-mark" aria-hidden="true">{{ fileType(preview.path) }}</span>
      <div class="preview-heading"><strong>{{ filename(preview.path) }}</strong><small :title="preview.path">{{ preview.path }}</small></div>
      <div class="preview-header-actions">
        <button :aria-label="t('浏览工作区文件')" :title="t('浏览工作区文件')" @click="actions.toggleFileBrowser()"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 7V5h6l2 2h10v13H3Z"/></svg></button>
        <button :aria-label="t('查找文件内容')" :title="t('查找文件内容')" @click="openFind"><svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="10" cy="10" r="6"/><path d="m15 15 6 6"/></svg></button>
        <button :disabled="preview.loading" :aria-label="t('刷新')" :title="t('重新读取当前文件')" @click="actions.refreshFilePreview()"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M20 7v5h-5M4 17v-5h5"/><path d="M6.1 7a7 7 0 0 1 11.6-1L20 9M4 15l2.3 3A7 7 0 0 0 17.9 17"/></svg><span class="sr-only">{{ t('刷新') }}</span></button>
        <button :disabled="preview.loading" :aria-label="t('对比')" :title="t('查看此文件最近一次记录的改动')" @click="actions.compareFilePreview()"><svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="4" width="18" height="16" rx="2"/><path d="M12 4v16M6 9h3M15 15h3M16.5 13.5v3"/></svg><span class="sr-only">{{ t('对比') }}</span></button>
        <button :disabled="preview.loading || !!preview.error" :aria-label="t('用系统应用打开')" :title="t('用系统应用打开')" @click="actions.openSystemFile(preview.path)"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M14 3h7v7M21 3l-9 9"/><path d="M10 3H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-5"/></svg><span class="sr-only">{{ t('用系统应用打开') }}</span></button>
        <button ref="closeButton" class="preview-close" :aria-label="t('关闭文件预览')" @click="close"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="m6 6 12 12M18 6 6 18"/></svg></button>
      </div>
    </header>
    <div class="preview-file-tabs" role="tablist" :aria-label="t('已打开的文件')">
      <div v-for="(tab,index) in store.filePreviews" :key="tab.request" class="preview-file-tab" :class="{ active: tab.request === preview.request }">
        <button role="tab" :aria-selected="tab.request === preview.request" :tabindex="tab.request === preview.request ? 0 : -1" :title="tab.path" @click="actions.activateFilePreview(tab.request)" @keydown="onTabKey($event,index)"><span class="tab-file-type" aria-hidden="true">{{ fileType(tab.path) }}</span><span class="tab-name">{{ filename(tab.path) }}</span></button>
        <button class="tab-close" :aria-label="t('关闭文件 {file}', { file: filename(tab.path) })" @click="actions.closePreviewTab(tab.request)">×</button>
      </div>
    </div>
    <nav class="preview-tabs" :aria-label="t('预览视图')">
      <div class="preview-view-switch">
      <template v-if="preview.change">
        <button :aria-pressed="mode === 'diff'" @click="mode='diff'">{{ t('差异') }}</button>
        <button :aria-pressed="mode === 'before'" @click="mode='before'">{{ t('修改前') }}</button>
        <button :aria-pressed="mode === 'after'" @click="mode='after'">{{ t('修改后') }}</button>
      </template>
      <button v-else :aria-pressed="mode === 'source'" @click="mode='source'">{{ t('原文') }}</button>
      <button v-if="preview.language === 'markdown'" :aria-pressed="mode === 'rendered'" @click="mode='rendered'">{{ t('渲染') }}</button>
      </div>
      <div class="preview-type-actions">
        <span class="preview-language">{{ preview.language }}</span>
        <button v-if="mode !== 'diff'" :disabled="preview.loading || !!preview.error" @click="copy"><svg viewBox="0 0 24 24" aria-hidden="true"><rect x="8" y="8" width="12" height="13" rx="2"/><path d="M16 8V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h3"/></svg>{{ copied ? t('已复制') : t('复制内容') }}</button>
      </div>
    </nav>
    <div v-if="findOpen" class="file-find-bar">
      <input ref="findInput" v-model="findQuery" :aria-label="t('查找文件内容')" :placeholder="t('查找文件内容')" @keydown.enter.prevent="moveFind($event.shiftKey?-1:1)" />
      <span role="status">{{ findCount ? `${findIndex+1}/${findCount}${findTruncated?'+':''}` : t('无匹配') }}</span>
      <button :disabled="!findCount" :aria-label="t('上一个匹配')" @click="moveFind(-1)">↑</button><button :disabled="!findCount" :aria-label="t('下一个匹配')" @click="moveFind(1)">↓</button><button :aria-label="t('关闭查找')" @click="closeFind">×</button>
      <input v-model="lineNumber" class="line-input" inputmode="numeric" :aria-label="t('跳到行号')" :placeholder="t('行号')" @keydown.enter.prevent="jumpLine" /><button @click="jumpLine">{{ t('跳行') }}</button>
    </div><p v-if="lineError" class="preview-error" role="alert">{{ lineError }}</p>
    <div v-if="mode === 'diff'" class="preview-tools">
      <button :aria-pressed="layout === 'split'" @click="layout='split'">{{ t('并排') }}</button>
      <button :aria-pressed="layout === 'unified'" @click="layout='unified'">{{ t('统一') }}</button>
    </div>
    <p v-if="preview.change" class="preview-note">{{ t('已记录的修改快照；当前文件可能不同。') }}</p>
    <p v-if="preview.notice" class="preview-note" role="status">{{ preview.notice }}</p>
    <p v-if="preview.loading" class="preview-note" role="status">{{ t('正在读取文件…') }}</p>
    <p v-else-if="preview.error" class="preview-error" role="alert">{{ preview.error }}</p>
    <template v-else>
      <div v-if="mode === 'diff'" ref="body" class="preview-body diff" @scroll="saveReading(true)">
        <p v-if="!diff.length" class="preview-note">{{ t('没有文本差异') }}</p>
        <div v-else-if="layout === 'split'" class="split-diff">
          <div class="diff-labels"><span>{{ t('修改前') }}</span><span>{{ t('修改后') }}</span></div>
          <div v-for="(row,index) in split" :key="index" class="split-row">
            <div class="diff-cell" :class="row.before?.kind"><span class="line-number">{{ row.before?.beforeLine }}</span><code v-html="highlightCode(row.before?.text ?? '', diffLanguage)" /></div>
            <div class="diff-cell" :class="row.after?.kind"><span class="line-number">{{ row.after?.afterLine }}</span><code v-html="highlightCode(row.after?.text ?? '', diffLanguage)" /></div>
          </div>
        </div>
        <div v-else class="unified-diff">
          <div v-for="(line,index) in lines" :key="index" class="diff-row" :class="line.kind">
            <span class="line-number">{{ line.beforeLine }}</span><span class="line-number">{{ line.afterLine }}</span><span>{{ line.kind==='add'?'+':line.kind==='remove'?'-':' ' }}</span><code v-html="highlightCode(line.text, diffLanguage)" />
          </div>
        </div>
        <p v-if="diff.length > 3000" class="preview-note">{{ t('差异超过显示范围，仅预览前 3000 行。') }}</p>
      </div>
      <div v-else-if="mode === 'rendered'" ref="body" class="preview-body markdown-preview prose" @scroll="saveReading(true)" @click="openReference" v-html="markup" />
      <div v-else ref="body" class="preview-body source-code" @scroll="saveReading(true)"><pre class="line-numbers" aria-hidden="true"><span v-for="(_,index) in visibleLines.slice(0,3000)" :key="index" :class="{current:index+1===activeLine}">{{ index+1 }}</span></pre><pre><code v-html="code" /></pre></div>
      <p v-if="mode !== 'diff' && truncated" class="preview-note">{{ t('内容超过显示范围，仅预览前 3000 行或 512 KB。') }}</p>
    </template>
  </aside>
</template>
<style scoped>
.file-preview{width:640px;max-width:55%;flex:none;min-width:0;height:100%;display:flex;flex-direction:column;background:var(--surface);border-left:1px solid var(--rule);color:var(--ink)}
.preview-header{display:flex;align-items:center;padding:12px 16px;gap:11px;border-bottom:1px solid var(--rule);flex:none}
.preview-file-mark{display:grid;place-items:center;flex:none;width:28px;height:32px;border:1px solid var(--rule);border-radius:7px;background:var(--surface-2);color:var(--accent);font-size:8px;font-weight:650;letter-spacing:.3px;overflow:hidden}
.preview-heading{flex:1;min-width:0;display:flex;flex-direction:column;gap:4px}.preview-heading strong{font-size:13px;font-weight:600;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.preview-heading small{font-size:10px;color:var(--muted);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.file-preview button{border:0;border-radius:6px;padding:6px 9px;background:transparent;color:var(--ink-2);cursor:pointer;font:inherit;font-size:11px;transition:background .15s,color .15s}.file-preview button:hover{background:var(--accent-soft);color:var(--accent)}.file-preview button:focus-visible{outline:2px solid var(--accent);outline-offset:2px}.file-preview button:disabled{opacity:.4;cursor:default}.file-preview svg{width:15px;height:15px;fill:none;stroke:currentColor;stroke-width:1.6;stroke-linecap:round;stroke-linejoin:round;flex:none}
.preview-header-actions{margin-left:auto;display:flex;align-items:center;flex:none;gap:3px}.preview-header-actions button{display:grid;place-items:center;width:29px;height:29px;padding:0}.preview-header-actions .preview-close{margin-left:5px;color:var(--muted)}
.sr-only{position:absolute;width:1px;height:1px;padding:0;margin:-1px;overflow:hidden;clip:rect(0,0,0,0);white-space:nowrap;border:0}
.preview-file-tabs{display:flex;flex:none;overflow-x:auto;border-bottom:1px solid var(--rule);background:var(--surface-2);padding:6px 12px 0;gap:3px}.preview-file-tab{display:flex;align-items:center;flex:none;max-width:230px;border-radius:7px 7px 0 0;border-bottom:2px solid transparent;padding:2px 3px 3px}.preview-file-tab.active{background:var(--surface);border-bottom-color:var(--accent)}.preview-file-tab button[role=tab]{display:flex;align-items:center;gap:7px;min-width:0;padding:6px 7px;color:var(--muted)}.preview-file-tab.active button[role=tab]{color:var(--ink)}.tab-name{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.tab-file-type{flex:none;font-size:8px;font-weight:600;letter-spacing:.2px;color:var(--accent);opacity:.85}.preview-file-tab .tab-close{flex:none;padding:2px 5px;color:var(--muted);font-size:14px;line-height:18px}
.preview-tabs,.preview-tools{display:flex;align-items:center;gap:5px;padding:8px 16px;border-bottom:1px solid var(--rule);flex-wrap:wrap;flex:none}.preview-view-switch{display:flex;align-items:center;gap:2px;padding:2px;background:var(--surface-2);border-radius:7px}.preview-view-switch button{padding:4px 10px;border-radius:5px}.preview-view-switch button[aria-pressed=true]{background:var(--surface);color:var(--accent);box-shadow:0 1px 3px #0000000d}.preview-tools button[aria-pressed=true]{background:var(--accent-soft);color:var(--accent)}
.preview-type-actions{margin-left:auto;display:flex;align-items:center;gap:9px}.preview-language{font-family:var(--mono,monospace);font-size:10px;color:var(--muted);padding:2px 6px;border-radius:4px;background:var(--surface-2)}.preview-type-actions button{display:flex;align-items:center;gap:5px;padding:4px 6px;color:var(--muted)}.preview-type-actions svg{width:13px;height:13px}
.preview-note,.preview-error{margin:0;padding:10px 16px;font-size:12px;line-height:1.5;overflow-wrap:anywhere}.preview-note{color:var(--muted)}.preview-error{color:var(--danger)}
.preview-body{flex:1;min-height:0;overflow:auto;font-size:12px}.source-code{display:flex;align-items:flex-start}.source-code pre{margin:0;padding:12px 0;line-height:21px;tab-size:4;font-family:var(--mono,monospace)}.source-code pre:last-child{padding-right:16px}.line-numbers,.line-number{color:var(--muted);user-select:none;text-align:right;font-variant-numeric:tabular-nums}.source-code .line-numbers{padding:12px 12px;min-width:36px;border-right:1px solid var(--rule);margin-right:12px;background:var(--surface-2)}
.diff{font-family:var(--mono,monospace);line-height:21px;tab-size:4}.split-diff{min-width:100%}.diff-labels{display:grid;grid-template-columns:1fr 1fr;position:sticky;top:0;background:var(--surface-2);border-bottom:1px solid var(--rule);font-family:inherit;font-size:11px}.diff-labels span{padding:5px 12px}.split-row{display:grid;grid-template-columns:minmax(0,1fr) minmax(0,1fr)}.diff-cell{display:flex;min-width:0;min-height:21px;border-right:1px solid var(--rule)}.diff-cell code{white-space:pre-wrap;overflow-wrap:anywhere;flex:1;padding-right:8px}.diff-cell .line-number{width:38px;flex:none;padding-right:9px;font-size:10px}.diff-row{display:grid;grid-template-columns:38px 38px 20px 1fr;min-width:max-content;min-height:21px}.diff-row .line-number{padding-right:8px;font-size:10px}.diff-row code{white-space:pre;padding-right:12px}.add{background:var(--ok-soft)}.remove{background:var(--danger-soft)}.markdown-preview{padding:16px 20px;overflow-wrap:anywhere;font-size:14px}.markdown-preview :deep(pre){overflow:auto}.markdown-preview :deep(table){width:100%}
.file-preview pre code,.file-preview .diff code{background:none;padding:0;font:inherit;line-height:inherit}
.file-preview :deep(.hljs-keyword),.file-preview :deep(.hljs-selector-tag){color:#8b4197}.file-preview :deep(.hljs-string),.file-preview :deep(.hljs-attr){color:var(--accent)}.file-preview :deep(.hljs-number),.file-preview :deep(.hljs-literal){color:#996515}.file-preview :deep(.hljs-comment){color:var(--muted)}.file-preview :deep(.hljs-title),.file-preview :deep(.hljs-built_in){color:#2869a3}
@media(prefers-color-scheme:dark){.file-preview :deep(.hljs-keyword),.file-preview :deep(.hljs-selector-tag){color:#d79de5}.file-preview :deep(.hljs-number),.file-preview :deep(.hljs-literal){color:#dfbd76}.file-preview :deep(.hljs-title),.file-preview :deep(.hljs-built_in){color:#84bcec}}
.file-find-bar{display:flex;align-items:center;gap:4px;padding:8px 12px;border-bottom:1px solid var(--rule);flex-wrap:wrap;font-size:11px}.file-find-bar input{width:130px;min-width:0;flex:1;padding:6px 8px;border:1px solid var(--rule);border-radius:6px;background:var(--surface-2);color:var(--ink);font:inherit}.file-find-bar .line-input{flex:none;width:45px}.file-find-bar span{color:var(--muted);white-space:nowrap}.source-code .line-numbers span{display:block;height:21px}.source-code .line-numbers .current{background:var(--accent-soft);color:var(--accent)}
</style>
<style>
::highlight(aiclaw-file-find){background:#ffe69b;color:#222}::highlight(aiclaw-file-active){background:#f2b74d;color:#111}
</style>
