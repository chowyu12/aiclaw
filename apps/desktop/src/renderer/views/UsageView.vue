<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { actions } from "../store";
import { describeError } from "../errors";
import type { UsageSummaryView } from "../../shared/types";
import { formatCount, formatDuration, sourceLabel } from "../usage-format";

/**
 * 设置 → 用量：花了多少 token、调了多少次模型和工具、哪些技能真在用。
 *
 * 数字来自内核的用量记录（每次打模型、每次调工具一行，见 store/usage.go），
 * 不是从会话存档里现算的：存档里没有 token 数，删了会话也不该让「这个月花了
 * 多少」变少。记录从 3.6 开始，更早的没有。
 */

const PERIODS = [
  { days: 7, label: "7 天" },
  { days: 30, label: "30 天" },
  { days: 90, label: "90 天" },
] as const;

const days = ref<number>(30);
const summary = ref<UsageSummaryView | null>(null);
const loading = ref(false);
const error = ref("");

async function load(): Promise<void> {
  loading.value = true;
  error.value = "";
  try {
    summary.value = (await window.aiclaw.usage.summary(days.value)) as UsageSummaryView;
  } catch (err) {
    error.value = `读不到用量：${describeError(err)}`;
  } finally {
    loading.value = false;
  }
}

function pick(value: number): void {
  if (days.value === value) return;
  days.value = value;
  void load();
}

onMounted(load);

const empty = computed(() => summary.value !== null && summary.value.firstAt === 0);

/** 记录开始得比所选区间晚：说一声，免得把「没记」读成「没用」。 */
const startedLate = computed(() => {
  const value = summary.value;
  if (!value || value.firstAt === 0 || value.firstAt <= value.since) return "";
  const date = new Date(value.firstAt);
  return `用量从 ${date.getMonth() + 1} 月 ${date.getDate()} 日开始记录，更早的没有数据。`;
});

// ---------- 每日柱状图 ----------

const CHART_HEIGHT = 120;
const chart = computed(() => {
  const list = summary.value?.days ?? [];
  const peak = Math.max(1, ...list.map((day) => day.input + day.output));
  const width = 100 / Math.max(1, list.length);
  // 天数多时标签隔几天一个，不然挤成一团。
  const every = list.length > 45 ? 14 : list.length > 14 ? 5 : 1;
  return list.map((day, index) => {
    const input = (day.input / peak) * CHART_HEIGHT;
    const output = (day.output / peak) * CHART_HEIGHT;
    const [, month, date] = day.day.split("-");
    return {
      key: day.day,
      x: index * width,
      width: width * 0.72,
      input,
      output,
      label: index % every === 0 || index === list.length - 1 ? `${Number(month)}/${Number(date)}` : "",
      title: `${day.day}：输入 ${formatCount(day.input)}，输出 ${formatCount(day.output)}，模型 ${day.modelCalls} 次，工具 ${day.toolCalls} 次`,
    };
  });
});

function rate(failed: number, calls: number): string {
  if (calls === 0 || failed === 0) return "—";
  const percent = Math.round((failed / calls) * 100);
  // 有失败但不到 1%：写成 0% 会被读成「没失败」。
  return percent === 0 ? "<1%" : `${percent}%`;
}

async function openSession(id: string): Promise<void> {
  await actions.openSession(id);
  actions.setView("chat");
}
</script>

