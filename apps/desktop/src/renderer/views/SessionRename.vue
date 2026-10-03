<script setup lang="ts">
import { onMounted, ref } from "vue";
import { actions } from "../store";
import { describeError } from "../errors";
import { t } from "../i18n";

const props = defineProps<{ sessionId: string; title: string }>();
const emit = defineEmits<{ close: [] }>();
const draft = ref(props.title);
const input = ref<HTMLInputElement>();
const saving = ref(false);
const error = ref("");
onMounted(() => { input.value?.focus(); input.value?.select(); });

async function save(): Promise<void> {
  if (saving.value || !draft.value.trim()) return;
  saving.value = true;
  error.value = "";
  try {
    await actions.renameSession(props.sessionId, draft.value.trim());
    emit("close");
  } catch (reason) {
    error.value = describeError(reason);
  } finally { saving.value = false; }
}

function cancel(): void { if (!saving.value) emit("close"); }
</script>

<template>
  <form class="session-rename" @submit.prevent="save" @click.stop @keydown.esc.stop.prevent="cancel">
    <label>
      <span>{{ t("会话名称") }}</span>
      <input ref="input" v-model="draft" maxlength="200" :disabled="saving" :aria-label="t('会话名称')" />
    </label>
    <p v-if="error" class="rename-error" role="alert">{{ error }}</p>
    <div class="rename-actions">
      <button type="button" class="ghost small" :disabled="saving" @click="cancel">{{ t("取消") }}</button>
      <button type="submit" class="primary small" :disabled="saving || !draft.trim()">{{ saving ? t("保存中…") : t("保存") }}</button>
    </div>
  </form>
</template>

<style scoped>
.session-rename {
  position: absolute;
  inset: 30px 6px auto;
  z-index: 6;
  padding: 10px;
  border: 1px solid var(--rule);
  border-radius: var(--r-md);
  background: var(--surface);
  color: var(--ink);
  box-shadow: var(--shadow-3);
  cursor: default;
}
label { display: flex; flex-direction: column; gap: 6px; font-size: 12px; }
input { width: 100%; min-width: 0; padding: 6px 8px; font-size: 12px; }
.rename-actions { display: flex; justify-content: flex-end; gap: 6px; margin-top: 8px; }
.rename-error { margin: 8px 0 0; color: var(--danger); font-size: 12px; overflow-wrap: anywhere; }
</style>
