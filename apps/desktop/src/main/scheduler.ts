import { EventEmitter } from "node:events";
import { existsSync, readFileSync, writeFileSync, renameSync } from "node:fs";
import { randomBytes } from "node:crypto";
import { join } from "node:path";
import { tr } from "../shared/i18n.js";
import {
  describeRule,
  dueState,
  formatWhen,
  toView,
  validateRule,
  type ScheduledTask,
  type ScheduledTaskInput,
  type ScheduledTaskView,
  type ScheduleRule,
} from "../shared/schedule.js";

/**
 * 定时任务的调度器。
 *
 * 跑在主进程里：到点时用**当前的**应用配置开一个新会话，把任务的提示词当作用户消息
 * 发出去——模型、MCP、技能、审批档位都按那时的设置，任务里不存一份会过期的配置。
 * 应用没开着时不会跑；错过 12 小时以内的，下次打开时补跑一次（见 dueState）。
 *
 * 每 30 秒看一眼有没有到点的，而不是给每个任务算好 setTimeout：电脑睡一觉醒来，
 * 定时器的时间就不准了，而轮询天然会在醒来后的第一次检查里补上。
 *
 * 执行中遇到要确认的操作照常弹审批——用户不在的话它就等着，不会自己放行。
 */

export interface SchedulerHost {
  /** 开一个后台会话并发出第一条消息，返回会话 id。不切换用户正在看的会话。 */
  runTask(task: ScheduledTask): Promise<string | { sessionId: string; turnId: string }>;
  /** 任务跑完（或失败）时通知用户。 */
  notify(task: ScheduledTask, ok: boolean, detail: string): void;
}

const TICK_MS = 30_000;

export class Scheduler extends EventEmitter {
  private timer: ReturnType<typeof setInterval> | undefined;
  private running = new Set<string>();
  private reports = new Map<string, {result: string; resultKey: string; complete: boolean}>();
  private earlyCompletions = new Map<string, {sessionId: string; error?: string}>();
  private startup: ReturnType<typeof setTimeout> | undefined;

  private readonly dir: string;
  private readonly host: SchedulerHost;

  constructor(dir: string, host: SchedulerHost) {
    super();
    this.dir = dir;
    this.host = host;
    const tasks = this.read();
    let recovered = false;
    for (const task of tasks) if (task.lastStatus === "running") {
      task.enabled = false;
      task.lastStatus = "failed";
      task.lastError = tr("上次执行结果不确定，请检查原会话后再启用");
      recovered = true;
    }
    if (recovered) this.write(tasks);
  }

  private get path(): string {
    return join(this.dir, "schedules.json");
  }

  private read(): ScheduledTask[] {
    if (!existsSync(this.path)) return [];
    try {
      const raw = JSON.parse(readFileSync(this.path, "utf8")) as unknown;
      return Array.isArray(raw) ? (raw as ScheduledTask[]) : [];
    } catch {
      return [];
    }
  }

  private write(tasks: ScheduledTask[]): void {
    writeFileSync(this.path + ".tmp", `${JSON.stringify(tasks, null, 2)}\n`, "utf8");
    renameSync(this.path + ".tmp", this.path);
    this.emit("changed", this.list());
  }

  private update(id: string, change: (task: ScheduledTask) => void): ScheduledTask | undefined {
    const tasks = this.read();
    const task = tasks.find((item) => item.id === id);
    if (!task) return undefined;
    change(task);
    this.write(tasks);
    return task;
  }

  list(): ScheduledTaskView[] {
    const now = new Date();
    return this.read()
      .map((task) => toView(task, now))
      .sort((a, b) => (a.nextRunAt ?? "9").localeCompare(b.nextRunAt ?? "9"));
  }

