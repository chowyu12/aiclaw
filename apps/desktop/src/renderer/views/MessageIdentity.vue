<script setup lang="ts">
import { computed } from "vue";
import BrandLogo from "./BrandLogo.vue";
import { t } from "../i18n";
import { formatMessageTime, fullMessageTime } from "../message-meta";
import type { Turn } from "../turns";
const props = defineProps<{ user?: boolean; at?: number; steps?: Turn['steps'] }>();
const tools = computed(() => props.steps?.filter(step => step.step === 'tool').length ?? 0);
const models = computed(() => props.steps?.filter(step => step.step === 'llm').length ?? 0);
const failed = computed(() => props.steps?.some(step => step.state === 'failed'));
</script>

<template>
  <div class="message-identity">
    <span class="message-avatar" aria-hidden="true">
      <svg v-if="user" viewBox="0 0 24 24"><circle cx="12" cy="8" r="4"/><path d="M4 21v-3a8 6 0 0 1 16 0v3Z"/></svg>
      <BrandLogo v-else compact />
    </span>
    <strong>{{ user ? t('你') : 'AIClaw' }}</strong>
    <time v-if="at" :title="fullMessageTime(at)">{{ formatMessageTime(at) }}</time>
  </div>
  <div v-if="steps" class="message-badges">
    <span class="message-badge generated">✓ {{ t('回答已生成') }}</span>
    <span v-if="tools" class="message-badge tools">⌘ {{ t('{n} 工具调用', { n: tools }) }}</span>
    <span v-if="models" class="message-badge models">{{ t('{n} 次模型调用', { n: models }) }}</span>
    <span v-if="failed" class="message-badge failed">! {{ t('有失败步骤') }}</span>
  </div>
</template>
