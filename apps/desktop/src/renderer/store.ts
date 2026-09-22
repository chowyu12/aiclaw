import { reactive, readonly } from "vue";
import { describeError } from "./errors";
import type {
  AgentEventPayload,
  AppConfigView,
  ApprovalPayload,
  HistoryItemView,
  McpProbeView,
  McpServerView,
  ModelChoiceView,
  SkillView,
  RuntimeStatus,
  SessionGroupsView,
  SessionStartView,
  SessionSummaryView,
  UpdateStatusView,
} from "../shared/types";

declare global {
  interface Window {
    aiclaw: import("../preload/index.cts").AiclawApi;
  }
}

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
  | { kind: "user"; id: string; text: string; images?: readonly string[] }
  | { kind: "agent"; id: string; text: string; streaming: boolean }
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
      /** 仅 llm：模型名、第几次采样、首字节、推理耗时、发起了几个工具调用、用量。 */
      round?: number;
      ttftMs?: number;
      thinkMs?: number;
      toolCalls?: number;
      tokens?: number;
    };

const state = reactive({
  /** 主区显示什么。放在 store 里是因为侧边栏底部的设置要切它，点会话又要切回来。 */
  view: "chat" as "chat" | "mcp" | "skills" | "settings",
  runtime: { state: "stopped" } as RuntimeStatus,
  sessionId: "",
  /** 会话启动时挂载的工具与 MCP 状态，展示给用户看「这次能用什么」。 */
  sessionInfo: null as SessionStartView | null,
  /** 当前会话用的模型。与配置页的默认值分开：切模型只影响当前会话。 */
  model: "",
  timeline: [] as TimelineEntry[],
  approvals: [] as ApprovalPayload[],
  busy: false,
  config: null as AppConfigView | null,
  credentials: { llmKey: false },
  profiles: [] as { id: string; label: string; description: string }[],
  sessions: [] as SessionSummaryView[],
  /** 会话搜索。关键词为空时界面用 sessions，不看这里。 */
  sessionSearch: {
    keyword: "",
    results: [] as SessionSummaryView[],
    loading: false,
  },
  groups: { groups: [], assignments: {} } as SessionGroupsView,
  models: [] as ModelChoiceView[],
  mcpServers: [] as McpServerView[],
  skills: [] as SkillView[],
  modelsLoading: false,
  modelsError: "",
  /** 检查更新的结果。null 表示还没查过。 */
  update: null as UpdateStatusView | null,
  /**
   * 用户点过「以后再说」的版本号。**只记在内存里**——下次开应用会重新提醒
   * 一次，而攒一份持久的「永远别提醒」名单只会让人忘了自己还在用旧版本。
   */
  updateDismissed: "",
  updating: false,
  /** 正在后台下载新版本。 */
  updateDownloading: false,
  /** 新版本已经下好，重启就能用。 */
  updateReady: false,
  /** 升级包的下载进度，0~100；没有 content-length 时是 -1（只知道在下）。 */
  updateProgress: -1,
  error: "",
});

/**
 * 把 Vue 的响应式代理剥成纯对象，再交给 IPC。
 *
 * Electron 的 invoke 走结构化克隆，**克隆不了 Proxy**——会抛
 * 「An object could not be cloned」。reactive() 里读出来的每个对象都是代理，
 * 所以 `[...state.list, newItem]` 这种写法在列表非空时必然失败。
 *
 * 这个 bug 的表现很迷惑：第一条能加进去（那时列表是空的，数组里只有新建的
 * 纯对象），第二条就开始失败。凡是把既有元素原样带过去的写法（展开、filter）
 * 都要先过这里；`.map(x => ({ ...x }))` 因为重建了对象反而是安全的。
 */
function plain<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}

function findEntry(id: string): TimelineEntry | undefined {
  return state.timeline.find((entry) => entry.id === id);
}

