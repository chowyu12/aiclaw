/**
 * 定时任务的时间规则：给定规则与「从哪个时刻往后算」，求下一次该跑的时刻。
 *
 * 主进程（调度）与渲染层（「下次运行」那一栏）用同一份，所以放在 shared 里、
 * 不碰 Node 也不碰 DOM。时间一律按本机时区——用户说「每天早上 9 点」指的是
 * 他墙上的钟，不是 UTC。
 */

export type ScheduleKind = "daily" | "weekdays" | "weekly" | "interval" | "once";

export interface ScheduleRule {
  kind: ScheduleKind;
  /** daily / weekdays / weekly：几点，"HH:MM"。 */
  time?: string;
  /** weekly：星期几，0 = 周日 … 6 = 周六。 */
  days?: number[];
  /** interval：每隔多少分钟。 */
  everyMinutes?: number;
  /** once：什么时候，ISO 时间。 */
  at?: string;
}

/** 间隔最短 5 分钟：再短就是轮询了，模型每跑一次都要花钱。 */
export const MIN_INTERVAL_MINUTES = 5;

const WEEKDAY_NAMES = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"];

/** 解析 "HH:MM"。不合法返回 null。 */
export function parseTime(value: string | undefined): { hour: number; minute: number } | null {
  const match = /^(\d{1,2}):(\d{2})$/.exec((value ?? "").trim());
  if (!match) return null;
  const hour = Number(match[1]);
  const minute = Number(match[2]);
  if (hour > 23 || minute > 59) return null;
  return { hour, minute };
}

/** 规则哪里不对；没问题返回空串。 */
export function validateRule(rule: ScheduleRule): string {
  switch (rule.kind) {
    case "daily":
    case "weekdays":
      return parseTime(rule.time) ? "" : "时间要写成 09:00 这样";
    case "weekly":
      if (!parseTime(rule.time)) return "时间要写成 09:00 这样";
      if (!rule.days || rule.days.length === 0) return "至少选一天";
      return rule.days.every((day) => Number.isInteger(day) && day >= 0 && day <= 6) ? "" : "星期几不对";
    case "interval":
      if (!Number.isFinite(rule.everyMinutes) || (rule.everyMinutes ?? 0) < MIN_INTERVAL_MINUTES) {
        return `间隔至少 ${MIN_INTERVAL_MINUTES} 分钟`;
      }
      return "";
    case "once":
      return rule.at && !Number.isNaN(Date.parse(rule.at)) ? "" : "要给一个具体的时间";
    default:
      return "不认识的规则";
  }
}

/**
 * after 之后（不含 after 本身）下一次该跑的时刻。once 已经过了、规则不合法时返回 null。
 *
 * interval 以 anchor（任务建立或上次运行的时刻）为起点每隔 N 分钟一次，而不是对齐
 * 整点：「每 30 分钟」从用户点下保存那一刻算起才符合直觉。
 */
export function nextRun(rule: ScheduleRule, after: Date, anchor?: Date): Date | null {
  if (validateRule(rule)) return null;
  if (rule.kind === "once") {
    const at = new Date(rule.at!);
    return at.getTime() > after.getTime() ? at : null;
  }
  if (rule.kind === "interval") {
    const step = rule.everyMinutes! * 60_000;
    const start = (anchor ?? after).getTime();
    if (start > after.getTime()) return new Date(start);
    const passed = Math.floor((after.getTime() - start) / step) + 1;
    return new Date(start + passed * step);
  }
  const { hour, minute } = parseTime(rule.time)!;
  const allowed = (day: number) => {
    if (rule.kind === "weekdays") return day >= 1 && day <= 5;
    if (rule.kind === "weekly") return rule.days!.includes(day);
    return true;
  };
  // 最多往后看 8 天：每周规则至少有一天，8 天里一定碰得到。
  for (let offset = 0; offset <= 8; offset++) {
    const candidate = new Date(after.getFullYear(), after.getMonth(), after.getDate() + offset, hour, minute, 0, 0);
    if (candidate.getTime() > after.getTime() && allowed(candidate.getDay())) return candidate;
  }
  return null;
}

