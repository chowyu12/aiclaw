/**
 * 每个会话自己的时间线与运行状态，以及把内核事件写进去的那段逻辑。
 *
 * 为什么按会话分开存：内核里多个会话可以同时跑，而事件是带 sessionId 的。
 * 早先渲染层只有一条时间线、一个 busy，切会话就把它们整个换掉——正在后台跑的
 * 那一轮的事件仍然在到，全写进了**现在看着的**这个会话；切回去时那一轮的过程
 * 已经不见了，看起来像是切走就中断了。现在事件按 sessionId 落到各自的记录里，
 * 界面只是「看着」其中一条。
 *
 * 这个文件不碰 Vue、不碰 window：全是纯函数，能在 node 里测。store 把这里的
 * 容器放进 reactive() 之后再调用，响应式由代理负责。
 */

import type { AgentEventPayload, HistoryItemView } from "../shared/types";

/**
 * 时间线上的一条。
 *
 * 消息与执行步骤在**同一个数组**里，按到达顺序排。
 * 早先它们是两个数组分别渲染，结果所有步骤都堆在对话末尾，看不出哪一步
 * 属于哪一轮——而执行过程的价值恰恰在于它发生的位置。
 */
export type TimelineEntry =
  /**
   * images 用 readonly：store 外面拿到的是 readonly() 包过的深只读副本，
   * 声明成可变数组的话 `groupTurns(store.timeline)` 这类调用会类型不兼容。
   */
  /**
   * pending 标记「这条是本机发送时先垫上的回显」。内核随后会为同一条输入发来一个
   * userMessage 事件，那时把这条认领掉（换成内核的 id），而不是再添一条。
   * 通道会话（微信、企业微信）的消息不经过本机发送，没有回显可认领，直接追加。
   */
  | { kind: "user"; id: string; text: string; images?: readonly string[]; pending?: boolean; at?: number }
  /** at：消息的时间（Unix 毫秒）。旧存档里没有，界面上就不显示。 */
  | { kind: "agent"; id: string; text: string; streaming: boolean; at?: number }
  | {
      kind: "step";
      id: string;
      /** llm：一次模型采样；tool：工具调用；notice：内核说的话。 */
      step: "llm" | "tool" | "notice";
      title: string;
      /** 工具结果、推理正文或失败原因，折叠在条目里。 */
      detail: string;
      state: "running" | "done" | "failed";
      /** 轮次内的序号，从 1 开始。notice 没有。 */
      seq?: number;
      startedAt?: number;
      durationMs?: number;
      /** 这一步产出的文件（相对工作区）：生成的图、合成的语音。 */
      artifacts?: readonly string[];
      /** 仅 llm：模型名、第几次采样、首字节、推理耗时、发起了几个工具调用、用量。 */
      round?: number;
      ttftMs?: number;
      thinkMs?: number;
      toolCalls?: number;
      tokens?: number;
    };

/** 一个会话在渲染层的活动状态。 */
export interface LiveSession {
  timeline: TimelineEntry[];
  /** 有一轮在跑。发送那一刻置真，turn/completed 或出错置假。 */
  busy: boolean;
}

export function newLive(timeline: TimelineEntry[] = []): LiveSession {
  return { timeline, busy: false };
}

/** 取一个会话的记录，没有就建。事件可能先于界面打开那个会话到达。 */
export function ensureLive(live: Record<string, LiveSession>, sessionId: string): LiveSession {
  let record = live[sessionId];
  if (!record) {
    record = newLive();
    live[sessionId] = record;
  }
  return record;
}

/** applyAgentEvent 告诉 store 的事：动了哪个会话、是不是一轮结束了、有没有错误要显示。 */
export interface Applied {
  sessionId: string;
  method: string;
  /** turn/completed 带的异常原因或 error 事件的消息；「已中断」不算。 */
  error: string;
}

type StepStats = Pick<
  Extract<TimelineEntry, { kind: "step" }>,
  | "seq"
  | "startedAt"
  | "durationMs"
  | "round"
  | "ttftMs"
  | "thinkMs"
  | "toolCalls"
  | "tokens"
  | "artifacts"
>;

function findEntry(record: LiveSession, id: string): TimelineEntry | undefined {
  return record.timeline.find((entry) => entry.id === id);
}

function upsertAgent(record: LiveSession, itemId: string): TimelineEntry {
  const existing = findEntry(record, itemId);
  if (existing) return existing;
  const created: TimelineEntry = { kind: "agent", id: itemId, text: "", streaming: true };
  record.timeline.push(created);
  return created;
}

/**
 * 认领本机发送时垫的那条回显，认领不到就追加一条。
 *
 * 认领而不是「看见 userMessage 就跳过」：跳过对本机发送是对的，对通道会话就成了
 * 「只有答案没有问题」。认领要同时比文字——用户在上一轮跑着的时候又发了一条时，
 * 时间线上会同时有两条待认领的回显，只按顺序认会张冠李戴。
 */