  /** 新建或修改。改了时间规则就从现在重新算，不按旧的锚点补跑。 */
  save(input: ScheduledTaskInput): ScheduledTaskView {
    const name = input.name.trim();
    const prompt = input.prompt.trim();
    if (!name) throw new Error(tr("给任务起个名字"));
    if (!prompt) throw new Error(tr("写上到点时要做什么"));
    const problem = validateRule(input.rule);
    if (problem) throw new Error(problem);
    const rule = normalizeRule(input.rule);
    const now = new Date().toISOString();
    const tasks = this.read();
    let task = input.id ? tasks.find((item) => item.id === input.id) : undefined;
    const mode = input.mode ?? task?.mode ?? "cron";
    if (mode !== "cron" && mode !== "followup") throw new Error(tr("不认识的任务方式"));
    const sessionId = (input.sessionId ?? task?.sessionId ?? "").trim();
    if (mode === "followup" && !sessionId) throw new Error(tr("请选择跟进的会话"));
    if (task && this.running.has(task.id)) throw new Error(tr("任务执行中，请结束后再修改"));
    const notificationPolicy = mode === "followup" ? input.notificationPolicy ?? task?.notificationPolicy ?? "changes" : "all";
    if (notificationPolicy !== "changes" && notificationPolicy !== "all") throw new Error(tr("通知方式无效"));
    if (task) {
      const ruleChanged = JSON.stringify(task.rule) !== JSON.stringify(rule);
      task.name = name;
      task.prompt = prompt;
      task.rule = rule;
      task.workspace = (input.workspace ?? task.workspace).trim();
      if (input.enabled !== undefined) task.enabled = input.enabled;
      if (ruleChanged) task.anchorAt = now;
    } else {
      task = {
        id: `t_${randomBytes(4).toString("hex")}`,
        name,
        prompt,
        rule,
        enabled: input.enabled ?? true,
        workspace: (input.workspace ?? "").trim(),
        createdAt: now,
        anchorAt: now,
      };
      tasks.push(task);
    }
    if (task.mode !== mode || task.sessionId !== (mode === "followup" ? sessionId : undefined)) { task.lastResult = undefined; task.lastResultKey = undefined; }
    task.mode = mode;
    task.sessionId = mode === "followup" ? sessionId : undefined;
    task.notificationPolicy = notificationPolicy;
    task.stopWhen = (input.stopWhen ?? task.stopWhen ?? "").trim();
    this.write(tasks);
    return toView(task);
  }

  remove(id: string): boolean {
    const tasks = this.read();
    const kept = tasks.filter((task) => task.id !== id);
    if (kept.length === tasks.length) return false;
    this.write(kept);
    return true;
  }

  /** 开关。重新打开时从现在算起：关着的那几天不补。 */
  setEnabled(id: string, enabled: boolean): void {
    this.update(id, (task) => {
      if (enabled && !task.enabled) task.anchorAt = new Date().toISOString();
      task.enabled = enabled;
      if (enabled) task.stoppedReason = undefined;
    });
  }

  /** 立刻跑一次，不影响原来的排期。 */
  async runNow(id: string): Promise<string> {
    const task = this.read().find((item) => item.id === id);
    if (!task) throw new Error(tr("没有这个任务"));
    return this.fire(task, false);
  }

  start(): void {
    if (this.timer) return;
    // 开机后稍等一会儿再查：内核刚起来，马上开会话容易撞上「运行时还没就绪」。
    this.startup = setTimeout(() => void this.tick(), 8_000);
    this.timer = setInterval(() => void this.tick(), TICK_MS);
  }

  stop(): void {
    clearTimeout(this.startup);
    clearInterval(this.timer);
    this.timer = undefined;
  }

  async tick(now = new Date()): Promise<void> {
    for (const task of this.read()) {
      if (!task.enabled || this.running.has(task.id)) continue;
      const state = dueState(task.rule, new Date(task.anchorAt), now);
      if (state === "missed") {
        this.update(task.id, (item) => {
          item.anchorAt = now.toISOString();
          item.lastStatus = "missed";
          item.lastError = tr("应用没开着，错过了太久，没有补跑");
        });
      } else if (state === "due") {
        await this.fire(task, true).catch(() => undefined);
      }
    }
  }