function upsertAgent(itemId: string): TimelineEntry {
  const existing = findEntry(itemId);
  if (existing) return existing;
  const created: TimelineEntry = { kind: "agent", id: itemId, text: "", streaming: true };
  state.timeline.push(created);
  return created;
}

type StepStats = Pick<
  Extract<TimelineEntry, { kind: "step" }>,
  "seq" | "startedAt" | "durationMs" | "round" | "ttftMs" | "thinkMs" | "toolCalls" | "tokens"
>;

function pushStep(
  id: string,
  step: "llm" | "tool" | "notice",
  title: string,
  stepState: "running" | "done" | "failed",
  detail = "",
  stats: StepStats = {},
): void {
  if (findEntry(id)) return;
  state.timeline.push({ kind: "step", id, step, title, detail, state: stepState, ...stats });
}

/** 从条目里取出计时与统计。 */
function stepStats(item: Record<string, unknown>): StepStats {
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
 * claw-agent 事件 → 界面状态。
 *
 * 事件模型是会话 → 轮次 → 条目三层；条目按 kind 分流进同一条时间线，
 * userMessage 忽略（发送时已经本地加过了，再加一次会重复）。
 */
function applyEvent(payload: AgentEventPayload): void {
  const params = (payload.params ?? {}) as Record<string, unknown>;
  switch (payload.method) {
    case "item/started": {
      const item = (params.item ?? {}) as Record<string, unknown>;
      const id = String(item.id ?? "");
      switch (item.kind) {
        case "agentMessage":
          upsertAgent(id);
          return;
        case "toolCall":
          pushStep(id, "tool", describeTool(item), "running", "", stepStats(item));
          return;
        case "llm":
          // 一次模型采样。推理正文之后通过 delta 挂到它的 detail 上——
          // 推理是这次采样的一部分，不是独立一步。
          pushStep(
            id,
            "llm",
            typeof item.summary === "string" ? item.summary : "模型",
            "running",
            "",
            stepStats(item),
          );
          return;
        default:
          return;
      }
    }
    case "item/delta": {
      const id = String(params.itemId ?? "");
      const delta = String(params.delta ?? "");
      const entry = findEntry(id);
      if (!entry) return;
      if (entry.kind === "agent") {
        entry.text += delta;
      } else if (entry.kind === "step") {
        // llm 步骤的 delta 是推理增量，挂在它的 detail 上。
        entry.detail += delta;
      }
      return;
    }
    case "item/completed": {
      const item = (params.item ?? {}) as Record<string, unknown>;
      const id = String(item.id ?? "");
      switch (item.kind) {
        case "agentMessage": {
          const entry = upsertAgent(id);
          if (entry.kind !== "agent") return;
          // 用完整文本覆盖增量拼接的结果：completed 带的是权威全文。
          if (typeof item.text === "string" && item.text) entry.text = item.text;
          entry.streaming = false;
          return;
        }
        case "toolCall": {
          const detail = toolDetail(item);
          const stats = stepStats(item);
          const entry = findEntry(id);
          if (!entry || entry.kind !== "step") {
            pushStep(
              id,
              "tool",
              describeTool(item),
              item.toolFailed ? "failed" : "done",
              detail,
              stats,
            );
            return;
          }
          entry.state = item.toolFailed ? "failed" : "done";
          entry.detail = detail;
          entry.title = describeTool(item);
          Object.assign(entry, stats);
          return;
        }
        case "llm": {
          const stats = stepStats(item);
          const title = typeof item.summary === "string" ? item.summary : "模型";
          const entry = findEntry(id);
          if (!entry || entry.kind !== "step") {
            pushStep(id, "llm", title, item.toolFailed ? "failed" : "done", "", stats);
            return;
          }
          entry.state = item.toolFailed ? "failed" : "done";
          entry.title = title;
          // 推理正文用 completed 带的权威全文覆盖流式拼接的结果。
          if (typeof item.text === "string" && item.text) entry.detail = item.text;
          Object.assign(entry, stats);
          return;
        }
        case "notice": {
          // 内核说的话：历史被压缩了、模型调用在重试。既不是模型输出也不是
          // 错误——压缩会悄悄丢掉一段历史，不说一声用户会以为模型失忆了。
          pushStep(id, "notice", typeof item.text === "string" ? item.text : "", "done");
          return;
        }
        case "reasoning":
          // 推理现在挂在 llm 步骤上，不再是独立条目。留着这个分支是为了
          // 让旧存档里的条目不至于掉到 default 去。
          return;
        default:
          return;
      }
    }
    case "turn/started":
      state.busy = true;
      return;
    case "turn/completed": {
      state.busy = false;
      for (const entry of state.timeline) {
        if (entry.kind === "agent") entry.streaming = false;
        else if (entry.kind === "step" && entry.state === "running") entry.state = "done";
      }
      const error = params.error;
      // 「已中断」是用户自己点的停止，不算错误，不弹红条。
      if (typeof error === "string" && error && error !== "已中断") state.error = error;
      // 标题与轮次数变了，侧边栏跟一下。
      void actions.refreshSessions();
      return;
    }
    case "error":
      state.busy = false;
      state.error = String(params.message ?? "运行出错");
      return;
    default:
      return;
  }
}

/**
 * 工具步骤展开后看到的内容：**先参数、后结果**。
 *
 * 只读工具不弹审批，所以这里是用户唯一能看见「它到底拿什么参数调的」的
 * 地方。只显示结果的话，「它查了哪家公司」这种问题就没有答案了。
 */
function toolDetail(item: Record<string, unknown>): string {
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

function describeTool(item: Record<string, unknown>): string {
  const name = String(item.toolName ?? "");
  const summary = typeof item.summary === "string" ? item.summary : "";
  return summary ? `${name} · ${summary}` : name;
}

/** 把恢复会话时拿到的历史条目摊回时间线。 */
function restoreHistory(history: HistoryItemView[]): TimelineEntry[] {
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
        });
        break;
      case "agentMessage":
        entries.push({ kind: "agent", id: item.id, text: item.text ?? "", streaming: false });
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

export type { AppConfigView };

export const store = readonly(state);

export const actions = {
  /**
   * 拉起界面需要的全部状态。
   *
   * 整段包在 try 里，是因为这里失败过一次而且**完全看不出来**：preload 没加载成功时
   * window.aiclaw 是 undefined，第一行就抛，config 停在 null，配置页的 v-if 什么都
   * 不渲染——用户看到的是一片空白，没有任何线索。宁可把原因摆在错误条上。
   */
  async bootstrap(): Promise<void> {
    if (!window.aiclaw) {
      // preload 没注入。这是构建问题不是配置问题，说清楚免得用户去翻设置。
      state.error =
        "本地桥接未加载（preload 注入失败），界面无法读写配置。这是构建问题：重新执行 make build 再启动；仍然如此请带上这条信息找开发。";
      return;
    }
    try {
      state.config = (await window.aiclaw.config.read()) as AppConfigView;
      state.model = state.config.model;
      state.credentials = (await window.aiclaw.credentials.status()) as { llmKey: boolean };
      state.profiles = (await window.aiclaw.profiles.list()) as {
        id: string;
        label: string;
        description: string;
      }[];
      state.groups = (await window.aiclaw.groups.read()) as SessionGroupsView;
      state.mcpServers = (await window.aiclaw.mcp.read()) as McpServerView[];
      state.skills = (await window.aiclaw.skills.list()) as SkillView[];
    } catch (error) {
      state.error = `读取本地配置失败：${describeError(error)}`;
      return;
    }

    window.aiclaw.on.agentEvent((payload) => applyEvent(payload as AgentEventPayload));
    window.aiclaw.on.approval((payload) => {
      state.approvals.push(payload as ApprovalPayload);
    });
    window.aiclaw.on.runtimeStatus((payload) => {
      state.runtime = payload as RuntimeStatus;
      if (state.runtime.state === "failed") {
        state.error = state.runtime.detail ?? "本地运行时启动失败";
      }
    });
    window.aiclaw.on.updateProgress((payload) => {
      const { received, total } = payload as { received: number; total: number };
      state.updateProgress = total > 0 ? Math.round((received / total) * 100) : -1;
    });
  },

  async saveConfig(patch: Partial<AppConfigView>): Promise<void> {
    state.config = (await window.aiclaw.config.write(patch)) as AppConfigView;
    // 还没开会话时顶部显示的就是默认模型，跟着配置走。
    if (!state.sessionId) state.model = state.config.model;
  },

  async saveCredentials(patch: { llmKey?: string }): Promise<void> {
    const wasReady = state.runtime.state === "ready";
    state.credentials = (await window.aiclaw.credentials.write(patch)) as { llmKey: boolean };
    // 主进程存完凭据会重启运行时（Key 只在拉起内核时进它的进程环境）。
    // 重启之后原来的会话句柄没了，这里重新接上——不接的话界面看着一切正常，
    // 下一句话却会报「会话不存在」。
    if (wasReady) {
      state.sessionId = "";
      state.sessionInfo = null;
      state.timeline = [];
      await actions.startRuntime();
    }
  },

  /**
   * 给当前会话挑一个工作区。取消选择就什么都不做。
   *
   * 工作区是**按会话**的：同一个人上午在改仓库 A、下午在整理一份报告，
   * 那是两个会话、两个地方。早先是一个全局工作目录，所有会话共用
   * ~/Workspace，而用户真正想让 Agent 干活的地方在别处。
   */
  async pickWorkspace(): Promise<void> {
    const dir = (await window.aiclaw.dialog.pickDirectory()) as string | null;
    if (dir) await actions.setWorkspace(dir);
  },

  /** 设置（空串表示清掉）当前会话的工作区。 */
  async setWorkspace(workspace: string): Promise<void> {
    if (!state.sessionId) return;
    try {
      const applied = (await window.aiclaw.session.setWorkspace({
        sessionId: state.sessionId,
        workspace,
      })) as string;
      if (state.sessionInfo) state.sessionInfo = { ...state.sessionInfo, workspace: applied };
    } catch (error) {
      state.error = describeError(error);
    }
  },

  // ---------- 运行时与会话 ----------

  /**
   * 拉起本地运行时，然后接着上次的会话。
   *
   * 接着上次而不是每次都开新的：应用现在是启动即拉起运行时，每次都开新会话
   * 的话，侧边栏会攒出一串从没说过话的「未命名会话」。真想要新的，左上角
   * 就是「新对话」。一个会话都没有时才开新的。
   */
  async startRuntime(): Promise<void> {
    state.error = "";
    try {
      await window.aiclaw.runtime.start();
      await actions.refreshSessions();
      const latest = state.sessions[0];
      if (latest) await actions.openSession(latest.id);
      else await actions.newSession();
      void actions.loadModels();
    } catch (error) {
      state.error = `启动本地运行时失败：${describeError(error)}`;
    }
  },

  /**
   * 开一个新会话。模型用配置页的默认值——临时换模型不该变成长期设置。
   *
   * 工作区默认**不带**：新会话先不预设在哪儿干活，用户想好了再指。
   * 传 workspace 是给「从某个会话的工作区接着开一个」用的。
   */
  async newSession(workspace?: string): Promise<void> {
    state.view = "chat";
    const info = (await window.aiclaw.session.start({ workspace })) as SessionStartView;
    state.sessionId = info.sessionId;
    state.sessionInfo = info;
    state.model = info.model;
    state.timeline = [];
    state.busy = false;
    await actions.refreshSessions();
  },

  /** 打开一个已有会话，连同它的历史一起还原。 */
  async openSession(sessionId: string): Promise<void> {
    state.view = "chat";
    if (sessionId === state.sessionId) return;
    state.error = "";
    try {
      const info = (await window.aiclaw.session.resume(sessionId)) as SessionStartView;
      state.sessionId = info.sessionId;
      state.sessionInfo = info;
      state.model = info.model;
      state.timeline = restoreHistory(info.history ?? []);
      state.busy = false;
    } catch (error) {
      state.error = `打开会话失败：${describeError(error)}`;
    }
  },

  async refreshSessions(): Promise<void> {
    try {
      state.sessions = (await window.aiclaw.session.list()) as SessionSummaryView[];
    } catch {
      // 列表拉不到不该打断正在进行的对话，静默即可——侧边栏空着就空着。
    }
  },

  /**
   * 按关键词找会话。标题与正文都找，命中的那一段由内核给出。
   *
   * 每次打字都打一遍内核：这是本机 SQLite 上的一次 LIKE，几百个会话的量级
   * 比在渲染层维护一份全文索引简单得多，也不会和真实数据不同步。
   * 防抖在界面那边做。
   */
  async searchSessions(keyword: string): Promise<void> {
    state.sessionSearch.keyword = keyword;
    if (!keyword.trim()) {
      state.sessionSearch.results = [];
      state.sessionSearch.loading = false;
      return;
    }
    state.sessionSearch.loading = true;
    try {
      const found = (await window.aiclaw.session.search(keyword)) as SessionSummaryView[];
      // 打字很快，回来的可能是上一个关键词的结果。对不上就丢掉。
      if (state.sessionSearch.keyword === keyword) state.sessionSearch.results = found;
    } catch (error) {
      state.error = describeError(error);
    } finally {
      if (state.sessionSearch.keyword === keyword) state.sessionSearch.loading = false;
    }
  },

  async deleteSession(sessionId: string): Promise<void> {
    await window.aiclaw.session.remove(sessionId);
    if (sessionId === state.sessionId) {
      state.sessionId = "";
      state.sessionInfo = null;
      state.timeline = [];
    }
    await actions.refreshSessions();
    state.groups = (await window.aiclaw.groups.read()) as SessionGroupsView;
  },

  // ---------- 分组 ----------

  async createGroup(name: string): Promise<void> {
    state.groups = (await window.aiclaw.groups.create(name)) as SessionGroupsView;
  },

  async renameGroup(groupId: string, name: string): Promise<void> {
    state.groups = (await window.aiclaw.groups.rename({ groupId, name })) as SessionGroupsView;
  },

  /** 删分组不删会话：成员回到「未分组」。 */
  async deleteGroup(groupId: string): Promise<void> {
    state.groups = (await window.aiclaw.groups.remove(groupId)) as SessionGroupsView;
  },

  async assignSession(sessionId: string, groupId: string | null): Promise<void> {
    state.groups = (await window.aiclaw.session.assign({
      sessionId,
      groupId,
    })) as SessionGroupsView;
  },

  // ---------- 自定义 MCP server ----------

  /** 取可变副本：store 是 readonly() 包过的，直接改它的元素会被 DeepReadonly 挡住。 */
  getMcpServers(): McpServerView[] {
    return state.mcpServers.map((server) => ({
      ...server,
      args: server.args ? [...server.args] : undefined,
      env: server.env ? { ...server.env } : undefined,
      headers: server.headers ? { ...server.headers } : undefined,
    }));
  },

  async saveMcpServers(servers: McpServerView[]): Promise<void> {
    state.mcpServers = (await window.aiclaw.mcp.write(plain(servers))) as McpServerView[];
  },

  /**
   * 打开模型在对话里提到的那个文件。
   *
   * 校验全在主进程：路径按会话工作区解析、必须真的存在、可执行的那几类只
   * 「在访达里显示」、凭据目录直接拒。这里只负责把结果说给用户听——
   * 点了没反应是最糟的，所以拒绝和找不到都要出现在错误条上。
   */
  async openFile(path: string): Promise<void> {
    try {
      const result = (await window.aiclaw.files.open(path)) as {
        ok: boolean;
        action: string;
        detail: string;
      };
      if (!result.ok) state.error = result.detail;
      else if (result.action === "reveal") state.error = result.detail;
    } catch (error) {
      state.error = describeError(error);
    }
  },

  /**
   * 试连一个 MCP server，列出它的工具。
   *
   * 挂载只发生在开会话时，配置页上原来看不到「通不通」——用户填完地址
   * 只能去开个会话再回来翻状态。这里直接给结论。
   */
  async probeMcp(server: McpServerView): Promise<McpProbeView> {
    return (await window.aiclaw.mcp.probe(plain(server))) as McpProbeView;
  },

  // ---------- 技能 ----------

  async refreshSkills(): Promise<void> {
    state.skills = (await window.aiclaw.skills.list()) as SkillView[];
  },

  async toggleSkill(id: string, enabled: boolean): Promise<void> {
    state.skills = (await window.aiclaw.skills.toggle({ id, enabled })) as SkillView[];
  },

  async deleteSkill(id: string): Promise<void> {
    // 别处目录里的技能删不了，主进程会抛。把原因摆到错误条上，
    // 而不是让这个按钮看起来点了没反应。
    try {
      state.skills = (await window.aiclaw.skills.remove(id)) as SkillView[];
    } catch (error) {
      state.error = describeError(error);
    }
  },

  async openSkillsDir(): Promise<void> {
    await window.aiclaw.skills.openDir();
  },

  // ---------- 模型 ----------

  // ---------- 检查更新 ----------

  /**
   * 查有没有新版本。
   *
   * 查不到不算错误，不往错误条上报——网络不通、指针文件暂时取不到都很正常，
   * 为这个在界面上摆一条红的，只会把人训练成无视错误条。
   */
  async checkUpdate(): Promise<void> {
    try {
      state.update = (await window.aiclaw.update.check()) as UpdateStatusView;
    } catch {
      state.update = null;
      return;
    }
    // 发现新版本就先下好，别等用户点。旧流程里那几十秒的下载卡在他按下
    // 按钮之后，只能盯着一个不动的界面等；先下好之后，点下去只剩替换与
    // 重开，两三秒的事。
    if (state.update?.hasUpdate && state.update.canInstall) {
      void actions.prepareUpdate();
    }
  },

  /**
   * 后台把新版本下下来。
   *
   * 失败不报到错误条上：用户没点过任何东西，弹一条他看不懂的错误只是打扰。
   * 点「重启更新」时还会再试一次，那时候失败才该说。
   */
  async prepareUpdate(): Promise<void> {
    if (state.updateDownloading || state.updateReady) return;
    state.updateDownloading = true;
    state.updateProgress = -1;
    try {
      const result = (await window.aiclaw.update.prepare()) as {
        ready: boolean;
        version: string;
      };
      state.updateReady = result.ready && result.version === state.update?.latest;
    } catch {
      state.updateReady = false;
    } finally {
      state.updateDownloading = false;
    }
  },

  /** 装新版本。脚本会退掉这个应用再打开新的，所以这之后没有下文。 */
  async installUpdate(): Promise<void> {
    if (state.updating) return;
    state.updating = true;
    state.updateProgress = -1;
    try {
      const result = (await window.aiclaw.update.install()) as {
        started: boolean;
        detail: string;
      };
      if (!result.started) {
        state.updating = false;
        state.error = result.detail;
      }
    } catch (error) {
      state.updating = false;
      state.error = `升级失败：${describeError(error)}`;
    }
  },

  dismissUpdate(): void {
    state.updateDismissed = state.update?.latest ?? "";
  },

  async loadModels(force = false): Promise<void> {
    state.modelsLoading = true;
    state.modelsError = "";
    try {
      state.models = (await window.aiclaw.models.list(force)) as ModelChoiceView[];
    } catch (error) {
      // 拉不到列表不影响已配好的模型继续用，所以只标在下拉里，
      // 不占用顶部的错误条。
      state.modelsError = describeError(error);
    } finally {
      state.modelsLoading = false;
    }
  },

  /**
   * 没设过默认模型时，拿 airouter 返回的第一个顶上。
   *
   * 配置页不再有「默认模型」这一格（挪进了「高级」），所以这里必须自己补上——
   * 否则新装的应用配完 Key 仍然开不了会话，而配置页上没有任何一处告诉用户
   * 缺的是什么。选第一个是因为顺序由 airouter 自己给；用户随时能在对话框上方
   * 换这个会话的模型，或到「高级」里改默认值。
   */
  async ensureDefaultModel(): Promise<boolean> {
    if (state.config?.model) return true;
    if (state.models.length === 0) await actions.loadModels();
    const first = state.models[0];
    if (!first) return false;
    await actions.saveConfig({ model: first.id, contextWindow: first.contextWindow });
    return true;
  },

  /**
   * 换当前会话的模型。只影响这个会话；新会话仍用配置页的默认值。
   *
   * 上下文窗口一起下发：不同模型窗口能差一个数量级，沿用上一个模型的值
   * 会让内核要么过早压缩、要么撑爆窗口。
   */
  async switchModel(modelId: string): Promise<void> {
    if (!modelId || modelId === state.model) return;
    const choice = state.models.find((model) => model.id === modelId);
    const previous = state.model;
    state.model = modelId;
    if (!state.sessionId) return;
    try {
      await window.aiclaw.session.configure({
        sessionId: state.sessionId,
        model: modelId,
        contextWindow: choice?.contextWindow ?? 0,
      });
      await actions.refreshSessions();
    } catch (error) {
      // 切失败就把显示切回去，别让界面显示一个内核并没有在用的模型。
      state.model = previous;
      state.error = `切换模型失败：${describeError(error)}`;
    }
  },

  // ---------- 对话 ----------

  /**
   * 发一条消息。轮次进行中也能发——内核会把它排进那一轮，模型下一次
   * 开口前就能看到。用户想纠正方向往往正是因为看见这一轮跑偏了，
   * 让他先等完一轮是最不该做的。
   */
  async send(text: string, images: string[] = []): Promise<void> {
    if (!text.trim() && images.length === 0) return;
    // 没有会话就先开一个。删掉当前会话之后运行时仍然是 ready，输入框还能打字，
    // 早先这里直接 return——发出去石沉大海，用户看不出发生了什么。
    if (!state.sessionId) {
      try {
        await actions.newSession();
      } catch (error) {
        state.error = `新建会话失败：${describeError(error)}`;
        return;
      }
    }
    state.timeline.push({
      kind: "user",
      id: `user-${Date.now()}`,
      text,
      images: images.map((data) => `data:image/jpeg;base64,${data}`),
    });
    state.busy = true;
    try {
      await window.aiclaw.session.send({ sessionId: state.sessionId, text, images });
    } catch (error) {
      state.busy = false;
      state.error = describeError(error);
    }
  },

  async interrupt(): Promise<void> {
    if (state.sessionId) await window.aiclaw.session.interrupt(state.sessionId);
  },

  async respondApproval(
    id: string,
    approved: boolean,
    scope?: "once" | "session",
  ): Promise<void> {
    await window.aiclaw.approval.respond(id, approved, scope);
    const index = state.approvals.findIndex((a) => a.id === id);
    if (index >= 0) state.approvals.splice(index, 1);
  },

  setView(view: "chat" | "mcp" | "skills" | "settings"): void {
    state.view = view;
  },

  clearError(): void {
    state.error = "";
  },
};
