<script setup lang="ts">
import { computed, ref, type DeepReadonly } from "vue";
import type { QuestionAnswer, QuestionPayload } from "../../shared/types";
import { t } from "../i18n";

/**
 * 模型提的一个问题（ask_user）：几个选项，也可以自己写，或者跳过。
 *
 * 放在对话里、输入框上方，而不是弹窗：它是这一轮的一部分，用户要看得见上面
 * 模型做到了哪一步才答得上来；弹窗会把那些挡住。有自己的输入框，不动用户
 * 正在输入框里写的草稿。
 */
// store 导出的是只读对象，这里照收。
const props = defineProps<{ question: DeepReadonly<QuestionPayload> }>();
const emit = defineEmits<{ answer: [answer: QuestionAnswer] }>();

const selected = ref<string[]>([]);
const text = ref("");
const sending = ref(false);

const canSubmit = computed(() => !sending.value && (selected.value.length > 0 || text.value.trim() !== ""));

function toggle(label: string): void {
  if (props.question.multiSelect) {
    selected.value = selected.value.includes(label)
      ? selected.value.filter((item) => item !== label)
      : [...selected.value, label];
    return;
  }
  selected.value = selected.value[0] === label ? [] : [label];
}

function submit(): void {
  if (!canSubmit.value) return;
  sending.value = true;
  emit("answer", { selected: [...selected.value], text: text.value.trim() });
}

function skip(): void {
  if (sending.value) return;
  sending.value = true;
  emit("answer", { skipped: true });
}

function onKeydown(event: KeyboardEvent): void {
  // 回车提交、Shift+回车换行，与主输入框一致。输入法组字时的回车不算。
  if (event.key === "Enter" && !event.shiftKey && !event.isComposing) {
    event.preventDefault();
    submit();
  }
}
</script>

<template>
  <div class="question" role="group" :aria-label="t(`AIClaw 的提问`)">
    <div class="head">
      <span class="badge">{{ t("等你回答") }}</span>
      <span class="hint">{{ question.multiSelect ? t("可以选多个，也可以自己写") : t("选一个，也可以自己写") }}</span>
    </div>
    <p class="text">{{ question.question }}</p>
    <div v-if="question.options.length > 0" class="options">
      <button
        v-for="(option, index) in question.options"
        :key="option.label"
        type="button"
        class="option"
        :class="{ on: selected.includes(option.label), multi: question.multiSelect }"
        :aria-pressed="selected.includes(option.label)"
        @click="toggle(option.label)"
      >
        <span class="mark" aria-hidden="true">{{ index + 1 }}</span>
        <span class="body">
          <span class="label">{{ option.label }}</span>
          <span v-if="option.description" class="desc">{{ option.description }}</span>
        </span>
      </button>
    </div>
    <textarea
      v-model="text"
      class="other"
      rows="1"
      :placeholder="question.options.length > 0 ? t(`其他（可选）：直接写你的回答`) : t(`写下你的回答`)"
      @keydown="onKeydown"
    />
    <div class="actions">
      <button type="button" class="skip" :disabled="sending" @click="skip">{{ t("跳过") }}</button>
      <button type="button" class="send" :disabled="!canSubmit" @click="submit">
        {{ sending ? t("已提交") : t("提交") }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.question {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin: 0 0 10px;
  padding: 12px 14px;
  border: 1px solid var(--rule-strong);
  border-left: 3px solid var(--ok);
  border-radius: var(--r-md);
  background: var(--paper, var(--bg));
}

.head {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 11.5px;
}

.badge {
  padding: 1px 8px;
  border-radius: var(--r-full);
  background: var(--ok-soft);
  color: var(--ok);
  font-weight: 500;
}

.hint {
  color: var(--muted);
}

.text {
  margin: 0;
  color: var(--ink);
  font-size: 13.5px;
  line-height: 1.6;
  white-space: pre-wrap;
}

.options {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.option {
  display: flex;
  align-items: flex-start;
  gap: 9px;
  padding: 7px 10px;
  border: 1px solid var(--rule);
  border-radius: var(--r-md);
  background: transparent;
  color: var(--ink);
  text-align: left;
  cursor: pointer;
}

.option:hover {
  border-color: var(--rule-strong);
  background: var(--active);
}

.option.on {
  border-color: var(--ok);
  background: var(--ok-soft);
}

.mark {
  flex: 0 0 auto;
  width: 18px;
  height: 18px;
  border: 1px solid var(--rule-strong);
  border-radius: 50%;
  color: var(--muted);
  font-size: 10.5px;
  line-height: 16px;
  text-align: center;
}

.option.multi .mark {
  border-radius: 4px;
}

.option.on .mark {
  border-color: var(--ok);
  background: var(--ok);
  color: #fff;
}

.body {
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
}

.label {
  font-size: 13px;
}

.desc {
  color: var(--muted);
  font-size: 11.5px;
  line-height: 1.5;
}

.other {
  box-sizing: border-box;
  width: 100%;
  min-height: 32px;
  padding: 6px 9px;
  border: 1px solid var(--rule);
  border-radius: var(--r-md);
  background: transparent;
  color: var(--ink);
  font: inherit;
  font-size: 12.5px;
  resize: vertical;
}

.actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

.actions button {
  padding: 4px 14px;
  border-radius: var(--r-md);
  font-size: 12px;
  cursor: pointer;
}

.skip {
  border: 1px solid var(--rule);
  background: transparent;
  color: var(--ink-2);
}

.send {
  border: 1px solid var(--ok);
  background: var(--ok);
  color: #fff;
}

.send:disabled,
.skip:disabled {
  opacity: 0.5;
  cursor: default;
}
</style>
