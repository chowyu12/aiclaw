<script setup lang="ts">
import { computed, onUnmounted, ref } from "vue";
import { highlightCode } from "../code-highlight";
import { t } from "../i18n";
import CommandAction from "./CommandAction.vue";

const props = defineProps<{code: string; language: string; runnable: boolean; sessionId?: string; runKey: string}>();
const output = ref<HTMLElement>();
const copied = ref<"" | "ok" | "failed">("");
let reset: ReturnType<typeof setTimeout> | undefined;
const language = computed(() => props.language.toLowerCase() || (props.runnable ? "bash" : ""));
const label = computed(() => ({sh: "Bash", bash: "Bash", zsh: "Zsh", shell: "Shell", console: "Terminal", terminal: "Terminal", powershell: "PowerShell", pwsh: "PowerShell", cmd: "CMD", python: "Python", py: "Python", javascript: "JavaScript", js: "JavaScript", typescript: "TypeScript", ts: "TypeScript", json: "JSON", html: "HTML", css: "CSS", sql: "SQL", go: "Go"})[language.value] ?? (props.language || t("代码")));
const highlighted = computed(() => highlightCode(props.code, language.value));
async function copy(): Promise<void> {
  try { copied.value = await window.aiclaw.clipboard.write(props.code) ? "ok" : "failed"; }
  catch { copied.value = "failed"; }
  clearTimeout(reset);
  reset = setTimeout(() => { copied.value = ""; }, 2000);
}
onUnmounted(() => clearTimeout(reset));
</script>

<template>
  <section class="code-panel" :class="{ runnable }" :data-language="language" :aria-label="label">
    <header class="code-toolbar">
      <span class="code-language"><svg viewBox="0 0 20 20" aria-hidden="true"><path d="m6 6-4 4 4 4m8-8 4 4-4 4m-3-10-2 12" /></svg>{{ label }}</span>
      <span class="code-actions">
        <CommandAction v-if="runnable && sessionId" :command="code" :session-id="sessionId" :run-key="runKey" :output-target="output" />
        <button class="code-copy" type="button" :title="t(copied === 'ok' ? '已复制' : copied === 'failed' ? '复制失败' : '复制代码')" :aria-label="t(copied === 'ok' ? '已复制' : copied === 'failed' ? '复制失败' : '复制代码')" @click="copy">
          <svg v-if="copied === 'ok'" viewBox="0 0 20 20" aria-hidden="true"><path d="m4 10 4 4 8-8" /></svg>
          <svg v-else viewBox="0 0 20 20" aria-hidden="true"><rect x="7" y="6" width="9" height="11" rx="2" /><path d="M12 6V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v7a2 2 0 0 0 2 2h2" /></svg>
        </button>
        <span v-if="copied" class="copy-feedback" role="status">{{ t(copied === 'ok' ? '已复制' : '复制失败') }}</span>
      </span>
    </header>
    <pre class="code-source"><code v-html="highlighted" /></pre>
    <div ref="output" class="code-terminal" />
  </section>
</template>

<style scoped>
.code-panel { --code-keyword: #8451bc; --code-number: #996515; --code-title: #2869a3; min-width: 0; overflow: hidden; border: 1px solid var(--rule); border-radius: 16px; background: var(--surface-2); color: var(--ink); }
.code-toolbar { display: flex; align-items: center; justify-content: space-between; gap: 12px; min-height: 44px; padding: 6px 14px; font: 12px var(--ui-font); }
.code-language,.code-actions { display: flex; align-items: center; gap: 10px; min-width: 0; }
.code-language { overflow-wrap: anywhere; }
svg { width: 16px; height: 16px; flex: none; fill: none; stroke: currentColor; stroke-width: 1.4; stroke-linecap: round; stroke-linejoin: round; }
.code-copy { display: inline-flex; align-items: center; justify-content: center; flex: none; width: 28px; height: 28px; padding: 0; border: 0; border-radius: 6px; background: transparent; color: var(--ink-2); cursor: pointer; }
.code-copy:hover { background: var(--hover); color: var(--ink); }
.code-copy:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
.copy-feedback { font-size: 11px; color: var(--muted); }
.code-panel pre.code-source { margin: 0; padding: 4px 18px 16px; border: 0; border-radius: 0; background: transparent; overflow-x: auto; tab-size: 4; line-height: 1.65; }
.code-panel .code-source code { padding: 0; border: 0; background: transparent; color: inherit; font: 12px/1.65 var(--mono); white-space: pre; }
.code-terminal:empty { display: none; }
.code-source :deep(.hljs-keyword),.code-source :deep(.hljs-selector-tag),.code-source :deep(.hljs-built_in) { color: var(--code-keyword); }
.code-source :deep(.hljs-string),.code-source :deep(.hljs-attr) { color: var(--accent); }
.code-source :deep(.hljs-number),.code-source :deep(.hljs-literal) { color: var(--code-number); }
.code-source :deep(.hljs-comment) { color: var(--muted); }
.code-source :deep(.hljs-title) { color: var(--code-title); }
:global(html[data-theme="dark"] .code-panel),:global(html[data-theme="modern"] .code-panel) { --code-keyword: #c7a6ef; --code-number: #dfbd76; --code-title: #84bcec; }
@media (max-width: 640px) { .code-toolbar { padding-inline: 10px; } .code-panel pre.code-source { padding-inline: 12px; } .copy-feedback { display: none; } }
</style>
