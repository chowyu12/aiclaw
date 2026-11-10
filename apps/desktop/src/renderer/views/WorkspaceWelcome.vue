<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { store } from "../store";
import { locale, t } from "../i18n";
import { describeError } from "../errors";
import { activityCalendar, calendarDay, sessionCreationDays } from "../activity-calendar";
import { formatCount } from "../usage-format";
import type { UsageSummaryView } from "../../shared/types";
import BrandLogo from "./BrandLogo.vue";

defineProps<{ workspace: string; model: string }>();
const emit = defineEmits<{ workspace: []; model: [] }>();
const metric = ref<"sessions" | "usage">("sessions");
const usage = ref<UsageSummaryView | null>(null), loading = ref(false), error = ref("");
async function load(): Promise<void> {
  if (loading.value) return;
  loading.value = true; error.value = "";
  try { usage.value = await window.aiclaw.usage.summary(365) as UsageSummaryView; }
  catch (reason) { error.value = describeError(reason); }
  finally { loading.value = false; }
}
watch(metric, value => { if (value === "usage" && !usage.value) void load(); });
const calendar = computed(() => metric.value === "sessions"
  ? activityCalendar(sessionCreationDays(store.sessions))
  : activityCalendar((usage.value?.days ?? []).map(day => ({ day: day.day, value: day.input + day.output })), new Date(), usage.value?.firstAt ? calendarDay(new Date(usage.value.firstAt)) : null));
const summary = computed(() => metric.value === "usage" && (loading.value || !usage.value || !usage.value.firstAt) ? t(loading.value ? "读取中…" : "还没有用量记录") : metric.value === "sessions"
  ? t("当前列表内，近一年创建 {n} 个会话", { n: calendar.value.total })
  : t("近一年已记录 {n} tokens", { n: formatCount(calendar.value.total) }));
function month(date: Date): string { return date.toLocaleDateString(locale.value === "en" ? "en-US" : "zh-CN", { month: "short" }); }
function title(day: string, value: number | null): string {
  if (value === null) return `${day} · ${t('无用量记录')}`;
  return metric.value === "sessions" ? t("{day}：创建 {n} 个会话", { day, n: value }) : `${day} · ${formatCount(value)} tokens`;
}
</script>
<template>
  <section class="workspace-welcome" :aria-label="t('工作台')">
    <div class="welcome-mark"><BrandLogo compact /><span>AIClaw</span></div>
    <h1>{{ t('今天想完成什么？') }}</h1>
    <p class="welcome-context"><span>{{ t('在') }}</span><button :title="t('选择工作区')" @click="emit('workspace')">{{ workspace }}<span>⌄</span></button><span>{{ t('使用') }}</span><button :title="t('选择模型')" @click="emit('model')">{{ model || t('选择模型') }}<span>⌄</span></button></p>
    <div class="activity-card">
      <div class="activity-heading"><div class="activity-tabs" role="tablist" :aria-label="t('活动记录')"><button role="tab" :aria-selected="metric === 'sessions'" aria-controls="welcome-activity" @click="metric = 'sessions'">{{ t('会话') }}</button><button role="tab" :aria-selected="metric === 'usage'" aria-controls="welcome-activity" @click="metric = 'usage'">{{ t('用量') }}</button></div><span class="activity-total">{{ loading ? t('读取中…') : error && metric === 'usage' ? '—' : summary }}</span></div>
      <div id="welcome-activity" class="activity-calendar" role="tabpanel" :aria-label="summary" :aria-busy="loading" :style="{ '--weeks': calendar.weeks }">
        <div class="calendar-cells" role="img" :aria-label="summary"><span v-for="cell in calendar.cells" :key="cell.day" class="calendar-day" :data-level="cell.value === null ? 'unknown' : cell.level" :class="{ outside: !cell.inRange }" :title="cell.inRange ? title(cell.day, cell.value) : undefined" /></div>
        <div class="calendar-months" aria-hidden="true"><span v-for="label in calendar.months" :key="label.column" :style="{ gridColumn: `${label.column}` }">{{ month(label.date) }}</span></div>
      </div>
      <p v-if="metric === 'usage' && error" class="activity-note bad" role="alert">{{ t('读取用量失败：{error}', { error }) }} <button @click="load">{{ t('重试') }}</button></p>
      <p v-else class="activity-note">{{ metric === 'sessions' ? t('基于当前会话列表的创建时间') : usage?.firstAt ? t('显示已记录的模型 token 用量') : t('还没有用量记录') }}</p>
    </div>
  </section>