  private async fire(task: ScheduledTask, scheduled: boolean): Promise<string> {
    if (this.running.has(task.id)) throw new Error(tr("任务已经在执行"));
    if (task.mode === "followup" && this.read().some(item => item.id !== task.id && item.lastStatus === "running" && item.lastSessionId === task.sessionId)) throw new Error(tr("原会话已有跟进任务在执行"));
    this.running.add(task.id);
    task.lastRunId = `run_${randomBytes(12).toString("hex")}`;
    const startedAt = new Date().toISOString();
    this.update(task.id, item => {
      item.lastStatus = "running"; item.lastRunId = task.lastRunId; item.lastTurnId = undefined;
      item.lastSessionId = task.mode === "followup" ? task.sessionId : undefined;
      item.lastError = undefined; item.lastRunAt = startedAt;
    });
    try {
      const receipt = await this.host.runTask(task);
      const sessionId = typeof receipt === "string" ? receipt : receipt.sessionId;
      const turnId = typeof receipt === "string" ? undefined : receipt.turnId;
      this.update(task.id, (item) => {
        item.lastRunAt = startedAt;
        item.lastStatus = "running";
        item.lastError = undefined;
        item.lastSessionId = sessionId;
        item.lastTurnId = turnId;
        if (scheduled) {
          item.anchorAt = startedAt;
          // 一次性的跑过就关掉，留在列表里让用户看得到结果。
          if (item.rule.kind === "once") item.enabled = false;
        }
      });
      if (turnId) {
        const completed = this.earlyCompletions.get(turnId);
        this.earlyCompletions.delete(turnId);
        if (completed) this.onTurnCompleted(sessionId, completed.error, turnId);
      }
      return sessionId;
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      this.update(task.id, (item) => {
        item.lastRunAt = startedAt;
        item.lastStatus = "failed";
        item.lastError = message;
        if (item.mode === "followup") item.enabled = false;
        if (scheduled) item.anchorAt = startedAt;
      });
      this.host.notify(task, false, message);
      this.running.delete(task.id);
      this.reports.delete(task.id);
      throw error;
    }
  }

  /** 会话的一轮结束了：是某个任务开的那个，就记下结果并通知。 */
  bindRunSession(id: string, runId: string, sessionId: string): void {
    this.update(id, task => { if (task.lastRunId === runId && task.lastStatus === "running") task.lastSessionId = sessionId; });
  }

  onTurnCompleted(sessionId: string, error: string | undefined, turnId?: string): void {
    const task = this.read().find(item => item.lastSessionId === sessionId && item.lastStatus === "running" && (!item.lastTurnId || item.lastTurnId === turnId));
    if (!task) return;
    if (turnId && !task.lastTurnId) { this.earlyCompletions.set(turnId, {sessionId, error}); return; }
    this.running.delete(task.id);
    const report = this.reports.get(task.id);
    this.reports.delete(task.id);
    const failed = error || (task.mode === "followup" && !report ? tr("跟进没有返回检查结果，请检查会话") : undefined);
    const changed = !!report && report.resultKey !== task.lastResultKey;
    const complete = !failed && !!report?.complete && !!task.stopWhen;
    this.update(task.id, item => {
      item.lastStatus = failed ? "failed" : "ok";
      item.lastError = failed;
      if (failed && item.mode === "followup") item.enabled = false;
      if (report && !failed) { item.lastResult = report.result; item.lastResultKey = report.resultKey; }
      if (complete) { item.enabled = false; item.stoppedReason = task.stopWhen; }
    });
    if (failed || complete || task.notificationPolicy !== "changes" || changed) this.host.notify(task, !failed, failed ?? report?.result ?? "");
  }

