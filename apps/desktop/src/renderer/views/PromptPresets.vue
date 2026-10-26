<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from "vue";
import { actions, store } from "../store";
import { t } from "../i18n";
import { describeError } from "../errors";
import { PRESET_SLOTS, type PromptPreset } from "../../shared/presets";
const props = defineProps<{ draft: string }>();
const emit = defineEmits<{close: []}>();
const dialog = ref<HTMLDialogElement>();
const slot = ref(1), title = ref(""), text = ref(""), error = ref(""), saving = ref(false);
const presets = computed(() => store.config?.presets ?? []);
const previousFocus = document.activeElement as HTMLElement | null;
function select(value: number): void {
  slot.value = value;
  const preset = presets.value.find(p => p.slot === value);
  title.value = preset?.title ?? ""; text.value = preset?.text ?? props.draft; error.value = "";
}
async function save(remove = false): Promise<void> {
  if (saving.value || (!remove && (!title.value.trim() || !text.value.trim()))) return;
  saving.value = true; error.value = "";
  const next: PromptPreset[] = presets.value.filter(p => p.slot !== slot.value).map(p => ({...p}));
  if (!remove) next.push({slot:slot.value,title:title.value.trim(),text:text.value});
  try { await actions.saveConfig({presets:next}); emit("close"); }
  catch (e) { error.value = describeError(e); }
  finally { saving.value = false; }
}
onMounted(async () => { select(1); await nextTick(); dialog.value?.showModal(); });
onUnmounted(() => { dialog.value?.close(); previousFocus?.focus(); });
</script>

<template>
  <dialog ref="dialog" class="preset-dialog" @cancel.prevent="emit('close')">
    <form @submit.prevent="save()">
      <header><h3>{{ t('常用提示词') }}</h3><button type="button" :aria-label="t('关闭')" @click="emit('close')">×</button></header>
      <p class="preset-note">{{ t('保存常用指令，在 / 菜单中插入；发送时沿用正常队列和权限。') }}</p>
      <label>{{ t('位置') }}<select :value="slot" :disabled="saving" @change="select(Number(($event.target as HTMLSelectElement).value))"><option v-for="n in PRESET_SLOTS" :key="n" :value="n">{{ n }} · {{ presets.find(p => p.slot === n)?.title || t('未设置') }}</option></select></label>
      <label>{{ t('名称') }}<input v-model="title" maxlength="80" required :disabled="saving" /></label>
      <label>{{ t('提示词') }}<textarea v-model="text" rows="8" maxlength="20000" required :disabled="saving" /></label>
      <p v-if="error" role="alert" class="attach-error">{{ error }}</p>
      <footer><button v-if="presets.some(p => p.slot === slot)" type="button" :disabled="saving" @click="save(true)">{{ t('删除') }}</button><span /><button type="button" :disabled="saving" @click="emit('close')">{{ t('取消') }}</button><button :disabled="saving || !title.trim() || !text.trim()">{{ t('保存') }}</button></footer>
    </form>
  </dialog>
</template>
<style scoped>
.preset-dialog { width: min(560px, calc(100vw - 48px)); max-height: calc(100vh - 48px); overflow: auto; padding: 22px; border: 1px solid var(--rule); border-radius: 16px; background: var(--surface); color: var(--ink); box-shadow: 0 20px 70px #0003; }
.preset-dialog::backdrop { background: #0004; }
header, footer { display: flex; align-items: center; gap: 10px; } header h3 { margin: 0; flex: 1; } footer span { flex: 1; }
label { display: grid; gap: 6px; margin: 14px 0; font-size: 13px; } input, textarea, select { box-sizing: border-box; width: 100%; padding: 9px; border: 1px solid var(--rule); border-radius: 8px; background: var(--surface); color: inherit; font: inherit; } textarea { resize: vertical; }
button { padding: 7px 12px; border: 1px solid var(--rule); border-radius: 8px; background: var(--surface); color: inherit; cursor: pointer; } .preset-note { color: var(--muted); font-size: 13px; line-height: 1.6; }
</style>
