import { EventEmitter } from "node:events";
import { existsSync, readFileSync, writeFileSync } from "node:fs";
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
  runTask(task: ScheduledTask): Promise<string>;
  /** 任务跑完（或失败）时通知用户。 */
  notify(task: ScheduledTask, ok: boolean, detail: string): void;
}

const TICK_MS = 30_000;

export class Scheduler extends EventEmitter {
  private timer: ReturnType<typeof setInterval> | undefined;
  private running = new Set<string>();

  private readonly dir: string;
  private readonly host: SchedulerHost;

  constructor(dir: string, host: SchedulerHost) {
    super();
    this.dir = dir;
    this.host = host;
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
    writeFileSync(this.path, `${JSON.stringify(tasks, null, 2)}\n`, "utf8");
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
    setTimeout(() => void this.tick(), 8_000);
    this.timer = setInterval(() => void this.tick(), TICK_MS);
  }

  stop(): void {
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
    this.running.add(task.id);
    const startedAt = new Date().toISOString();
    try {
      const sessionId = await this.host.runTask(task);
      this.update(task.id, (item) => {
        item.lastRunAt = startedAt;
        item.lastStatus = "running";
        item.lastError = undefined;
        item.lastSessionId = sessionId;
        if (scheduled) {
          item.anchorAt = startedAt;
          // 一次性的跑过就关掉，留在列表里让用户看得到结果。
          if (item.rule.kind === "once") item.enabled = false;
        }
      });
      return sessionId;
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      this.update(task.id, (item) => {
        item.lastRunAt = startedAt;
        item.lastStatus = "failed";
        item.lastError = message;
        if (scheduled) item.anchorAt = startedAt;
      });
      this.host.notify(task, false, message);
      this.running.delete(task.id);
      throw error;
    }
  }

  /** 会话的一轮结束了：是某个任务开的那个，就记下结果并通知。 */
  onTurnCompleted(sessionId: string, error: string | undefined): void {
    const task = this.read().find((item) => item.lastSessionId === sessionId && item.lastStatus === "running");
    if (!task) return;
    this.running.delete(task.id);
    this.update(task.id, (item) => {
      item.lastStatus = error ? "failed" : "ok";
      item.lastError = error || undefined;
    });
    this.host.notify(task, !error, error ?? "");
  }

  /** 模型经 schedule_* 工具发来的请求。回一段给模型看的话；它也显示在界面的步骤详情里，跟界面语言走。 */
  handleModelRequest(request: {
    action: string;
    id?: string;
    task?: { name: string; prompt: string; kind: string; time?: string; days?: number[]; everyMinutes?: number; at?: string; workspace?: string };
  }): string {
    switch (request.action) {
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
        const saved = this.save({ name: input.name, prompt: input.prompt, rule, workspace: input.workspace ?? "" });
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

