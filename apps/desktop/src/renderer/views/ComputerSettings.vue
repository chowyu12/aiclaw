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
    <header><h2>{{ t("桌面控制") }}</h2><span>{{ mode }}</span></header>
    <p>{{ t("控制状态对所有聊天生效。暂停或停止后，桌面操作不会继续执行。") }}</p>
    <div class="computer-setting-actions">
      <button v-if="store.computer.mode === 'ready'" @click="actions.controlComputer('pause')">{{ t("暂停桌面操作") }}</button>
      <button v-else @click="actions.controlComputer('resume')">{{ t("恢复桌面操作") }}</button>
      <button @click="actions.controlComputer('stop')">{{ t("停止桌面操作") }}</button>
    </div>
    <dl>
      <div><dt>{{ t("运行状态") }}</dt><dd>{{ diagnostics ? (diagnostics.available ? t("已就绪") : t("不可用")) : t("待检查") }}</dd></div>
      <div><dt>{{ t("辅助功能") }}</dt><dd>{{ permission(diagnostics?.accessibility) }}</dd></div>
      <div><dt>{{ t("屏幕访问") }}</dt><dd>{{ permission(diagnostics?.screenCapture) }}</dd></div>
    </dl>
    <p v-if="diagnostics?.message" class="computer-diagnostic-message" role="status">{{ t(diagnostics.message) }}</p>
    <p v-if="error" class="computer-diagnostic-error" role="alert">{{ error }}</p>
    <div class="computer-setting-actions">
      <button :disabled="checking" @click="check()">{{ checking ? t("正在检查…") : t("检查状态") }}</button>
      <button v-if="diagnostics?.platform === 'darwin'" :disabled="checking" @click="check(true)">{{ t("打开授权引导") }}</button>
    </div>
    <p>{{ t("在「插件」中启用桌面操作。系统权限仍需你手动确认。") }}</p>
  </section>
</template>

<style scoped>
.computer-settings { padding:24px 0; border-top:1px solid var(--rule); }
header { display:flex; align-items:center; justify-content:space-between; gap:12px; }
h2 { font-size:15px; margin:0; }
header span, p, dl { font-size:13px; }
p { color:var(--muted); line-height:1.6; }
.computer-setting-actions { display:flex; gap:8px; flex-wrap:wrap; }
button { border:1px solid var(--rule); border-radius:6px; background:var(--surface); color:var(--ink); padding:7px 12px; cursor:pointer; font:inherit; font-size:13px; }
button:disabled { opacity:.55; cursor:default; }
button:hover:not(:disabled) { border-color:var(--accent); }
dl { display:grid; gap:8px; margin:18px 0; }
dl div { display:flex; gap:16px; }
dt { width:90px; color:var(--muted); } dd { margin:0; }
.computer-diagnostic-error { color:var(--danger, #c43e3e); }
</style>
