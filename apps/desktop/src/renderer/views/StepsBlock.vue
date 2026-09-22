<script setup lang="ts">
import type { Turn } from "../turns";

/**
 * 一轮的执行步骤块。
 *
 * 单独成组件是因为它在 ChatView 里要出现在两个位置（跑着的时候在回答上面、
 * 跑完挪到回答下面），同一段标记写两遍迟早会改歪一份。样式跟着搬过来：
 * scoped 样式进不到子组件里，留在 ChatView 的话这里会秃。
 */

defineProps<{
  turn: Turn;
  open: boolean;
  /** 折叠条上的那行字，由 ChatView 按当轮状态算好传进来。 */
  summary: string;
}>();

defineEmits<{ toggle: [] }>();

const STEP_LABEL = { llm: "llm", tool: "工具", notice: "提示" } as const;

/** 耗时按量级换单位：毫秒级的东西写成 0.02 s 读不出快慢。 */
function formatDuration(ms: number | undefined): string {
  if (ms === undefined) return "";
  if (ms < 1000) return `${ms} ms`;
  return `${(ms / 1000).toFixed(2)} s`;
}

/** 超过千的 token 数压成 K：8010 看着像个时间戳。 */
function formatTokens(tokens: number | undefined): string {
  if (!tokens) return "";
  return tokens >= 1000 ? `${(tokens / 1000).toFixed(2)}K tokens` : `${tokens} tokens`;
}

function formatTime(ms: number | undefined): string {
  if (!ms) return "";
  const at = new Date(ms);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${pad(at.getHours())}:${pad(at.getMinutes())}:${pad(at.getSeconds())}`;
}

/** llm 步骤的第二行：R1 · TTFT 1.8s · 思考 526ms · 2 工具调用 */
function llmSubline(step: Turn["steps"][number]): string {
  const parts: string[] = [];
  if (step.round) parts.push(`R${step.round}`);
  if (step.ttftMs) parts.push(`TTFT ${formatDuration(step.ttftMs)}`);
  if (step.thinkMs) parts.push(`思考 ${formatDuration(step.thinkMs)}`);
  if (step.toolCalls) parts.push(`${step.toolCalls} 工具调用`);
  return parts.join(" · ");
}
</script>

<template>
  <div class="steps">
    <button class="steps-head" :class="{ live: !!summary && summary.startsWith('执行中') }" @click="$emit('toggle')">
      <span class="chev" :class="{ open }">›</span>
      {{ summary }}
    </button>

    <div v-if="open" class="steps-body">
      <details v-for="step in turn.steps" :key="step.id" class="step" :data-state="step.state">
        <summary>
          <div class="row">
            <span class="dot" />
            <span class="kind" :data-kind="step.step">{{ STEP_LABEL[step.step] }}</span>
            <span v-if="step.seq" class="seq">{{ step.seq }}</span>
            <span class="name">{{ step.title }}</span>
            <span v-if="step.durationMs !== undefined" class="pill">
              {{ formatDuration(step.durationMs) }}
            </span>
            <span v-if="step.startedAt" class="at">{{ formatTime(step.startedAt) }}</span>
            <span v-if="step.tokens" class="at">{{ formatTokens(step.tokens) }}</span>
          </div>
          <!-- 这一行常驻，不藏在展开里：TTFT 与推理耗时正是「这一轮慢在
               哪儿」的答案，要展开才看得到就等于没给。 -->
          <div v-if="step.step === 'llm' && llmSubline(step)" class="subline">
            {{ llmSubline(step) }}
          </div>
        </summary>
        <pre v-if="step.detail" class="step-detail">{{ step.detail }}</pre>
      </details>
    </div>
  </div>
</template>

<style scoped>
.steps {
  /* 跟着对话正文一起变宽。这个变量定义在 ChatView 的 .chat 上——
     三处各写各的话，窗口一拉大它们就会错开，看着像没对齐。 */
  max-width: var(--content-width, 74ch);
  margin: 0 auto 14px;
  padding: 0 28px;
}

.steps-head {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 3px 10px 3px 6px;
  border: 1px solid var(--rule);
  border-radius: var(--r-full);
  background: var(--surface);
  color: var(--muted);
  font-size: 12px;
  cursor: pointer;
}

.steps-head:hover {
  border-color: var(--rule-strong);
  color: var(--ink-2);
}

.chev {
  display: inline-block;
  transition: transform 0.15s;
  font-size: 13px;
  line-height: 1;
}

.chev.open {
  transform: rotate(90deg);
}

@media (prefers-reduced-motion: reduce) {
  .chev {
    transition: none;
  }
}

.steps-body {
  margin-top: 6px;
  border: 1px solid var(--rule);
  border-radius: var(--r-md);
  background: var(--surface);
  overflow: hidden;
}

.step {
  font-size: 12px;
}

.step + .step {
  border-top: 1px solid var(--rule);
}

.step summary {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 7px 11px;
  cursor: pointer;
  list-style: none;
}

.step summary .row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.step summary::-webkit-details-marker {
  display: none;
}

.step summary:hover {
  background: var(--hover);
}

.dot {
  width: 6px;
  height: 6px;
  flex: 0 0 6px;
  border-radius: 50%;
  background: var(--muted);
}

.step[data-state="running"] .dot {
  background: var(--accent);
  animation: pulse 1.1s ease-in-out infinite;
}

.step[data-state="failed"] .dot {
  background: var(--danger);
}

@keyframes pulse {
  50% {
    opacity: 0.25;
  }
}

@media (prefers-reduced-motion: reduce) {
  .step[data-state="running"] .dot {
    animation: none;
  }
}

/* 类型徽标：模型与工具用不同的颜色，扫一眼就能看出这一轮的时间花在哪一类上。 */
.kind {
  flex: 0 0 auto;
  padding: 1px 7px;
  border-radius: var(--r-sm);
  font-family: var(--mono);
  font-size: 10.5px;
  font-weight: 500;
}

.kind[data-kind="llm"] {
  background: var(--accent-soft);
  color: var(--accent);
}

.kind[data-kind="tool"] {
  background: var(--warn-soft);
  color: var(--warn);
}

.kind[data-kind="notice"] {
  background: var(--surface-2);
  color: var(--muted);
}

.seq {
  flex: 0 0 auto;
  color: var(--muted);
  font-variant-numeric: tabular-nums;
  font-size: 11px;
}

.step .name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: var(--mono);
  color: var(--ink-2);
}

.step[data-state="failed"] .name {
  color: var(--danger);
}

.step[data-kind="notice"] .name,
.step .name:only-child {
  font-family: inherit;
}

/* 耗时用药丸标出来：这是这张清单上最常被扫的一列。 */
.pill {
  flex: 0 0 auto;
  padding: 1px 7px;
  border-radius: var(--r-full);
  background: var(--ok-soft);
  color: var(--ok);
  font-variant-numeric: tabular-nums;
  font-size: 10.5px;
}

.at {
  flex: 0 0 auto;
  color: var(--muted);
  font-variant-numeric: tabular-nums;
  font-size: 10.5px;
}

.subline {
  padding-left: 28px;
  color: var(--muted);
  font-size: 11px;
}

.step-detail {
  margin: 0;
  padding: 9px 11px 11px 39px;
  max-height: 260px;
  overflow: auto;
  background: var(--surface-2);
  font-family: var(--mono);
  font-size: 11px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
}

/* 跑着的那一块把折叠条点亮：它是这一屏里唯一在动的东西。 */
.steps-head.live {
  border-color: var(--accent);
  color: var(--accent);
}
</style>