  /** 模型经 schedule_* 工具发来的请求。回一段给模型看的话；它也显示在界面的步骤详情里，跟界面语言走。 */
  handleModelRequest(request: {
    action: string;
    sessionId?: string;
    id?: string;
    runId?: string;
    result?: string;
    resultKey?: string;
    complete?: boolean;
    task?: { name: string; prompt: string; kind: string; time?: string; days?: number[]; everyMinutes?: number; at?: string; workspace?: string; mode?: "cron" | "followup"; notificationPolicy?: "changes" | "all"; stopWhen?: string };
  }): string {
    switch (request.action) {
      case "report": {
        const task = this.read().find(item => item.id === request.id);
        if (!task || !this.running.has(task.id) || task.lastRunId !== request.runId || task.lastSessionId !== request.sessionId) throw new Error(tr("检查结果不属于当前执行"));
        const result = (request.result ?? "").trim(), resultKey = (request.resultKey ?? "").trim();
        if (!result || !resultKey || result.length > 16000 || resultKey.length > 4000) throw new Error(tr("检查结果与比较标识不能为空或过长"));
        this.reports.set(task.id, {result, resultKey, complete: request.complete === true});
        return tr("已记录本次检查结果");
      }
      case "list": {
        const tasks = this.list();
        if (tasks.length === 0) return tr("还没有定时任务。");
        return tasks
          .map((task) => {
            const next = task.nextRunAt
              ? tr("下次 {when}", { when: when(task.nextRunAt) })
              : task.enabled
                ? tr("没有下一次了")
                : tr("已停用");
            const last = task.lastRunAt
              ? ` · ${tr("上次 {when} {status}", { when: when(task.lastRunAt), status: statusText(task.lastStatus) })}`
              : "";
            return `[${task.id}] ${task.name} · ${describeRule(task.rule)} · ${next}${last}\n    ${firstLine(task.prompt)}`;
          })
          .join("\n");
      }
      case "create": {
        const input = request.task;
        if (!input) throw new Error(tr("缺少任务内容"));
        const rule: ScheduleRule = {
          kind: input.kind as ScheduleRule["kind"],
          time: input.time,
          days: input.days,
          everyMinutes: input.everyMinutes,
          at: input.at,
        };
        const saved = this.save({ name: input.name, prompt: input.prompt, rule, workspace: input.workspace ?? "", mode: input.mode ?? (request.sessionId ? "followup" : "cron"), sessionId: request.sessionId, notificationPolicy: input.notificationPolicy, stopWhen: input.stopWhen });
        const next = saved.nextRunAt ? when(saved.nextRunAt) : tr("（没有下一次）");
        return tr(
          "已建好定时任务「{name}」（编号 {id}）：{rule}，下次 {next}。到点时 AIClaw 要开着才会跑；用户可以在「设置 → 定时任务」里查看、暂停或修改。",
          { name: saved.name, id: saved.id, rule: describeRule(saved.rule), next },
        );
      }
      case "delete": {
        const id = (request.id ?? "").trim();
        const task = this.read().find((item) => item.id === id);
        if (!task) throw new Error(tr("没有编号为 {id} 的定时任务（先用 schedule_list 看看）", { id }));
        this.remove(id);
        return tr("已删掉定时任务「{name}」。", { name: task.name });
      }
    }
    throw new Error(tr("不认识的操作：{action}", { action: request.action }));
  }
}

/** 规则里只留这种规则用得上的字段，存下来的文件干净、比较是否改过也准。 */
function normalizeRule(rule: ScheduleRule): ScheduleRule {
  switch (rule.kind) {
    case "daily":
    case "weekdays":
      return { kind: rule.kind, time: padTime(rule.time!) };
    case "weekly":
      return { kind: "weekly", time: padTime(rule.time!), days: [...new Set(rule.days)].sort() };
    case "interval":
      return { kind: "interval", everyMinutes: Math.round(rule.everyMinutes!) };
    case "once":
      return { kind: "once", at: new Date(rule.at!).toISOString() };
  }
}

function padTime(value: string): string {
  const [hour, minute] = value.trim().split(":");
  return `${hour!.padStart(2, "0")}:${minute}`;
}

function statusText(status: ScheduledTask["lastStatus"]): string {
  switch (status) {
    case "running":
      return tr("正在跑");
    case "ok":
      return tr("完成");
    case "failed":
      return tr("失败");
    case "missed":
      return tr("错过了");
    default:
      return "";
  }
}

/** 回给模型（也显示在步骤详情里）的时间，跟界面语言。 */
function when(iso: string): string {
  return formatWhen(new Date(iso), new Date());
}

function firstLine(text: string): string {
  const line = text.split("\n")[0] ?? "";
  return line.length > 60 ? `${line.slice(0, 60)}…` : line;
}