<template>
  <div class="page">
    <section>
      <header class="top">
        <div>
          <h2>用量</h2>
          <p class="sub">token、模型调用、工具与技能。所有会话都算，包括微信、企业微信的通道会话。</p>
        </div>
        <div class="periods" role="group" aria-label="时间范围">
          <button
            v-for="period in PERIODS"
            :key="period.days"
            :class="{ on: days === period.days }"
            :disabled="loading"
            @click="pick(period.days)"
          >
            {{ period.label }}
          </button>
        </div>
      </header>

      <p v-if="error" class="note warn">{{ error }}</p>
      <p v-else-if="empty" class="note">还没有记录。用量从这个版本开始记，聊过几轮之后这里就有数了。</p>

      <template v-if="summary && !empty">
        <p v-if="startedLate" class="note">{{ startedLate }}</p>

        <div class="cards">
          <div class="card">
            <span class="k">输入 token</span>
            <span class="v">{{ formatCount(summary.totals.input) }}</span>
          </div>
          <div class="card">
            <span class="k">输出 token</span>
            <span class="v">{{ formatCount(summary.totals.output) }}</span>
          </div>
          <div class="card">
            <span class="k">模型调用</span>
            <span class="v">{{ formatCount(summary.totals.modelCalls) }}</span>
            <span v-if="summary.totals.modelFailed" class="bad">失败 {{ summary.totals.modelFailed }}</span>
          </div>
          <div class="card">
            <span class="k">工具调用</span>
            <span class="v">{{ formatCount(summary.totals.toolCalls) }}</span>
            <span v-if="summary.totals.toolFailed" class="bad">失败 {{ summary.totals.toolFailed }}</span>
          </div>
          <div class="card">
            <span class="k">会话</span>
            <span class="v">{{ summary.totals.sessions }}</span>
          </div>
        </div>

        <div class="block">
          <div class="block-head">
            <h3>每天的 token</h3>
            <span class="legend"><i class="in" />输入 <i class="out" />输出</span>
          </div>
          <svg class="chart" :viewBox="`0 0 100 ${CHART_HEIGHT + 14}`" preserveAspectRatio="none" role="img" aria-label="每天的 token 用量">
            <g v-for="bar in chart" :key="bar.key">
              <title>{{ bar.title }}</title>
              <rect :x="bar.x" :y="CHART_HEIGHT - bar.input - bar.output" :width="bar.width" :height="bar.output" class="out" />
              <rect :x="bar.x" :y="CHART_HEIGHT - bar.input" :width="bar.width" :height="bar.input" class="in" />
            </g>
          </svg>
          <div class="axis">
            <span v-for="bar in chart" :key="bar.key" :style="{ width: `${100 / chart.length}%` }">{{ bar.label }}</span>
          </div>
        </div>

        <div class="block">
          <h3>按模型</h3>
          <table v-if="summary.models.length">
            <thead>
              <tr><th>模型</th><th class="n">调用</th><th class="n">输入</th><th class="n">输出</th><th class="n">失败率</th></tr>
            </thead>
            <tbody>
              <tr v-for="row in summary.models" :key="row.model">
                <td class="name">{{ row.model || "（未知）" }}</td>
                <td class="n">{{ formatCount(row.calls) }}</td>
                <td class="n">{{ formatCount(row.input) }}</td>
                <td class="n">{{ formatCount(row.output) }}</td>
                <td class="n" :class="{ bad: row.failed }">{{ rate(row.failed, row.calls) }}</td>
              </tr>
            </tbody>
          </table>
          <p v-else class="none">这段时间没有模型调用。</p>
        </div>

        <div class="block">
          <h3>按工具</h3>
          <table v-if="summary.tools.length">
            <thead>
              <tr><th>工具</th><th>来源</th><th class="n">调用</th><th class="n">失败率</th><th class="n">平均耗时</th></tr>
            </thead>
            <tbody>
              <tr v-for="row in summary.tools" :key="`${row.tool}|${row.source}`">
                <td class="name mono">{{ row.tool }}</td>
                <td class="src">{{ sourceLabel(row.source) }}</td>
                <td class="n">{{ formatCount(row.calls) }}</td>
                <td class="n" :class="{ bad: row.failed }">{{ rate(row.failed, row.calls) }}</td>
                <td class="n">{{ formatDuration(row.avgMs) }}</td>
              </tr>
            </tbody>
          </table>
          <p v-else class="none">这段时间没有工具调用。</p>
        </div>

        <div class="split">
          <div class="block">
            <h3>技能</h3>
            <table v-if="summary.skills.length">
              <tbody>
                <tr v-for="row in summary.skills" :key="row.skill">
                  <td class="name">{{ row.skill }}</td>
                  <td class="n">{{ row.uses }} 次</td>
                </tr>
              </tbody>
            </table>
            <p v-else class="none">这段时间没有取用技能。</p>
          </div>
          <div class="block">
            <h3>最耗 token 的会话</h3>
            <table v-if="summary.sessions.length">
              <tbody>
                <tr v-for="row in summary.sessions" :key="row.sessionId" class="link" @click="openSession(row.sessionId)">
                  <td class="name">{{ row.title || "（已删除的会话）" }}</td>
                  <td class="n">{{ formatCount(row.total) }}</td>
                </tr>
              </tbody>
            </table>
            <p v-else class="none">这段时间没有会话。</p>
          </div>
        </div>
      </template>
    </section>
  </div>
