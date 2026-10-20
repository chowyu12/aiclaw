<script setup lang="ts">
import { reactive, ref, type DeepReadonly } from "vue";
import type { ConfigurationCard } from "../../shared/app-config";
import { actions } from "../store";
import { describeError } from "../errors";
import { t } from "../i18n";
const props = defineProps<{card: DeepReadonly<ConfigurationCard>}>();
const values = reactive<Record<string, string | boolean>>(Object.fromEntries(props.card.fields.map(field => [field.key,field.value])));
const busy = ref(false), error = ref("");
async function submit(cancel = false): Promise<void> {
  if (busy.value) return;
  busy.value = true; error.value = "";
  try { await actions.respondConfiguration({id:props.card.id,cancel,...(!cancel ? {values:{...values}} : {})}); }
  catch (reason) { error.value = describeError(reason); }
  finally { for (const field of props.card.fields) if (field.secret) values[field.key] = ""; busy.value = false; }
}
</script>
<template>
  <section class="configuration-card" role="group" :aria-label="t('应用配置')">
    <header><strong>{{ card.title }}</strong><span>{{ t('等你配置') }}</span></header>
    <p class="note">{{ card.note }}</p>
    <form autocomplete="off" @submit.prevent="submit()">
      <div class="fields">
        <label v-for="field in card.fields" :key="field.key" :class="{wide:field.kind === 'json' || field.key === 'prompt'}">
          <span>{{ field.label }} <small v-if="field.secret && field.isSet">{{ t('已配置，留空保持不变') }}</small></span>
          <input v-if="field.kind === 'boolean'" v-model="values[field.key]" type="checkbox" :disabled="busy" />
          <select v-else-if="field.kind === 'select'" v-model="values[field.key]" :disabled="busy"><option v-for="choice in field.choices" :key="choice" :value="choice">{{ choice }}</option></select>
          <textarea v-else-if="field.kind === 'json' || field.key === 'prompt'" :value="String(values[field.key] ?? '')" @input="values[field.key] = ($event.target as HTMLTextAreaElement).value" :disabled="busy" rows="2" :spellcheck="false" :required="field.required && !field.isSet" />
          <input v-else v-model="values[field.key]" :disabled="busy" :type="field.kind === 'password' ? 'password' : 'text'" :required="field.required && !field.isSet" :autocomplete="field.secret ? 'new-password' : 'off'" />
        </label>
      </div>
      <p v-if="error" class="error" role="alert">{{ error }}</p>
      <footer><button type="button" class="ghost" :disabled="busy" @click="submit(true)">{{ t('取消') }}</button><button type="submit" class="primary" :disabled="busy">{{ busy ? t('正在保存或测试…') : t('确认并执行') }}</button></footer>
    </form>
  </section>
</template>
<style scoped>
.configuration-card { margin:0 0 10px; padding:14px; border:1px solid var(--rule-strong); border-radius:var(--r-md); background:var(--paper,var(--bg)); max-height:55vh; overflow:auto; }
header, footer { display:flex; align-items:center; gap:12px; justify-content:space-between; }
header span, .note, small { color:var(--muted); font-size:12px; }
.note { line-height:1.6; margin:8px 0 12px; }
.fields { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:10px 14px; }
label { display:flex; flex-direction:column; gap:5px; font-size:12px; min-width:0; }
.wide { grid-column:1 / -1; }
input:not([type=checkbox]), select, textarea { width:100%; box-sizing:border-box; border:1px solid var(--rule-strong); border-radius:6px; padding:7px 9px; background:var(--bg); color:var(--ink); font:inherit; }
input[type=checkbox] { align-self:flex-start; }
textarea { resize:vertical; }
footer { justify-content:flex-end; margin-top:12px; }
.error { color:var(--danger,#b42318); font-size:12px; }
@media (max-width:700px) { .fields { grid-template-columns:minmax(0,1fr); } }
</style>
