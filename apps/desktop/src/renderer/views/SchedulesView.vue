<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { actions, store } from "../store";
import { describeError } from "../errors";
import { t } from "../i18n";
import {
  describeRule,
  formatWhen,
  MIN_INTERVAL_MINUTES,
  validateRule,
  type ScheduleKind,
  type ScheduleRule,
} from "../../shared/schedule";

/**
 * 设置 → 定时任务：到点时 AIClaw 自动开一个新会话，把任务的指令当作你的一条消息发出去。
 *
 * 也可以在对话里直接说「每天 9 点帮我汇总邮件」，模型会用 schedule_create 建一个
 * （建之前请你确认）；建出来的同样列在这里，能改、能停、能删。
 */

type TaskRow = (typeof store.schedules)[number];

const KINDS = computed<{ id: ScheduleKind; label: string }[]>(() => [
  { id: "daily", label: t("每天") },
  { id: "weekdays", label: t("每个工作日") },
  { id: "weekly", label: t("每周几") },
  { id: "interval", label: t("每隔一段时间") },
  { id: "once", label: t("只跑一次") },
]);

/** 周一在前，与日历一致；存的时候 0 = 周日。 */
const WEEK = computed(() => [
  { day: 1, label: t("周一") },
  { day: 2, label: t("周二") },
  { day: 3, label: t("周三") },
  { day: 4, label: t("周四") },
  { day: 5, label: t("周五") },
  { day: 6, label: t("周六") },
  { day: 0, label: t("周日") },
]);

interface Draft {
  id: string;
  name: string;
  prompt: string;
  kind: ScheduleKind;
  time: string;
  days: number[];
  every: number;
  unit: "minutes" | "hours";
  at: string;
  workspace: string;
}

const editing = ref<Draft | null>(null);
const formError = ref("");
const saving = ref(false);
const busy = reactive<Record<string, string>>({});

onMounted(() => void actions.loadSchedules());

const tasks = computed(() => store.schedules);

function blank(): Draft {
  const inAnHour = new Date(Date.now() + 60 * 60_000);
  return {
    id: "",
    name: "",
    prompt: "",
    kind: "daily",
    time: "09:00",
    days: [1],
    every: 1,
    unit: "hours",
    at: localInput(inAnHour),
    workspace: "",
  };
}

function edit(task: TaskRow): void {
  const minutes = task.rule.everyMinutes ?? 60;
  editing.value = {
    id: task.id,
    name: task.name,
    prompt: task.prompt,
    kind: task.rule.kind,
    time: task.rule.time ?? "09:00",
    days: [...(task.rule.days ?? [1])],
    every: minutes % 60 === 0 ? minutes / 60 : minutes,
    unit: minutes % 60 === 0 ? "hours" : "minutes",
    at: task.rule.at ? localInput(new Date(task.rule.at)) : localInput(new Date(Date.now() + 3_600_000)),
    workspace: task.workspace,
  };
  formError.value = "";
}

