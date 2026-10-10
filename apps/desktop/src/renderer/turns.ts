import type { SearchSource } from "../shared/types";
import { referencedFiles } from "./markdown.js";
import type { TimelineEntry } from "./store.js";

/**
 * 把时间线按「轮」切开。
 *
 * 一轮 = 一条用户消息，加上它触发的全部执行步骤与全部回答。
 *
 * 为什么不按时间线原样平铺：一轮里模型会被采样好几次（每次工具回填之后再问
 * 一次），于是「几句话 → 几个步骤 → 又几句话」交替出现，答案被切成好几截，
 * 中间夹着执行细节。把步骤全部抽出来归成一块之后，回答是连着读的一段，
 * 步骤是一个可以展开的整体。
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
}

/** Only successful structured artifacts, never paths guessed from assistant prose. */
export function turnArtifacts(turn: Turn): string[] {
  return [...new Set(turn.steps.filter(step => step.state === "done")
    .flatMap(step => step.artifacts ?? []).filter(path => path.trim() !== ""))];
}

export function imageArtifacts(turn: Turn): string[] {
  return turnArtifacts(turn).filter(path => /\.(png|jpe?g|webp|gif)$/i.test(path));
}

/** Hide only unambiguous matches; identical filenames in different folders remain distinct. */
export function unmentionedArtifacts(turn: Turn): string[] {
  const images = new Set(imageArtifacts(turn));
  const paths = turnArtifacts(turn).filter(path => !images.has(path));
  const mentioned = new Set<string>();
  const normalize = (path: string) => path.replaceAll("\\", "/").replace(/^\.\//, "");
  for (const ref of turn.messages.flatMap(message => referencedFiles(message.text))) {
    const normalized = normalize(ref);
    const exact = paths.filter(path => normalize(path) === normalized);
    const matches = exact.length ? exact : paths.filter(path =>
      normalize(path).endsWith(`/${normalized}`) || normalized.endsWith(`/${normalize(path)}`));
    if (matches.length === 1) mentioned.add(matches[0]!);
  }
  return paths.filter(path => !mentioned.has(path));
}

export function groupTurns(timeline: readonly TimelineEntry[]): Turn[] {
  const turns: Turn[] = [];
  let current: Turn | null = null;

  for (const entry of timeline) {
    if (entry.kind === "user") {
      current = { key: entry.id, user: entry, messages: [], steps: [] };
      turns.push(current);
      continue;
    }
    if (!current) {
      // 没有用户消息打头的条目自成一轮，不要并进后面那一轮——并进去的话
      // 它会显示在一条它根本不属于的提问底下。
      current = { key: `lead-${entry.id}`, user: null, messages: [], steps: [] };
      turns.push(current);
    }
    if (entry.kind === "step") current.steps.push(entry);
    else current.messages.push(entry);
  }

  return turns;
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