/** 规则写成人话：「每个工作日 09:00」「每 30 分钟」。 */
export function describeRule(rule: ScheduleRule): string {
  switch (rule.kind) {
    case "daily":
      return `每天 ${rule.time}`;
    case "weekdays":
      return `每个工作日 ${rule.time}`;
    case "weekly": {
      const days = [...(rule.days ?? [])].sort((a, b) => ((a + 6) % 7) - ((b + 6) % 7));
      return `每${days.map((day) => WEEKDAY_NAMES[day]).join("、")} ${rule.time}`;
    }
    case "interval": {
      const minutes = rule.everyMinutes ?? 0;
      if (minutes % 60 === 0) return `每 ${minutes / 60} 小时`;
      return `每 ${minutes} 分钟`;
    }
    case "once":
      return rule.at ? `${formatWhen(new Date(rule.at))}（一次）` : "一次";
    default:
      return "";
  }
}

/** 「今天 09:00」「明天 09:00」「10月3日 09:00」。 */
export function formatWhen(when: Date, now = new Date()): string {
  const time = `${String(when.getHours()).padStart(2, "0")}:${String(when.getMinutes()).padStart(2, "0")}`;
  const day = (date: Date) => new Date(date.getFullYear(), date.getMonth(), date.getDate()).getTime();
  const diff = Math.round((day(when) - day(now)) / 86_400_000);
  if (diff === 0) return `今天 ${time}`;
  if (diff === 1) return `明天 ${time}`;
  if (diff === -1) return `昨天 ${time}`;
  const date = `${when.getMonth() + 1}月${when.getDate()}日`;
  return when.getFullYear() === now.getFullYear() ? `${date} ${time}` : `${when.getFullYear()}年${date} ${time}`;
}

/**
 * 现在该不该跑。from 是「从哪儿往后算」：任务建立的时刻，或者上一次运行的时刻。
 *
 * - due：到点了，或者错过了 12 小时以内（应用没开、电脑睡着）——补跑一次。
 * - missed：错过太久了，不补；调用方把 from 挪到现在，从下一个点重新算。
 *   开机时一口气补跑一周的日报没有意义，「昨晚 11 点的备份」开机补一次却是用户想要的。
 * - wait：还没到。
 */
export function dueState(rule: ScheduleRule, from: Date, now: Date): "due" | "missed" | "wait" {
  const next = nextRun(rule, from, rule.kind === "interval" ? from : undefined);
  if (!next || next.getTime() > now.getTime()) return "wait";
  return now.getTime() - next.getTime() <= 12 * 60 * 60_000 ? "due" : "missed";
}

/** 一个定时任务。存在 userData/schedules.json。 */
export interface ScheduledTask {
  id: string;
  name: string;
  /** 到点时当作用户消息发出去的那段话。 */
  prompt: string;
  rule: ScheduleRule;
  enabled: boolean;
  /** 在哪个工作区跑；空表示不设。 */
  workspace: string;
  createdAt: string;
  /** 下一次从哪个时刻往后算：建立、上次运行、或者错过太久被挪到的那一刻。 */
  anchorAt: string;
  lastRunAt?: string;
  lastStatus?: "running" | "ok" | "failed" | "missed";
  lastError?: string;
  /** 上次运行开的那个会话，界面上点它就打开。 */
  lastSessionId?: string;
}

/** 界面上显示的一行：任务本身加算好的「下次运行」。 */
export interface ScheduledTaskView extends ScheduledTask {
  nextRunAt: string | null;
  describe: string;
}

/** 新建或修改时界面交过来的。id 为空表示新建。 */
export interface ScheduledTaskInput {
  id?: string;
  name: string;
  prompt: string;
  rule: ScheduleRule;
  enabled?: boolean;
  workspace?: string;
}

export function toView(task: ScheduledTask, now = new Date()): ScheduledTaskView {
  const from = new Date(task.anchorAt);
  const next = task.enabled ? nextRun(task.rule, from > now ? from : now, task.rule.kind === "interval" ? from : undefined) : null;
  return { ...task, nextRunAt: next ? next.toISOString() : null, describe: describeRule(task.rule) };
}