/** datetime-local 输入框要的「本地时间、不带时区」写法。 */
function localInput(date: Date): string {
  const pad = (value: number) => String(value).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

function ruleOf(draft: Draft): ScheduleRule {
  switch (draft.kind) {
    case "daily":
    case "weekdays":
      return { kind: draft.kind, time: draft.time };
    case "weekly":
      return { kind: "weekly", time: draft.time, days: [...draft.days] };
    case "interval":
      return { kind: "interval", everyMinutes: draft.unit === "hours" ? draft.every * 60 : draft.every };
    case "once":
      return { kind: "once", at: draft.at ? new Date(draft.at).toISOString() : "" };
  }
}

/** 表单下面那行预览：「每个工作日 09:00」。规则不对时显示哪里不对。 */
const preview = computed(() => {
  const draft = editing.value;
  if (!draft) return "";
  const rule = ruleOf(draft);
  return validateRule(rule) || describeRule(rule);
});

function toggleDay(day: number): void {
  const draft = editing.value;
  if (!draft) return;
  draft.days = draft.days.includes(day) ? draft.days.filter((item) => item !== day) : [...draft.days, day];
}

async function pickWorkspace(): Promise<void> {
  const picked = (await window.aiclaw.dialog.pickDirectory()) as string | null;
  if (picked && editing.value) editing.value.workspace = picked;
}

async function save(): Promise<void> {
  const draft = editing.value;
  if (!draft) return;
  formError.value = "";
  saving.value = true;
  try {
    await actions.saveSchedule({
      id: draft.id || undefined,
      name: draft.name,
      prompt: draft.prompt,
      rule: ruleOf(draft),
      workspace: draft.workspace,
    });
    editing.value = null;
  } catch (error) {
    formError.value = describeError(error);
  } finally {
    saving.value = false;
  }
}

async function remove(task: TaskRow): Promise<void> {
  if (!confirm(t("删除定时任务「{name}」？它之前跑出来的会话留着。", { name: task.name }))) return;
  await actions.deleteSchedule(task.id);
}

async function runNow(task: TaskRow): Promise<void> {
  busy[task.id] = t("正在开会话…");
  try {
    const sessionId = await actions.runScheduleNow(task.id);
    busy[task.id] = "";
    await actions.openSession(sessionId);
  } catch (error) {
    busy[task.id] = describeError(error);
  }
}

function nextText(task: TaskRow): string {
  if (!task.enabled) return task.rule.kind === "once" && task.lastRunAt ? t("已跑过") : t("已暂停");
  return task.nextRunAt ? t("下次 {when}", { when: formatWhen(new Date(task.nextRunAt)) }) : t("没有下一次了");
}

function lastText(task: TaskRow): string {
  if (!task.lastRunAt) return t("还没跑过");
  const when = formatWhen(new Date(task.lastRunAt));
  switch (task.lastStatus) {
    case "running":
      return t("{when} 开始，正在跑", { when });
    case "ok":
      return t("{when} 完成", { when });
    case "failed":
      return t("{when} 失败：{error}", { when, error: task.lastError ?? "" });
    case "missed":
      return t("错过了：{error}", { error: task.lastError ?? "" });
    default:
      return when;
  }
}
</script>

<template>
  <div class="page">
    <section>
      <header>
        <h2>{{ t("定时任务") }}</h2>
        <p class="sub">
          {{ t("到点时 AIClaw 自动开一个新会话，把「要做什么」当作你的一条消息发出去——模型、MCP、技能都按那时的设置。") }}
          {{ t("跑出来的会话归在侧边栏的「定时任务」分组里，跑完会发一条系统通知。") }}
        </p>
      </header>

      <p class="note">
        {{ t("也可以在对话里直接说「每个工作日早上 9 点帮我汇总未读邮件」，模型会建一个（建之前请你确认）。") }}
        <strong>{{ t("AIClaw 要开着才会跑") }}</strong>{{ t("；错过 12 小时以内的，下次打开时补跑一次。执行中遇到要确认的操作仍会停下来等你。") }}
      </p>

      <div class="add">
        <button class="ghost" @click="editing = blank(); formError = ''">+ {{ t("新建定时任务") }}</button>
      </div>

      <!-- 新建 / 修改 -->
      <article v-if="editing" class="card editor">
        <label>
          <span>{{ t("名称") }}</span>
          <input v-model="editing.name" :placeholder="t(`比如：每日邮件汇总`)" />
        </label>
        <label>
          <span>{{ t("要做什么") }}</span>
          <textarea
            v-model="editing.prompt"
            rows="4"
            :placeholder="t(`到点时当作你发的一条消息。写清楚做什么、结果怎么给，比如：汇总今天的未读邮件，列出需要我回复的，每封一句话。`)"
          />
        </label>
        <div class="pair">
          <label>
            <span>{{ t("什么时候") }}</span>
            <select v-model="editing.kind">
              <option v-for="kind in KINDS" :key="kind.id" :value="kind.id">{{ kind.label }}</option>
            </select>
          </label>
          <label v-if="editing.kind === 'daily' || editing.kind === 'weekdays' || editing.kind === 'weekly'">
            <span>{{ t("几点") }}</span>
            <input v-model="editing.time" type="time" />
          </label>
          <label v-else-if="editing.kind === 'interval'">
            <span>{{ t("每隔") }}</span>
            <div class="inline">
              <input v-model.number="editing.every" type="number" :min="editing.unit === 'hours' ? 1 : MIN_INTERVAL_MINUTES" />
              <select v-model="editing.unit">
                <option value="minutes">{{ t("分钟") }}</option>
                <option value="hours">{{ t("小时") }}</option>
              </select>
            </div>
          </label>
          <label v-else>
            <span>{{ t("时间") }}</span>
            <input v-model="editing.at" type="datetime-local" />
          </label>
        </div>
        <div v-if="editing.kind === 'weekly'" class="days">
          <button
            v-for="item in WEEK"
            :key="item.day"
            class="day"
            :class="{ on: editing.days.includes(item.day) }"
            @click="toggleDay(item.day)"
          >
            {{ item.label }}
          </button>
        </div>
        <label>
          <span>{{ t("工作区") }}</span>
          <div class="inline">
            <input v-model="editing.workspace" :placeholder="t(`不设：相对路径按主目录解析，写文件前会问你`)" />
            <button class="ghost small" @click="pickWorkspace()">{{ t("选择…") }}</button>
          </div>
        </label>
        <p class="hint" :class="{ bad: !!validateRule(ruleOf(editing)) }">{{ preview }}</p>
        <div class="actions">
          <button class="primary" :disabled="saving" @click="save()">{{ editing.id ? t("保存") : t("建好") }}</button>
          <button class="ghost" @click="editing = null">{{ t("取消") }}</button>
          <span v-if="formError" class="hint bad">{{ formError }}</span>
        </div>
      </article>

      <p v-if="tasks.length === 0 && !editing" class="note">{{ t("还没有定时任务。") }}</p>

      <article v-for="task in tasks" :key="task.id" class="card" :class="{ off: !task.enabled }">
        <div class="card-head">
          <div class="title">
            <span class="name">{{ task.name }}</span>
            <span class="badge">{{ describeRule(task.rule as ScheduleRule) }}</span>
            <span class="state">{{ nextText(task) }}</span>
          </div>
          <label class="toggle">
            <input
              type="checkbox"
              :checked="task.enabled"
              @change="actions.toggleSchedule(task.id, ($event.target as HTMLInputElement).checked)"
            />
            {{ t("启用") }}
          </label>
          <button class="icon" :title="t(`删除`)" @click="remove(task)">×</button>
        </div>
        <p class="prompt">{{ task.prompt }}</p>
        <p class="hint" :class="{ bad: task.lastStatus === 'failed' }">
          {{ t("上次：{text}", { text: lastText(task) }) }}
          <button v-if="task.lastSessionId" class="link" @click="actions.openSession(task.lastSessionId)">{{ t("打开会话") }}</button>
        </p>
        <p v-if="task.workspace" class="hint">{{ t("工作区：") }}<code>{{ task.workspace }}</code></p>
        <div class="actions">
          <button class="ghost small" :disabled="!!busy[task.id] && busy[task.id] === t(`正在开会话…`)" @click="runNow(task)">{{ t("立即运行") }}</button>
          <button class="ghost small" @click="edit(task)">{{ t("修改") }}</button>
          <span v-if="busy[task.id]" class="hint">{{ busy[task.id] }}</span>
        </div>
      </article>
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
  gap: 14px;
  max-width: 660px;
  margin: 0 auto;
}