</template>
<style scoped>
.workspace-welcome { width: var(--content-width, min(720px, calc(100% - 48px))); margin: auto; padding: 32px 0; text-align: center; }
.welcome-mark { display: inline-flex; align-items: center; gap: 12px; margin-bottom: 24px; padding: 12px 18px; border: 1px solid var(--chrome-rule); border-radius: 13px; color: var(--muted); font-size: 14px; letter-spacing: .1em; background: var(--surface); box-shadow: var(--shadow-1); }
.welcome-mark :deep(.mark) { width: 38px; height: 38px; }
.workspace-welcome h1 { margin: 0 0 9px; font-size: 23px; font-weight: 550; letter-spacing: -.5px; }
.welcome-context { display: flex; align-items: center; justify-content: center; gap: 8px; flex-wrap: wrap; margin: 0; color: var(--muted); font-size: 14px; }
.welcome-context button { display: inline-flex; align-items: center; gap: 8px; max-width: 220px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; padding: 0 1px; border: 0; border-bottom: 1px solid var(--rule); background: none; color: var(--ink-2); cursor: pointer; font-size: inherit; }
.welcome-context button:hover { color: var(--accent); }
.welcome-context button span { font-size: 11px; color: var(--muted); }
.activity-card { margin-top: 26px; padding: 16px 17px 10px; border: 1px solid var(--rule); border-radius: 9px; text-align: left; background: var(--surface); }
.activity-heading { display: flex; align-items: center; justify-content: space-between; gap: 10px; margin-bottom: 15px; }
.activity-tabs { display: flex; gap: 14px; flex: none; }
.activity-tabs button { padding: 0; border: 0; background: none; color: var(--muted); font-size: 12px; cursor: pointer; }
.activity-tabs button[aria-selected=true] { color: var(--ink); }
.activity-total { color: var(--muted); font-size: 10px; text-align: right; }
.activity-calendar { min-width: 0; }
.calendar-cells { display: grid; grid-template-rows: repeat(7, auto); grid-auto-flow: column; grid-template-columns: repeat(var(--weeks), minmax(0, 1fr)); gap: 3px; }
.calendar-day { display: block; aspect-ratio: 1; border-radius: 2px; background: var(--hover); }
.calendar-day[data-level='unknown'] { background: var(--hover); opacity: .45; }
.calendar-day[data-level='1'] { background: color-mix(in srgb, var(--ok) 18%, var(--surface)); }
.calendar-day[data-level='2'] { background: color-mix(in srgb, var(--ok) 35%, var(--surface)); }
.calendar-day[data-level='3'] { background: color-mix(in srgb, var(--ok) 58%, var(--surface)); }
.calendar-day[data-level='4'] { background: var(--ok); }
.calendar-day.outside { visibility: hidden; }
.calendar-months { display: grid; grid-template-columns: repeat(var(--weeks), minmax(0, 1fr)); margin-top: 7px; color: var(--muted); font-size: 9px; white-space: nowrap; }
.activity-note { margin: 8px 0 0; color: var(--muted); font-size: 10px; }
.activity-note.bad { color: var(--danger); }
.activity-note button { border: 0; background: transparent; color: inherit; text-decoration: underline; cursor: pointer; }
@media (max-height: 660px) { .workspace-welcome { padding: 14px 0; } .welcome-mark { margin-bottom: 12px; padding: 7px 14px; } .activity-card { margin-top: 16px; } }
</style>
