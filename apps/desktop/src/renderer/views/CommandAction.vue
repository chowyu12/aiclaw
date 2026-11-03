<script setup lang="ts">
import { computed } from "vue";
import { commandRuns, runCommand, stopCommand } from "../command-state";
import { t } from "../i18n";
const props = defineProps<{command: string; sessionId: string; runKey: string; inline: boolean}>();
const run = computed(() => commandRuns[props.runKey]);
function execute(event: MouseEvent): void {
  // A command shown in a response may only run from a real user click or keyboard action.
  if (!event.isTrusted) return;
  void runCommand(props.runKey, props.sessionId, props.command);
}
</script>
<template>
  <span class="command-action" :class="{ inline }">
    <button v-if="!run || run.status === 'finished'" type="button" class="command-button" :title="t('在系统 Shell 中执行（不使用沙箱）')" @click.stop="execute">▶ {{ t('运行命令') }}</button>
    <button v-else type="button" class="command-button stop" :disabled="run.status !== 'running'" @click.stop="stopCommand(runKey)">■ {{ run.status === 'stopping' ? t('停止中…') : t('停止') }}</button>
    <span v-if="run" class="command-result" aria-live="polite">
      <span class="command-status">{{ run.status === 'starting' ? t('启动中…') : run.status === 'running' ? t('命令执行中…') : run.status === 'stopping' ? t('停止中…') : run.stopped ? t('已停止') : run.error ? t('命令执行失败') : t('退出码：{code}', {code: String(run.code ?? '—')}) }}</span>
      <span v-if="run.error" class="command-error">{{ run.error }}</span>
      <span v-if="run.output" class="command-output">{{ run.output }}</span>
    </span>
  </span>
</template>
<style scoped>
.command-action{display:block;white-space:normal;margin-top:8px}.command-button{position:absolute;top:8px;right:9px;border:1px solid var(--rule-strong);border-radius:6px;background:var(--surface);color:var(--ink-2);padding:4px 9px;font:12px var(--mono);cursor:pointer}.command-button:hover:not(:disabled){color:var(--accent);border-color:var(--accent)}.command-button:focus-visible{outline:2px solid var(--accent);outline-offset:2px}.command-button:disabled{opacity:.5;cursor:default}.inline{display:inline;position:relative;margin-left:6px}.inline .command-button{position:static;padding:2px 6px}.command-result{display:block;margin-top:8px;padding-top:8px;border-top:1px solid var(--rule);font:12px var(--mono)}.inline .command-result{white-space:normal}.command-status{display:block;color:var(--muted);margin-bottom:4px}.command-error{display:block;color:var(--danger);white-space:pre-wrap;overflow-wrap:anywhere}.command-output{display:block;white-space:pre-wrap;overflow-wrap:anywhere;max-height:320px;overflow:auto;user-select:text}
</style>
