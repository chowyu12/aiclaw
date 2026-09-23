import { reactive, readonly } from "vue";
import { describeError } from "./errors";
import { modelChoices, usable, type ModelChoice } from "./model-choices";
import {
  applyAgentEvent,
  ensureLive,
  newLive,
  restoreHistory,
  type LiveSession,
  type TimelineEntry,
} from "./live-sessions";
import type {
  AgentEventPayload,
  AppConfigView,
  ApprovalPayload,
  ChannelBindingView,
  ChannelStatusView,
  McpProbeView,
  McpServerView,
  PluginConfigFieldView,
  PluginContributionsView,
  PluginView,
  ProviderView,
  SearchEngineView,
  SearchHitView,
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

const EMPTY_TIMELINE: TimelineEntry[] = [];

const state = reactive({
  /** 主区显示什么。放在 store 里是因为侧边栏底部的设置要切它，点会话又要切回来。 */
  view: "chat" as "chat" | "providers" | "search" | "plugins" | "mcp" | "skills" | "settings",
  runtime: { state: "stopped" } as RuntimeStatus,
  sessionId: "",
  /** 会话启动时挂载的工具与 MCP 状态，展示给用户看「这次能用什么」。 */
  sessionInfo: null as SessionStartView | null,
  /** 当前会话用的模型与模型服务。与配置页的默认值分开：切模型只影响当前会话。 */
  model: "",
  providerId: 0,
  /**
   * 每个会话自己的时间线与运行状态，按 sessionId 存。
   *
   * 切会话只是换 sessionId，正在后台跑的那一轮照样往它自己的记录里写；
   * 切回去看到的就是它跑到哪儿了。见 live-sessions.ts。
   */
  live: {} as Record<string, LiveSession>,
  /** 当前看着的会话的时间线。空会话给一个稳定的空数组，免得每次读都换引用。 */
  get timeline(): TimelineEntry[] {
    return this.live[this.sessionId]?.timeline ?? EMPTY_TIMELINE;
  },
  /** 当前看着的会话有一轮在跑。 */
  get busy(): boolean {
    return this.live[this.sessionId]?.busy ?? false;
  },
  approvals: [] as ApprovalPayload[],
  config: null as AppConfigView | null,
  profiles: [] as { id: string; label: string; description: string }[],
  sessions: [] as SessionSummaryView[],
  /** 会话搜索。关键词为空时界面用 sessions，不看这里。 */
  sessionSearch: {
    keyword: "",
    results: [] as SessionSummaryView[],
    loading: false,
  },
  groups: { groups: [], assignments: {} } as SessionGroupsView,
  /** 模型服务清单。由内核从配置库读，所以要运行时起来之后才有。 */
  providers: [] as ProviderView[],
  providersLoading: false,
  providersError: "",
  mcpServers: [] as McpServerView[],
  skills: [] as SkillView[],
  /** 联网搜索引擎。由内核从应用库读。 */
  searchEngines: [] as SearchEngineView[],
  searchLoading: false,
  searchError: "",
  /** 插件清单与它们合起来贡献的东西。由内核从应用库读，要运行时起来之后才有。 */
  plugins: [] as PluginView[],
  pluginsLoading: false,
  pluginsError: "",
  contributions: { mcpServers: {}, skills: [], computerUse: false } as PluginContributionsView,
  /** 每个插件的配置项，点开时才拉。 */
  pluginConfigs: {} as Record<string, PluginConfigFieldView[]>,
  channels: [] as ChannelStatusView[],
  bindings: [] as ChannelBindingView[],
  /** 检查更新的结果。null 表示还没查过。 */
  update: null as UpdateStatusView | null,
  /**
   * 用户点过「以后再说」的版本号。**只记在内存里**——下次开应用会重新提醒
   * 一次，而攒一份持久的「永远别提醒」名单只会让人忘了自己还在用旧版本。
   */
  updateDismissed: "",
  /**
   * 有一次重挂被推迟了：用户在轮次跑着的时候改了 MCP / 技能 / 插件配置。
   * 跑着时不能卸会话（那一轮的事件会没有出口），轮次结束时补上这一次。
   */
  remountPending: false,
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

/** 侧边栏里那个会话叫什么，给错误条用。 */
function sessionLabel(sessionId: string): string {
  const found = state.sessions.find((session) => session.id === sessionId);
  return found?.title || "另一个会话";
}

export type { AppConfigView, TimelineEntry, LiveSession };

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
      state.providerId = state.config.providerId;
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

    window.aiclaw.on.agentEvent((payload) => {
      const applied = applyAgentEvent(state.live, payload as AgentEventPayload, state.sessionId);
      // 错误条是全局的：后台那个会话出错也要让人看见，不然它就静静地停了。
      if (applied.error) {
        const label = applied.sessionId === state.sessionId ? "" : `${sessionLabel(applied.sessionId)}：`;
        state.error = label + applied.error;
      }
      // 标题与轮次数变了，侧边栏跟一下。
      if (applied.method === "turn/completed") {
        void actions.refreshSessions();
        // 跑着时被推迟的重挂，现在补上（只补当前看着的会话；别的会话点开时自然会重挂）。
        if (state.remountPending && applied.sessionId === state.sessionId) {
          void actions.remountCurrentSession();
        }
      }
    });
    window.aiclaw.on.approval((payload) => {
      state.approvals.push(payload as ApprovalPayload);
    });
    window.aiclaw.on.runtimeStatus((payload) => {
      state.runtime = payload as RuntimeStatus;
      // 运行时一停，内存里跑着的轮次全没了；不清的话「正在执行」会一直亮着。
      if (state.runtime.state !== "ready") {
        for (const record of Object.values(state.live)) record.busy = false;
      }
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
    // 必须过 plain()：patch 里的嵌套对象（角色模型那一组）常常是从 store 里
    // 读出来再改的，而 store 是 readonly() 包过的——那些值是 Proxy，
    // 结构化克隆克隆不了，IPC 会直接抛。带原始值的 patch 一直没事，
    // 所以这个坑到有嵌套对象的配置项时才露出来。
    state.config = (await window.aiclaw.config.write(plain(patch))) as AppConfigView;
    // 还没开会话时顶部显示的就是默认模型，跟着配置走。
    if (!state.sessionId) {
      state.model = state.config.model;
      state.providerId = state.config.providerId;
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
   * 拉起本地运行时。
   *
   * 不需要先配好模型：模型服务的清单就在内核那边的库里，配置页要靠运行时
   * 才能读写它。所以应用一启动就拉，配没配模型只决定接下来开不开会话。
   */
  async startRuntime(): Promise<void> {
    state.error = "";
    try {
      await window.aiclaw.runtime.start();
      // start() 返回就是起来了。状态推送通常先到，但别赌顺序——App.vue 接下来
      // 就要按这个状态决定开不开会话。
      state.runtime = { state: "ready" };
      await actions.refreshSessions();
      await actions.loadProviders();
    } catch (error) {
      state.error = `启动本地运行时失败：${describeError(error)}`;
    }
  },

  /**
   * 接着上次的会话；一个都没有时才开新的。
   *
   * 接着上次而不是每次都开新的：每次都开新会话的话，侧边栏会攒出一串
   * 从没说过话的「未命名会话」。真想要新的，左上角就是「新对话」。
   */
  async resumeLatest(): Promise<void> {
    const latest = state.sessions[0];
    if (latest) await actions.openSession(latest.id);
    else await actions.newSession();
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
    state.providerId = info.providerId;
    state.live[info.sessionId] = newLive();
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
      state.providerId = info.providerId;
      // 正在跑的会话用它自己的活动记录：内核给的历史只到上一条已完成的消息，
      // 进行中的那几步只存在于事件流里，换成历史就把它们弄丢了。
      // 没在跑的以内核的历史为准——那是权威的，也覆盖了这期间排队进去的输入。
      if (!state.live[sessionId]?.busy) {
        state.live[sessionId] = newLive(restoreHistory(info.history ?? []));
      }
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
    delete state.live[sessionId];
    if (sessionId === state.sessionId) {
      state.sessionId = "";
      state.sessionInfo = null;
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
    await actions.remountCurrentSession();
  },

  /**
   * 让当前会话按现在的配置重挂一遍：MCP server、技能、插件、搜索引擎。
   *
   * 不做这一步的话，用户在 MCP 页加了一个 server，回到正在聊的会话里它并不在——
   * 只有开新会话或切到别的会话再切回来才会挂上，而界面上什么都不会说，
   * 用户只会得出「配了没用」。轮次正在跑时不动它：卸掉会让那一轮的事件没有出口。
   */
  async remountCurrentSession(): Promise<void> {
    if (!state.sessionId) return;
    if (state.busy) {
      // 现在不能动，记下来，turn/completed 时补。不记的话用户会得出「配了没用」，
      // 直到切一次会话才生效。
      state.remountPending = true;
      return;
    }
    state.remountPending = false;
    try {
      const info = (await window.aiclaw.session.resume(state.sessionId)) as SessionStartView;
      state.sessionInfo = info;
      state.model = info.model;
      state.providerId = info.providerId;
    } catch (error) {
      state.error = `重新挂载失败：${describeError(error)}`;
    }
  },

  /**
   * 打开模型在对话里提到的那个文件。
   *
   * 校验全在主进程：路径按会话工作区解析、必须真的存在、可执行的那几类只
   * 「在访达里显示」、凭据目录直接拒。这里只负责把结果说给用户听——
   * 点了没反应是最糟的，所以拒绝和找不到都要出现在错误条上。
   */
  /**
   * 读一个产出物用于内联显示。路径来自模型输出，校验在主进程。
   *
   * 读失败不进错误条：一张显示不出来的图旁边写一句原因就够了，不该盖住
   * 正在进行的对话。
   */
  async readMedia(path: string): Promise<{ dataUrl: string; kind: "image" | "audio" }> {
    return (await window.aiclaw.files.media(path)) as { dataUrl: string; kind: "image" | "audio" };
  },

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

  /**
   * 重扫技能目录。顺带把当前会话重挂一遍：用户往 ~/.aiclaw/skills 丢了一个新技能
   * 之后点的就是这个刷新，只更新列表不重挂的话，列表里有、会话里没有。
   */
  async refreshSkills(): Promise<void> {
    state.skills = (await window.aiclaw.skills.list()) as SkillView[];
    await actions.remountCurrentSession();
  },

  async toggleSkill(id: string, enabled: boolean): Promise<void> {
    state.skills = (await window.aiclaw.skills.toggle({ id, enabled })) as SkillView[];
    await actions.remountCurrentSession();
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

  // ---------- 搜索引擎 ----------

  async loadSearchEngines(): Promise<void> {
    state.searchLoading = true;
    state.searchError = "";
    try {
      state.searchEngines = (await window.aiclaw.search.list()) as SearchEngineView[];
    } catch (error) {
      state.searchError = describeError(error);
    } finally {
      state.searchLoading = false;
    }
  },

  async createSearchEngine(params: {
    provider: string;
    name?: string;
    baseUrl?: string;
    apiKey?: string;
    enabled?: boolean;
  }): Promise<SearchEngineView> {
    const created = (await window.aiclaw.search.create(params)) as SearchEngineView;
    await actions.loadSearchEngines();
    return created;
  },

  /** 没给的字段不动；apiKey 给空串表示清掉。 */
  async updateSearchEngine(params: {
    id: number;
    provider?: string;
    name?: string;
    baseUrl?: string;
    apiKey?: string;
    enabled?: boolean;
  }): Promise<void> {
    const updated = (await window.aiclaw.search.update(params)) as SearchEngineView;
    state.searchEngines = state.searchEngines.map((item) => (item.id === updated.id ? updated : item));
    // 启停或换 Key 会改变要不要挂搜索 server；改名字这类不会，但重挂一次无害。
    await actions.remountCurrentSession();
  },

  async deleteSearchEngine(id: number): Promise<void> {
    await window.aiclaw.search.remove(id);
    state.searchEngines = state.searchEngines.filter((item) => item.id !== id);
    await actions.remountCurrentSession();
  },

  async testSearchEngine(id: number, query: string): Promise<{ provider: string; results: SearchHitView[] }> {
    return (await window.aiclaw.search.test({ id, query })) as { provider: string; results: SearchHitView[] };
  },

  // ---------- 插件与通道 ----------

  /** 插件清单、贡献、通道状态与授权一起刷：插件页要的就是这四样。 */
  async loadPlugins(): Promise<void> {
    state.pluginsLoading = true;
    state.pluginsError = "";
    try {
      state.plugins = (await window.aiclaw.plugins.list()) as PluginView[];
      state.contributions = (await window.aiclaw.plugins.contributions()) as PluginContributionsView;
      state.channels = (await window.aiclaw.channels.status()) as ChannelStatusView[];
      state.bindings = (await window.aiclaw.channels.bindings()) as ChannelBindingView[];
    } catch (error) {
      state.pluginsError = describeError(error);
    } finally {
      state.pluginsLoading = false;
    }
  },

  async installPluginFromDirectory(): Promise<void> {
    const dir = (await window.aiclaw.dialog.pickDirectory()) as string | null;
    if (!dir) return;
    await window.aiclaw.plugins.install(dir);
    await actions.loadPlugins();
  },

  /**
   * 启停一个插件。启用即授权：内核会先检查必填配置齐了没有，缺了就拒绝，
   * 原因摆到错误条上。
   */
  async togglePlugin(uuid: string, enabled: boolean): Promise<void> {
    try {
      await window.aiclaw.plugins.toggle({ uuid, enabled });
    } catch (error) {
      state.error = describeError(error);
    }
    await actions.loadPlugins();
    // 技能页也跟着变：插件带的技能随插件启停出现或消失。
    await actions.refreshSkills();
    await actions.remountCurrentSession();
  },

  async deletePlugin(uuid: string): Promise<void> {
    try {
      await window.aiclaw.plugins.remove(uuid);
    } catch (error) {
      state.error = describeError(error);
    }
    await actions.loadPlugins();
  },

  async loadPluginConfig(uuid: string): Promise<void> {
    state.pluginConfigs[uuid] = (await window.aiclaw.plugins.config(uuid)) as PluginConfigFieldView[];
  },

  /** 空串表示清掉。存完重拉一遍，秘密只会以 isSet 的形式回来。 */
  async setPluginConfig(uuid: string, key: string, value: string): Promise<void> {
    try {
      await window.aiclaw.plugins.setConfig({ uuid, key, value });
    } catch (error) {
      state.error = describeError(error);
    }
    await actions.loadPluginConfig(uuid);
    await actions.loadPlugins();
  },

  async refreshChannels(): Promise<void> {
    try {
      state.channels = (await window.aiclaw.channels.status()) as ChannelStatusView[];
      state.bindings = (await window.aiclaw.channels.bindings()) as ChannelBindingView[];
    } catch (error) {
      state.pluginsError = describeError(error);
    }
  },

  /**
   * 放行一个外部会话。这是入站消息的同意环节——放行前那边发来的消息只被记下。
   * 模型与放开的工具在这里选，而不是沿用桌面会话的：发消息的人不是本机用户。
   */
  async authorizeBinding(input: {
    pluginUuid: string;
    channelId: string;
    externalKey: string;
    providerId: number;
    model: string;
    allowedTools: string[];
  }): Promise<void> {
    try {
      await window.aiclaw.channels.authorize(plain(input));
    } catch (error) {
      state.error = describeError(error);
    }
    await actions.refreshChannels();
  },

  async revokeBinding(key: { pluginUuid: string; channelId: string; externalKey: string }): Promise<void> {
    try {
      await window.aiclaw.channels.revoke(plain(key));
    } catch (error) {
      state.error = describeError(error);
    }
    await actions.refreshChannels();
  },

  async wechatLoginStart(): Promise<{ token: string; image: string }> {
    return (await window.aiclaw.wechat.loginStart()) as { token: string; image: string };
  },

  async wechatLoginPoll(uuid: string, token: string): Promise<{ status: string; saved: boolean }> {
    return (await window.aiclaw.wechat.loginPoll({ uuid, token })) as { status: string; saved: boolean };
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

  // ---------- 模型服务 ----------

  async loadProviders(): Promise<void> {
    state.providersLoading = true;
    state.providersError = "";
    try {
      state.providers = (await window.aiclaw.providers.list()) as ProviderView[];
    } catch (error) {
      // 拉不到清单不影响已经开着的会话，所以只标在用到它的那一块，
      // 不占用顶部的错误条。
      state.providersError = describeError(error);
    } finally {
      state.providersLoading = false;
    }
  },

  async createProvider(params: {
    name: string;
    type?: string;
    baseUrl?: string;
    apiKey?: string;
    models?: string[];
    enabled?: boolean;
  }): Promise<ProviderView> {
    const created = (await window.aiclaw.providers.create(params)) as ProviderView;
    await actions.loadProviders();
    return created;
  },

  /** 没给的字段不动；apiKey 给空串表示清掉。 */
  async updateProvider(params: {
    id: number;
    name?: string;
    type?: string;
    baseUrl?: string;
    apiKey?: string;
    models?: string[];
    enabled?: boolean;
  }): Promise<void> {
    const updated = (await window.aiclaw.providers.update(params)) as ProviderView;
    state.providers = state.providers.map((item) => (item.id === updated.id ? updated : item));
  },

  async deleteProvider(id: number): Promise<void> {
    await window.aiclaw.providers.remove(id);
    state.providers = state.providers.filter((item) => item.id !== id);
    // 默认模型指着被删的服务就清掉，别让配置页显示一个已经不存在的名字。
    if (state.config?.providerId === id) await actions.saveConfig({ providerId: 0, model: "" });
  },

  /** 到端点拉模型名。不落库——调用方决定要不要写进清单。 */
  async fetchProviderModels(id: number): Promise<string[]> {
    return (await window.aiclaw.providers.models(id)) as string[];
  },

  /** 按 models.dev 自动标记能力。只加不减，结果直接落库。 */
  async autoMarkProvider(
    id: number,
  ): Promise<{ matched: number; unmatched: number; guessed?: number; note?: string }> {
    const result = (await window.aiclaw.providers.autoMark(id)) as {
      provider: ProviderView;
      matched: number;
      unmatched: number;
      guessed?: number;
      note?: string;
    };
    state.providers = state.providers.map((item) =>
      item.id === result.provider.id ? result.provider : item,
    );
    return { matched: result.matched, unmatched: result.unmatched, guessed: result.guessed, note: result.note };
  },

  /**
   * 没设过默认模型、或设的那个已经不可用时，挑第一个能用的顶上。
   *
   * 配置页上的默认模型可以手动改，但新装的应用不该卡在「没选模型」上：
   * 用户在模型服务页填完端点、Key 与模型名，回到对话页就该能聊。
   * 能用 = 启用了、配了 Key、清单里至少有一个模型。
   */
  async ensureDefaultModel(): Promise<boolean> {
    if (state.providers.length === 0) await actions.loadProviders();
    const current = state.providers.find((item) => item.id === state.config?.providerId);
    if (current && usable(current) && state.config?.model) return true;
    const first = state.providers.find(usable);
    if (!first) return false;
    await actions.saveConfig({ providerId: first.id, model: first.models[0] ?? "" });
    return true;
  },

  /**
   * 换当前会话的模型（连模型服务一起）。只影响这个会话；新会话仍用配置页的默认值。
   *
   * 上下文窗口一起下发：不同模型窗口能差一个数量级，沿用上一个模型的值
   * 会让内核要么过早压缩、要么撑爆窗口。这里只知道默认模型的窗口，别的传 0。
   */
  async switchModel(providerId: number, modelId: string, contextWindow = 0): Promise<void> {
    if (!modelId || (modelId === state.model && providerId === state.providerId)) return;
    const previous = { model: state.model, providerId: state.providerId };
    state.model = modelId;
    state.providerId = providerId;
    if (!state.sessionId) return;
    // 窗口优先用清单里记着的；切回默认模型时退回配置里那个（用户可能手填过）。
    // 变量名别叫 window——那会遮蔽全局的 window，而下一行正要用它。
    const isDefault = modelId === state.config?.model && providerId === state.config?.providerId;
    const limit = contextWindow || (isDefault ? (state.config?.contextWindow ?? 0) : 0);
    try {
      await window.aiclaw.session.configure({
        sessionId: state.sessionId,
        providerId,
        model: modelId,
        contextWindow: limit,
      });
      await actions.refreshSessions();
    } catch (error) {
      // 切失败就把显示切回去，别让界面显示一个内核并没有在用的模型。
      state.model = previous.model;
      state.providerId = previous.providerId;
      state.error = `切换模型失败：${describeError(error)}`;
    }
  },

  // ---------- 对话 ----------

  /**
   * 发一条消息。轮次进行中也能发——内核会把它排进那一轮，模型下一次
   * 开口前就能看到。用户想纠正方向往往正是因为看见这一轮跑偏了，
   * 让他先等完一轮是最不该做的。
   */
  async send(
    text: string,
    images: string[] = [],
    audio: { name: string; data: string }[] = [],
  ): Promise<void> {
    if (!text.trim() && images.length === 0 && audio.length === 0) return;
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
    // 记住发出去的是哪个会话：下面有几次 await，期间用户可能已经切走了。
    const sessionId = state.sessionId;
    const record = ensureLive(state.live, sessionId);
    record.timeline.push({
      kind: "user",
      id: `user-${Date.now()}`,
      text,
      images: images.map((data) => `data:image/jpeg;base64,${data}`),
    });
    record.busy = true;
    try {
      // 音频先落盘：行协议单帧 16MB 装不下一段录音，所以交给内核的是路径。
      const audioPaths: string[] = [];
      for (const item of audio) {
        audioPaths.push((await window.aiclaw.audio.stage(plain(item))) as string);
      }
      await window.aiclaw.session.send({ sessionId, text, images, audioPaths });
    } catch (error) {
      record.busy = false;
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

  setView(view: "chat" | "providers" | "search" | "plugins" | "mcp" | "skills" | "settings"): void {
    state.view = view;
  },

  clearError(): void {
    state.error = "";
  },

  /** 把一条错误放到顶部的错误条上。给那些自己不管错误展示的页面用。 */
  showError(message: string): void {
    state.error = message;
  },
};

export { filterChoices, modelChoices, usable } from "./model-choices";
export type { ModelChoice } from "./model-choices";