function adoptOrAppendUser(record: LiveSession, id: string, text: string, at?: number): void {
  for (let index = record.timeline.length - 1; index >= 0; index--) {
    const entry = record.timeline[index]!;
    if (entry.kind !== "user" || !entry.pending) continue;
    if (entry.text !== text) continue;
    // 内核的 id 是权威的：恢复历史时用的也是它，换过来两边才对得上。
    // 图片保留本机那份——它已经是能直接显示的 data URL。
    // 时间也换成内核的：它记的是进历史的时刻，恢复历史时显示的也是它。
    record.timeline[index] = { ...entry, id, pending: undefined, at: at ?? entry.at };
    return;
  }
  record.timeline.push({ kind: "user", id, text, at });
}

function pushStep(
  record: LiveSession,
  id: string,
  step: "llm" | "tool" | "notice",
  title: string,
  stepState: "running" | "done" | "failed",
  detail = "",
  stats: StepStats = {},
): void {
  if (findEntry(record, id)) return;
  record.timeline.push({ kind: "step", id, step, title, detail, state: stepState, ...stats });
}

/** 从条目里取出计时与统计。 */
export function stepStats(item: Record<string, unknown>): StepStats {
  const usage = item.usage as { totalTokens?: number } | undefined;
  return {
    seq: numberOr(item.seq),
    startedAt: numberOr(item.startedAt),
    durationMs: numberOr(item.durationMs),
    round: numberOr(item.round),
    ttftMs: numberOr(item.ttftMs),
    thinkMs: numberOr(item.thinkMs),
    toolCalls: numberOr(item.toolCalls),
    tokens: numberOr(usage?.totalTokens),
  };
}

function numberOr(value: unknown): number | undefined {
  return typeof value === "number" ? value : undefined;
}

/**
 * claw-agent 事件 → 对应会话的状态。
 *
 * 事件模型是会话 → 轮次 → 条目三层；条目按 kind 分流进那个会话的时间线。
 *
 * 没带 sessionId 的事件（旧内核、或 error 里省略了）算到 fallback 头上——
 * 那是界面当前看着的会话，也是早先唯一的归宿。
 */
export function applyAgentEvent(
  live: Record<string, LiveSession>,
  payload: AgentEventPayload,
  fallback: string,
): Applied {
  const params = (payload.params ?? {}) as Record<string, unknown>;
  const sessionId = typeof params.sessionId === "string" && params.sessionId ? params.sessionId : fallback;
  const applied: Applied = { sessionId, method: payload.method, error: "" };
  if (!sessionId) return applied;
  const record = ensureLive(live, sessionId);

  switch (payload.method) {
    case "item/started": {
      const item = (params.item ?? {}) as Record<string, unknown>;
      const id = String(item.id ?? "");
      switch (item.kind) {
        case "agentMessage":
          upsertAgent(record, id);
          return applied;
        case "toolCall":
          pushStep(record, id, "tool", describeTool(item), "running", "", stepStats(item));
          return applied;
        case "llm":
          // 一次模型采样。推理正文之后通过 delta 挂到它的 detail 上——
          // 推理是这次采样的一部分，不是独立一步。
          pushStep(
            record,
            id,
            "llm",
            typeof item.summary === "string" ? item.summary : "模型",
            "running",
            "",
            stepStats(item),
          );
          return applied;
        default:
          return applied;
      }
    }
    case "item/delta": {
      const id = String(params.itemId ?? "");
      const delta = String(params.delta ?? "");
      const entry = findEntry(record, id);
      if (!entry) return applied;
      if (entry.kind === "agent") {
        entry.text += delta;
      } else if (entry.kind === "step") {
        // llm 步骤的 delta 是推理增量，挂在它的 detail 上。
        entry.detail += delta;
      }
      return applied;
    }
    case "item/completed": {
      const item = (params.item ?? {}) as Record<string, unknown>;
      const id = String(item.id ?? "");
      switch (item.kind) {
        case "agentMessage": {
          const entry = upsertAgent(record, id);
          if (entry.kind !== "agent") return applied;
          // 用完整文本覆盖增量拼接的结果：completed 带的是权威全文。
          if (typeof item.text === "string" && item.text) entry.text = item.text;
          entry.streaming = false;
          entry.at = typeof item.at === "number" && item.at > 0 ? item.at : Date.now();
          return applied;
        }
        case "toolCall": {
          const detail = toolDetail(item);
          const stats = stepStats(item);
          const artifacts = Array.isArray(item.artifacts) ? (item.artifacts as string[]) : [];
          const entry = findEntry(record, id);
          if (!entry || entry.kind !== "step") {
            pushStep(
              record,
              id,
              "tool",
              describeTool(item),
              item.toolFailed ? "failed" : "done",
              detail,
              { ...stats, artifacts },
            );
            return applied;
          }
          entry.state = item.toolFailed ? "failed" : "done";
          entry.detail = detail;
          entry.title = describeTool(item);
          entry.artifacts = artifacts;
          Object.assign(entry, stats);
          return applied;
        }
        case "llm": {
          const stats = stepStats(item);
          const title = typeof item.summary === "string" ? item.summary : "模型";
          const entry = findEntry(record, id);
          if (!entry || entry.kind !== "step") {
            pushStep(record, id, "llm", title, item.toolFailed ? "failed" : "done", "", stats);
            return applied;
          }
          entry.state = item.toolFailed ? "failed" : "done";
          entry.title = title;
          // 推理正文用 completed 带的权威全文覆盖流式拼接的结果。
          if (typeof item.text === "string" && item.text) entry.detail = item.text;
          Object.assign(entry, stats);
          return applied;
        }
        case "userMessage": {
          // **通道会话全靠这一条。** 微信 / 企业微信的提问不经过本机发送，没有
          // 回显可认领；不收下它，界面上就只有答案没有问题，要切走再切回来、
          // 让内核历史补上才看得见。本机发的那条已经垫过回显，认领即可。
          adoptOrAppendUser(
            record,
            id,
            typeof item.text === "string" ? item.text : "",
            typeof item.at === "number" ? item.at : undefined,
          );
          return applied;
        }
        case "notice": {
          // 内核说的话：历史被压缩了、模型调用在重试。既不是模型输出也不是
          // 错误——压缩会悄悄丢掉一段历史，不说一声用户会以为模型失忆了。
          pushStep(record, id, "notice", typeof item.text === "string" ? item.text : "", "done");
          return applied;
        }
        default:
          // reasoning 现在挂在 llm 步骤上，不再是独立条目；旧存档里的落到这儿。
          return applied;
      }
    }
    case "turn/started":
      record.busy = true;
      return applied;
    case "turn/completed": {
      record.busy = false;
      for (const entry of record.timeline) {
        if (entry.kind === "agent") entry.streaming = false;
        else if (entry.kind === "step" && entry.state === "running") entry.state = "done";
      }
      const error = params.error;
      // 「已中断」是用户自己点的停止，不算错误，不弹红条。
      if (typeof error === "string" && error && error !== "已中断") applied.error = error;
      return applied;
    }
    case "error":
      record.busy = false;
      applied.error = String(params.message ?? "运行出错");
      return applied;
    default:
      return applied;
  }
}

