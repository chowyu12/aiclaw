import type { SearchSource } from "../shared/types";
import { referencedFiles } from "./markdown.js";
import type { TimelineEntry } from "./store.js";

/**
 * 把时间线按「轮」切开。
 *
 * 一轮 = 一条用户消息，加上它触发的全部执行步骤与全部回答。
 *
 * 保留正文与工具活动的原始顺序，展示时连续的工具合并成一行活动摘要。
 * messages 和 steps 仍用于复制回答、引用来源与展开执行详情。
 *
 * 纯函数、单独成文件，为的是能在 node 里直接测（`scripts/test-turns.ts`）；
 * ChatView 里那段分组逻辑出过一次错就很难发现——界面还是有东西的，只是
 * 分组不对。
 */

export type UserEntry = Extract<TimelineEntry, { kind: "user" }>;
export type AgentEntry = Extract<TimelineEntry, { kind: "agent" }>;
export type StepEntry = Extract<TimelineEntry, { kind: "step" }>;

export interface Turn {
  /** 渲染用的稳定 key：有用户消息就用它的 id，否则用首个条目的 id。 */
  key: string;
  /** 恢复的历史可能以助手消息开头（旧存档），所以允许没有。 */
  user: UserEntry | null;
  messages: AgentEntry[];
  steps: StepEntry[];
  entries: (AgentEntry | StepEntry)[];
}

/** A message followed by more tool/model activity is progress, including interrupted turns. */
export function finalMessage(turn: Turn): AgentEntry | null {
  const last = turn.entries.at(-1);
  return last?.kind === "agent" && !last.streaming ? last : null;
}

/** Tool artifacts stay in steps; only final-answer references select user-facing previews. */
export function turnArtifacts(turn: Turn): string[] {
  const answer = finalMessage(turn);
  if (!answer) return [];
  return referencedFiles(answer.text);
}

export function imageArtifacts(turn: Turn): string[] {
  return turnArtifacts(turn).filter(path => /\.(png|jpe?g|webp|gif)$/i.test(path));
}

export function groupTurns(timeline: readonly TimelineEntry[]): Turn[] {
  const turns: Turn[] = [];
  let current: Turn | null = null;

  for (const entry of timeline) {
    if (entry.kind === "user") {
      current = { key: entry.id, user: entry, messages: [], steps: [], entries: [] };
      turns.push(current);
      continue;
    }
    if (!current) {
      // 没有用户消息打头的条目自成一轮，不要并进后面那一轮——并进去的话
      // 它会显示在一条它根本不属于的提问底下。
      current = { key: `lead-${entry.id}`, user: null, messages: [], steps: [], entries: [] };
      turns.push(current);
    }
    current.entries.push(entry);
    if (entry.kind === "step") current.steps.push(entry);
    else current.messages.push(entry);
  }

  return turns;
}

export type TurnBlock =
  | { kind: "message"; key: string; message: AgentEntry }
  | { kind: "activity"; key: string; steps: StepEntry[] };

/** Only adjacent activities merge; a progress message always keeps its place. */
export function turnBlocks(turn: Turn, live = false): TurnBlock[] {
  const final = live ? null : finalMessage(turn);
  const blocks: TurnBlock[] = [];
  for (const entry of turn.entries) {
    if (entry === final) continue;
    if (entry.kind === "agent") {
      blocks.push({ kind: "message", key: entry.id, message: entry });
      continue;
    }
    const previous = blocks.at(-1);
    if (previous?.kind === "activity") previous.steps.push(entry);
    else blocks.push({ kind: "activity", key: entry.id, steps: [entry] });
  }
  // Completed sampling alone adds no user-facing activity; its metrics remain in execution details.
  return blocks.filter(block => block.kind === "message" || block.steps.some(step => step.step !== "llm" || step.state !== "done"));
}

/**
 * 一轮里执行步骤占用的墙上时间。
 *
 * 用「最后一步结束 − 第一步开始」而不是把每步耗时加起来：只读工具是并行跑的，
 * 相加会把 3 个并行的 5 秒说成 15 秒，而用户看这个数就是想知道自己等了多久。
 */
export function stepsElapsed(steps: readonly StepEntry[]): number {
  let first = Infinity;
  let last = -Infinity;
  for (const step of steps) {
    if (step.startedAt === undefined) continue;
    first = Math.min(first, step.startedAt);
    last = Math.max(last, step.startedAt + (step.durationMs ?? 0));
  }
  if (first === Infinity || last <= first) return 0;
  return last - first;
}

/** Sources are tool metadata, independent of which links the model includes in its answer. */
export function turnSources(turn: Turn): SearchSource[] {
  const sources = new Map<string, SearchSource>();
  for (const step of turn.steps) for (const source of step.sources ?? []) {
    if (!source || typeof source.url !== "string") continue;
    try {
      const url = new URL(source.url);
      if (!["http:", "https:"].includes(url.protocol) || !url.hostname || url.username || url.password) continue;
      url.hash = "";
      if (!sources.has(url.href)) sources.set(url.href, {
        url: url.href, title: typeof source.title === "string" ? source.title : "",
        snippet: typeof source.snippet === "string" ? source.snippet : "",
      });
    } catch { /* Invalid persisted URLs are not navigation targets. */ }
  }
  return [...sources.values()];
}