header {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

h2 {
  margin: 0;
  font-size: 15px;
  font-weight: 650;
}

.sub,
.note {
  margin: 0;
  color: var(--muted);
  font-size: 12px;
  line-height: 1.7;
}

.add,
.actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}

.card {
  display: flex;
  flex-direction: column;
  gap: 9px;
  padding: 14px 16px;
  border: 1px solid var(--rule);
  border-radius: var(--r-lg);
  background: var(--surface);
  box-shadow: var(--shadow-1);
}

.card.off {
  opacity: 0.6;
}

.card.editor {
  gap: 12px;
}

.card-head {
  display: flex;
  align-items: center;
  gap: 10px;
}

.title {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: 8px;
}

.name {
  font-size: 13px;
  font-weight: 500;
}

.badge {
  padding: 2px 8px;
  border-radius: var(--r-full);
  background: var(--surface-2);
  color: var(--ink-2);
  font-size: 10.5px;
  white-space: nowrap;
}

.state {
  color: var(--accent);
  font-size: 11.5px;
  white-space: nowrap;
}

.prompt {
  margin: 0;
  color: var(--ink-2);
  font-size: 12.5px;
  line-height: 1.6;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  display: -webkit-box;
  -webkit-line-clamp: 3;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.hint {
  margin: 0;
  color: var(--muted);
  font-size: 11.5px;
  line-height: 1.6;
  overflow-wrap: anywhere;
}

.hint.bad {
  color: var(--danger);
}

.toggle {
  flex: 0 0 auto;
  display: flex;
  flex-direction: row;
  align-items: center;
  gap: 5px;
  font-size: 12px;
  color: var(--ink-2);
  cursor: pointer;
  white-space: nowrap;
}

.toggle input {
  width: auto;
}

.icon {
  flex: 0 0 auto;
  width: 24px;
  height: 24px;
  border: none;
  border-radius: var(--r-sm);
  background: none;
  color: var(--muted);
  font-size: 15px;
  line-height: 1;
  cursor: pointer;
}

.icon:hover {
  background: var(--danger-soft);
  color: var(--danger);
}

label {
  display: flex;
  flex-direction: column;
  gap: 5px;
  font-size: 13px;
}

label > span {
  color: var(--ink-2);
  font-size: 12px;
  font-weight: 500;
}

textarea {
  resize: vertical;
  font: inherit;
  line-height: 1.6;
}

.pair {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 10px;
}

.inline {
  display: flex;
  gap: 8px;
}

.inline input {
  flex: 1;
  min-width: 0;
}

.inline select,
.inline button {
  flex: 0 0 auto;
}

.days {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.day {
  padding: 4px 10px;
  border: 1px solid var(--rule);
  border-radius: var(--r-full);
  background: transparent;
  color: var(--ink-2);
  font-size: 12px;
  cursor: pointer;
}

.day.on {
  border-color: var(--accent);
  background: var(--accent-soft);
  color: var(--accent);
}

button.small {
  padding: 3px 10px;
  font-size: 11.5px;
}

.link {
  margin-left: 6px;
  border: none;
  background: none;
  color: var(--accent);
  font-size: inherit;
  cursor: pointer;
  text-decoration: underline;
}

@media (max-width: 560px) {
  .pair {
    grid-template-columns: 1fr;
  }
}
</style>
