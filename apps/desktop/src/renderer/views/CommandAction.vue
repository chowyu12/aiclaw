<script setup lang="ts">
import { computed } from "vue";
import { commandRuns, runCommand, stopCommand } from "../command-state";
import { t } from "../i18n";
const props = defineProps<{command: string; sessionId: string; runKey: string; outputTarget?: HTMLElement}>();
const run = computed(() => commandRuns[props.runKey]);
function execute(event: MouseEvent): void {
  // A command shown in a response may only run from a real user click or keyboard action.
  if (!event.isTrusted) return;
  void runCommand(props.runKey, props.sessionId, props.command);
}
</script>
<template>
  <span class="command-action">
    <button v-if="!run || run.status === 'finished'" type="button" class="command-button" :title="t('在系统 Shell 中执行（不使用沙箱）')" :aria-label="t('运行命令')" @click.stop="execute"><svg viewBox="0 0 20 20" aria-hidden="true"><path d="m6 5 8 5-8 5Z" /></svg></button>
    <button v-else type="button" class="command-button stop" :disabled="run.status !== 'running'" :aria-label="t('停止')" @click.stop="stopCommand(runKey)"><svg viewBox="0 0 20 20" aria-hidden="true"><rect x="5" y="5" width="10" height="10" rx="1" /></svg></button>
  </span>
  <Teleport v-if="run && outputTarget" :to="outputTarget">
    <section class="command-result" :aria-label="t('命令输出')">
      <header class="terminal-header"><span class="terminal-label"><svg viewBox="0 0 20 20" aria-hidden="true"><path d="m4 5 5 5-5 5m7 0h5" /></svg>{{ t('终端') }}</span><span class="command-status" role="status">{{ run.status === 'starting' ? t('启动中…') : run.status === 'running' ? t('命令执行中…') : run.status === 'stopping' ? t('停止中…') : run.stopped ? t('已停止') : run.error ? t('命令执行失败') : t('退出码：{code}', {code: String(run.code ?? '—')}) }}</span></header>
      <pre v-if="run.error" class="command-error">{{ run.error }}</pre>
      <pre v-if="run.output" class="command-output" role="log" :aria-label="t('命令输出')">{{ run.output }}</pre>
    </section>
  </Teleport>
</template>
<style scoped>
.command-action { display: inline-flex; vertical-align: middle; }
.command-button { display: inline-flex; align-items: center; justify-content: center; gap: 4px; min-width: 28px; height: 28px; padding: 0 5px; border: 0; border-radius: 6px; background: transparent; color: var(--ink-2); font: 11px var(--ui-font); cursor: pointer; }
.command-button:hover:not(:disabled) { color: var(--accent); background: var(--hover); }
.command-button:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
.command-button:disabled { opacity: .5; cursor: default; }
svg { width: 16px; height: 16px; flex: none; fill: none; stroke: currentColor; stroke-width: 1.4; stroke-linecap: round; stroke-linejoin: round; }
.command-result { min-width: 0; padding: 0 16px 12px; border-top: 1px solid var(--rule); background: var(--surface-2); color: var(--ink); }
.terminal-header { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 6px 12px; min-height: 40px; font: 11px var(--ui-font); }
.terminal-label { display: inline-flex; align-items: center; gap: 8px; color: var(--ink-2); }
.command-status { color: var(--muted); }
.command-result pre.command-error,.command-result pre.command-output { display: block; margin: 0; padding: 4px 0; border: 0; border-radius: 0; background: transparent; font: 12px/1.6 var(--mono); white-space: pre-wrap; overflow-wrap: anywhere; user-select: text; }
.command-result pre.command-output { max-height: 320px; overflow: auto; }
.command-result pre.command-error { color: var(--danger); }
</style>
