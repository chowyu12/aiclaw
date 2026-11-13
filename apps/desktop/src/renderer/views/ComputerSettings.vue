<script setup lang="ts">
import { computed, ref } from "vue";
import type { ComputerDiagnostics } from "../../shared/computer-control";
import { actions, store } from "../store";
import { describeError } from "../errors";
import { t } from "../i18n";
const diagnostics = ref<ComputerDiagnostics | null>(null);
const checking = ref(false);
const error = ref("");
const mode = computed(() => store.computer.mode === "paused" ? t("已暂停") : store.computer.mode === "stopped" ? t("已停止") : t("可操作"));
function permission(value: boolean | null | undefined): string {
  return value === true ? t("已允许") : value === false ? t("未允许") : t("待检查");
}
async function check(setup = false): Promise<void> {
  if (checking.value) return;
  checking.value = true; error.value = "";
  try {
    diagnostics.value = await (setup ? window.aiclaw.computer.setupPermissions() : window.aiclaw.computer.diagnose()) as ComputerDiagnostics;
  } catch (reason) { error.value = describeError(reason); }
  finally { checking.value = false; }
}
</script>

<template>
  <section class="computer-settings">
    <header class="computer-settings-header">
      <div class="computer-settings-heading">
        <h2>{{ t("桌面控制") }}</h2>

      </div>
      <span class="computer-mode" :data-mode="store.computer.mode"><span class="computer-mode-dot" aria-hidden="true"></span>{{ mode }}</span>
    </header>
    <div v-if="store.computer.mode !== 'ready'" class="computer-setting-actions">
      <button @click="actions.controlComputer('resume')">{{ t("恢复桌面操作") }}</button>
    </div>
    <dl class="computer-status-list">
      <div><dt>{{ t("运行状态") }}</dt><dd :data-state="diagnostics ? (diagnostics.available ? 'ok' : 'error') : 'unknown'">{{ diagnostics ? (diagnostics.available ? t("已就绪") : t("不可用")) : t("待检查") }}</dd></div>
      <div><dt>{{ t("辅助功能") }}</dt><dd :data-state="diagnostics?.accessibility == null ? 'unknown' : diagnostics.accessibility ? 'ok' : 'error'">{{ permission(diagnostics?.accessibility) }}</dd></div>
      <div><dt>{{ t("屏幕访问") }}</dt><dd :data-state="diagnostics?.screenCapture == null ? 'unknown' : diagnostics.screenCapture ? 'ok' : 'error'">{{ permission(diagnostics?.screenCapture) }}</dd></div>
    </dl>
    <p v-if="diagnostics?.message" class="computer-diagnostic-message" role="status">{{ t(diagnostics.message) }}</p>
    <p v-if="error" class="computer-diagnostic-error" role="alert">{{ error }}</p>
    <div class="computer-setting-actions">
      <button :disabled="checking" @click="check()">{{ checking ? t("正在检查…") : t("检查状态") }}</button>
      <button v-if="diagnostics?.platform === 'darwin'" :disabled="checking" @click="check(true)">{{ t("打开授权引导") }}</button>
    </div>
    <p class="computer-settings-note">{{ t("在「插件」中启用桌面操作。系统权限仍需你手动确认。") }}</p>
  </section>
</template>

<style scoped>
.computer-settings { padding: 22px; gap: 18px; }
.computer-settings-header { display: flex; flex-direction: row; align-items: flex-start; justify-content: space-between; gap: 16px; }
.computer-settings-heading { min-width: 0; }
h2 { margin: 0; color: var(--ink); font-size: 15px; font-weight: 650; }
p { margin: 0; color: var(--muted); font-size: 12px; line-height: 1.6; }
.computer-settings-heading p { margin-top: 6px; }
.computer-mode { display: inline-flex; flex: none; align-items: center; gap: 6px; padding: 5px 9px; border: 1px solid var(--rule); border-radius: 999px; background: var(--surface-2); color: var(--ink-2); font-size: 12px; line-height: 1; }
.computer-mode-dot { width: 6px; height: 6px; border-radius: 50%; background: var(--muted); }
.computer-mode[data-mode="ready"] .computer-mode-dot { background: var(--ok); }
.computer-mode[data-mode="stopped"] .computer-mode-dot { background: var(--danger); }
.computer-setting-actions { display: flex; gap: 8px; flex-wrap: wrap; }
button { min-height: 34px; padding: 7px 12px; border: 1px solid var(--rule); border-radius: 8px; background: var(--surface); color: var(--ink); cursor: pointer; font: inherit; font-size: 13px; transition: border-color .15s, background .15s; }
button:disabled { opacity: .55; cursor: default; }
button:hover:not(:disabled) { border-color: var(--accent); background: var(--surface-2); }
button:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
.computer-status-list { display: grid; gap: 0; margin: 0; padding: 2px 12px; border: 1px solid var(--rule); border-radius: 10px; background: var(--surface-2); font-size: 13px; }
.computer-status-list div { display: flex; align-items: center; justify-content: space-between; gap: 12px; min-height: 38px; }
.computer-status-list div + div { border-top: 1px solid var(--rule); }
dt { color: var(--muted); }
dd { margin: 0; color: var(--ink-2); text-align: right; }
dd[data-state="ok"] { color: var(--ok); }
dd[data-state="error"], .computer-diagnostic-error { color: var(--danger, #c43e3e); }
.computer-settings-note { padding-top: 2px; }
@media (max-width: 500px) {
  .computer-settings { padding: 18px; }
  .computer-settings-header { flex-wrap: wrap; }
}
</style>
