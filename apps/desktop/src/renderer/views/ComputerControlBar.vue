<script setup lang="ts">
import { computed } from "vue";
import { actions, store } from "../store";
import { t } from "../i18n";
import { computerActionIsObservation } from "../../shared/computer-control";
const visible = computed(() => store.computer.mode !== "ready" || !!store.computer.active || store.computer.watching > 0);
const status = computed(() => store.computer.mode === "paused" ? t("桌面操作已暂停")
  : store.computer.mode === "stopped" ? t("桌面操作已停止")
  : store.computer.active?.action === "waiting" ? t("AIClaw 已连接目标窗口")
  : store.computer.active && !computerActionIsObservation(store.computer.active.action) ? t("AIClaw 正在操作桌面") : t("AIClaw 正在观察窗口"));
</script>

<template>
  <div v-if="visible" class="computer-control-bar" role="region" :aria-label="t('桌面控制')" :data-mode="store.computer.mode">
    <span class="computer-indicator" aria-hidden="true">▣</span>
    <div class="computer-control-label" role="status" aria-live="polite">
      <strong>{{ status }}</strong>
      <span v-if="store.computer.active?.app">{{ t("目标：{app}", { app: store.computer.active.displayName || store.computer.active.app }) }}</span>
      <span v-else-if="store.computer.mode !== 'ready'">{{ t("恢复后需要重新观察窗口，不会重放之前的操作。") }}</span>
    </div>
    <button v-if="store.computer.mode === 'ready'" @click="actions.controlComputer('pause')">{{ t("暂停桌面操作") }}</button>
    <button v-else @click="actions.controlComputer('resume')">{{ t("恢复桌面操作") }}</button>
    <button v-if="store.computer.mode !== 'stopped'" class="computer-stop" @click="actions.controlComputer('stop')">{{ t("停止桌面操作") }}</button>
  </div>
</template>

<style scoped>
.computer-control-bar { display:flex; align-items:center; gap:10px; padding:10px 16px; border-bottom:1px solid var(--rule); background:var(--surface-2); flex-shrink:0; }
.computer-indicator { color:var(--accent); font-size:20px; }
.computer-control-label { min-width:0; flex:1; display:flex; flex-direction:column; gap:2px; font-size:12px; }
.computer-control-label strong { font-size:13px; font-weight:600; }
.computer-control-label span { color:var(--muted); overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
button { flex-shrink:0; border:1px solid var(--rule); border-radius:6px; background:var(--surface); color:var(--ink); padding:6px 10px; cursor:pointer; font:inherit; font-size:12px; }
button:hover { border-color:var(--accent); }
.computer-stop { color:var(--danger, #c43e3e); }
@media (max-width:640px) { .computer-control-bar { gap:6px; padding:8px; } .computer-control-label span { display:none; } button { padding:6px; } }
</style>