/**
 * 工具步骤展开后看到的内容：**先参数、后结果**。
 *
 * 只读工具不弹审批，所以这里是用户唯一能看见「它到底拿什么参数调的」的
 * 地方。只显示结果的话，「它查了哪家公司」这种问题就没有答案了。
 */
export function toolDetail(item: Record<string, unknown>): string {
  const args = typeof item.toolArgs === "string" ? item.toolArgs.trim() : "";
  const result = typeof item.toolResult === "string" ? item.toolResult : "";
  const sections: string[] = [];
  // 空参数不占地方：一个 {} 挤在上面只会把结果推下去。
  if (args && args !== "{}") sections.push(`参数\n${prettyJson(args)}`);
  if (result) sections.push(`结果\n${result}`);
  return sections.join("\n\n");
}

/** 参数能解析成 JSON 就缩进显示；解析不了就原样——原样总比丢掉强。 */
function prettyJson(raw: string): string {
  try {
    return JSON.stringify(JSON.parse(raw), null, 2);
  } catch {
    return raw;
  }
}

export function describeTool(item: Record<string, unknown>): string {
  const name = String(item.toolName ?? "");
  const summary = typeof item.summary === "string" ? item.summary : "";
  return summary ? `${name} · ${summary}` : name;
}

/** 把恢复会话时拿到的历史条目摊回时间线。 */
export function restoreHistory(history: HistoryItemView[]): TimelineEntry[] {
  const entries: TimelineEntry[] = [];
  for (const item of history) {
    switch (item.kind) {
      case "userMessage":
        entries.push({
          kind: "user",
          id: item.id,
          text: item.text ?? "",
          // 恢复出来的是裸 base64，界面要的是能直接塞进 <img> 的 data URL。
          // 内核那边统一成 JPEG，所以这里也按 JPEG 拼。
          images: (item.images ?? []).map((data) => `data:image/jpeg;base64,${data}`),
          at: item.at,
        });
        break;
      case "agentMessage":
        entries.push({ kind: "agent", id: item.id, text: item.text ?? "", streaming: false, at: item.at });
        break;
      case "toolCall":
        entries.push({
          kind: "step",
          id: item.id,
          step: "tool",
          title: describeTool(item as unknown as Record<string, unknown>),
          detail: toolDetail(item as unknown as Record<string, unknown>),
          state: item.toolFailed ? "failed" : "done",
          ...stepStats(item as unknown as Record<string, unknown>),
        });
        break;
      case "llm":
        // 采样步骤也要还原，否则重开会话看到的步骤里只有工具、没有「谁决定
        // 调它们」，与这一轮正在跑时看到的对不上。内核从 assistant 消息还原，
        // 所以没有耗时与 token——那些数字只存在于当轮的事件流里。
        entries.push({
          kind: "step",
          id: item.id,
          step: "llm",
          title: item.summary || "模型",
          detail: "",
          state: "done",
          ...stepStats(item as unknown as Record<string, unknown>),
        });
        break;
      case "notice":
        entries.push({
          kind: "step",
          id: item.id,
          step: "notice",
          title: item.text ?? "",
          detail: "",
          state: "done",
        });
        break;
      default:
        break;
    }
  }
  return entries;
}