</template>

<style scoped>
.page {
  height: 100%;
  overflow-y: auto;
  padding: 28px 28px 64px;
}

section {
  display: flex;
  flex-direction: column;
  gap: 16px;
  max-width: 760px;
  margin: 0 auto;
}

.top {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

h2 {
  margin: 0;
  font-size: 15px;
  font-weight: 650;
}

h3 {
  margin: 0;
  font-size: 12.5px;
  font-weight: 600;
  color: var(--ink-2);
}

.sub {
  margin: 3px 0 0;
  color: var(--muted);
  font-size: 12px;
  line-height: 1.7;
}

.periods {
  display: flex;
  flex: 0 0 auto;
  border: 1px solid var(--rule);
  border-radius: var(--r-md);
  overflow: hidden;
}

.periods button {
  padding: 4px 12px;
  border: none;
  background: transparent;
  color: var(--ink-2);
  font-size: 12px;
  cursor: pointer;
}

.periods button + button {
  border-left: 1px solid var(--rule);
}

.periods button.on {
  background: var(--active);
  color: var(--ink);
  font-weight: 600;
}

.note {
  margin: 0;
  font-size: 12px;
  color: var(--muted);
  line-height: 1.7;
}

.note.warn {
  color: var(--danger);
}

.cards {
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr));
  gap: 10px;
}

.card {
  display: flex;
  flex-direction: column;
  gap: 3px;
  padding: 11px 13px;
  border: 1px solid var(--rule);
  border-radius: var(--r-lg);
}

.k {
  color: var(--muted);
  font-size: 11.5px;
}

.v {
  color: var(--ink);
  font-size: 19px;
  font-weight: 650;
  font-variant-numeric: tabular-nums;
}

.bad {
  color: var(--danger);
  font-size: 11px;
}

.block {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 13px 15px;
  border: 1px solid var(--rule);
  border-radius: var(--r-lg);
  min-width: 0;
}

.block-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.legend {
  display: flex;
  align-items: center;
  gap: 5px;
  color: var(--muted);
  font-size: 11px;
}

.legend i {
  display: inline-block;
  width: 9px;
  height: 9px;
  border-radius: 2px;
  margin-left: 6px;
}

.chart {
  width: 100%;
  height: 140px;
}

.chart .in,
.legend .in {
  fill: var(--ok);
  background: var(--ok);
}

.chart .out,
.legend .out {
  fill: var(--ok-soft);
  background: var(--ok-soft);
  stroke: var(--ok);
  stroke-width: 0.15;
}

.axis {
  display: flex;
  margin-top: -6px;
  color: var(--muted);
  font-size: 10px;
  white-space: nowrap;
}

.axis span {
  overflow: visible;
}

table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12px;
}

th {
  padding: 4px 6px;
  color: var(--muted);
  font-weight: 500;
  text-align: left;
  border-bottom: 1px solid var(--rule);
}

td {
  padding: 5px 6px;
  border-bottom: 1px solid var(--rule);
  color: var(--ink);
}

tr:last-child td {
  border-bottom: none;
}

.n {
  text-align: right;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.name {
  max-width: 280px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.mono {
  font-family: var(--mono);
  font-size: 11.5px;
}

.src {
  color: var(--muted);
  white-space: nowrap;
}

.link {
  cursor: pointer;
}

.link:hover td {
  background: var(--active);
}

.none {
  margin: 0;
  color: var(--muted);
  font-size: 12px;
}

.split {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: 12px;
}
</style>
