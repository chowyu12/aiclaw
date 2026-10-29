<script setup lang="ts">
import { onMounted, ref } from "vue";
import { actions } from "../store";
import { t } from "../i18n";
import { describeError } from "../errors";
const props = defineProps<{ project?: {id: string; name: string; directories?: string[]} }>();
const emit = defineEmits<{close: []}>();
const name = ref(props.project?.name ?? "");
const directories = ref([...(props.project?.directories ?? [])]);
function addPath(path: string): void {
  const value = path.trim();
  if (value && !directories.value.includes(value)) directories.value.push(value);
}
const saving = ref(false), error = ref("");
const nameInput = ref<HTMLInputElement>();
onMounted(() => nameInput.value?.focus());
async function addDirectory(): Promise<void> {
  try { const path = await window.aiclaw.dialog.pickDirectory(); if(path) addPath(path); }
  catch(reason) { error.value = describeError(reason); }
}
async function save(): Promise<void> {
  if(saving.value || !name.value.trim()) return;
  saving.value = true; error.value = "";
  try { await actions.saveProject({id:props.project?.id, name:name.value, directories:directories.value}); emit("close"); }
  catch(reason) {error.value=describeError(reason);}
  finally {saving.value=false;}
}
</script>
<template>
  <div class="project-backdrop" @click.self="!saving && $emit('close')" @keydown.esc.prevent.stop="!saving && $emit('close')">
    <form class="project-editor" role="dialog" aria-modal="true" :aria-label="t('项目设置')" @submit.prevent="save">
      <h2>{{ t('项目设置') }}</h2>
      <label>{{ t('项目名称') }}<input ref="nameInput" v-model="name" maxlength="200" :disabled="saving" required /></label>
      <div class="directory-section">
        <span class="directory-label">{{ t('工作区目录') }}</span>
        <ul v-if="directories.length" class="directory-list">
          <li v-for="(directory, index) in directories" :key="directory" class="directory-row">
            <svg viewBox="0 0 20 20" aria-hidden="true"><path d="M2.5 5.5h5l1.5 2h8.5v7.5a1.5 1.5 0 0 1-1.5 1.5h-12A1.5 1.5 0 0 1 2.5 15z" /></svg>
            <span class="directory-path" :title="directory">{{ directory }}</span>
            <span v-if="index === 0" class="directory-default">{{ t('默认') }}</span>
            <button type="button" class="directory-remove" :aria-label="t('移除目录') + ': ' + directory" :title="t('移除目录')" :disabled="saving" @click="directories.splice(index, 1)">×</button>
          </li>
        </ul>
        <div v-else class="directory-empty">{{ t('未添加目录，默认使用 ~/aiclaw') }}</div>

      </div>
      <p>{{ t('可以配置多个目录；新会话可选择其中一个执行。留空默认使用 ~/aiclaw。修改目录不会移动已有会话的文件。') }}</p>
      <button type="button" class="ghost small" :disabled="saving" @click="addDirectory">{{ t('添加目录') }}</button>
      <p v-if="error" role="alert" class="error">{{ error }}</p>
      <footer><button type="button" class="ghost" :disabled="saving" @click="$emit('close')">{{ t('取消') }}</button><button type="submit" class="primary" :disabled="saving || !name.trim()">{{ t('保存') }}</button></footer>
    </form>
  </div>
</template>
<style scoped>
.project-backdrop{position:fixed;inset:0;z-index:100;display:grid;place-items:center;background:#0005;padding:24px}
.project-editor{width:min(500px,100%);max-height:85vh;overflow:auto;background:var(--surface);border:1px solid var(--rule);border-radius:14px;padding:24px;box-shadow:var(--shadow-3);color:var(--ink)}
h2{margin:0 0 18px;font-size:18px}label{display:flex;flex-direction:column;gap:8px;margin-bottom:16px;font-size:13px}input{padding:9px;width:100%;font:inherit}
.directory-section{font-size:13px}.directory-label{display:block;margin-bottom:8px}.directory-list{list-style:none;margin:0;padding:4px;border:1px solid var(--rule);border-radius:8px;background:var(--surface-2);max-height:170px;overflow:auto}.directory-row{display:flex;align-items:center;gap:9px;padding:8px;border-radius:6px}.directory-row+.directory-row{border-top:1px solid var(--rule)}.directory-row svg{width:16px;height:16px;flex:none;fill:none;stroke:var(--muted);stroke-width:1.4;stroke-linejoin:round}.directory-path{flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-family:var(--mono);font-size:12px}.directory-default{font-size:11px;color:var(--accent);background:var(--accent-soft);border-radius:5px;padding:2px 6px}.directory-remove{border:0;background:transparent;color:var(--muted);font-size:18px;cursor:pointer;padding:0 4px}.directory-remove:hover{color:var(--danger)}.directory-remove:focus-visible{outline:2px solid var(--accent);border-radius:4px}.directory-empty{padding:14px;border:1px dashed var(--rule-strong);border-radius:8px;color:var(--muted);font-size:12px}p{font-size:12px;line-height:1.6;color:var(--muted)}footer{display:flex;justify-content:flex-end;gap:8px;margin-top:18px}.error{color:var(--danger)}
</style>
