import { EventEmitter } from "node:events";
import { AgentTransport, type TransportOptions } from "./transport.js";
import type {
  AgentNotification,
  RoleModels,
  ApprovalRequestParams,
  ComputerRequestParams,
  ComputerResult,
  InitializeResult,
  Item,
  MCPProbeResult,
  MCPServerConfig,
  ChannelAuthorizeParams,
  ChannelBindingKey,
  ChannelBindingView,
  ChannelStatusView,
  PluginConfigField,
  PluginContributions,
  PluginView,
  ProviderAutoMarkResult,
  ProviderCreateParams,
  ProviderUpdateParams,
  ProviderView,
  SearchEngineCreateParams,
  SearchEngineTestResult,
  SearchEngineUpdateParams,
  SearchEngineView,
  WeChatLoginPollResult,
  WeChatLoginStartResult,
  SessionRefresh,
  SessionStartParams,
  SessionStartResult,
  ModelConfig,
  SessionHistoryResult,
  SessionSummary,
  TurnStartResult,
  BrowserRequestParams,
  BrowserResult,
} from "./protocol.js";

/** 待宿主决定的审批。id 用于回应。 */
/** 一次待宿主执行的屏幕操作。执行完必须调 respond 或 fail，不能不回。 */
export interface PendingComputerAction {
  request: ComputerRequestParams;
  respond: (result: ComputerResult) => void;
  fail: (message: string) => void;
}

export interface PendingBrowserAction {
  request: BrowserRequestParams;
  respond: (result: BrowserResult) => void;
  fail: (message: string) => void;
}

export interface PendingApproval extends ApprovalRequestParams {
  id: number | string;
}

export interface ClientEvents {
  notification: (notification: AgentNotification) => void;
  approval: (request: PendingApproval) => void;
  computer: (action: PendingComputerAction) => void;
  browser: (action: PendingBrowserAction) => void;
  stderr: (chunk: string) => void;
  exit: (code: number | null) => void;
}

export declare interface ClawAgentClient {
  on<E extends keyof ClientEvents>(event: E, listener: ClientEvents[E]): this;
  off<E extends keyof ClientEvents>(event: E, listener: ClientEvents[E]): this;
  emit<E extends keyof ClientEvents>(event: E, ...args: Parameters<ClientEvents[E]>): boolean;
}

/**
 * claw-agent 的类型化客户端。方法名与 Go 侧 protocol 包一致。
 */
export class ClawAgentClient extends EventEmitter {
  private readonly transport: AgentTransport;
  private initialized = false;

  constructor(options: TransportOptions) {
    super();
    this.transport = new AgentTransport(options);
    this.transport.on("notification", (method, params) => {
      this.emit("notification", { method, params } as AgentNotification);
    });
    this.transport.on("serverRequest", (id, method, params) => {
      if (method === "approval/request") {
        this.emit("approval", { id, ...(params as ApprovalRequestParams) });
        return;
      }
      if (method === "computer/request") {
        // 屏幕操作交给宿主做完再回。做不了也要回一个错误——
        // 不回那一轮会一直等到超时。
        this.emit("computer", {
          request: params as ComputerRequestParams,
          respond: (result: ComputerResult) => this.transport.respond(id, result),
          fail: (message: string) =>
            this.transport.respondError(id, { code: -32000, message }),
        });
        return;
      }
      if (method === "browser/request") {
        this.emit("browser", {
          request: params as BrowserRequestParams,
          respond: (result: BrowserResult) => this.transport.respond(id, result),
          fail: (message: string) =>
            this.transport.respondError(id, { code: -32000, message }),
        });
        return;
      }
      // 没实现的请求必须显式回错，不能不回——不回那一轮会一直等到超时。
      this.transport.respondError(id, { code: -32601, message: `unsupported request ${method}` });
    });
    this.transport.on("stderr", (chunk) => this.emit("stderr", chunk));
    this.transport.on("exit", (code) => {
      this.initialized = false;
      this.emit("exit", code);
    });
  }

  get running(): boolean {
    return this.transport.running;
  }

  async start(): Promise<InitializeResult> {
    this.transport.start();
    const result = await this.transport.request<InitializeResult>("initialize", {
      clientName: "aiclaw",
      clientVersion: "0.1.0",
    });
    this.initialized = true;
    return result;
  }

  async stop(): Promise<void> {
    await this.transport.stop();
  }

  sessionStart(params: SessionStartParams): Promise<SessionStartResult> {
    this.assertReady();
    return this.transport.request<SessionStartResult>("session/start", params);
  }

  /**
   * 恢复会话。refresh 给的是当前配置里「跟着配置走」的那几项——不给的话
   * 内核按存档恢复，用户后来加的 MCP server 与技能不会生效。
   */
  sessionResume(sessionId: string, refresh?: SessionRefresh): Promise<SessionStartResult> {
    this.assertReady();
    return this.transport.request<SessionStartResult>("session/resume", { sessionId, refresh });
  }

  /** 试连一个 MCP server 并列出它的工具。与会话无关，配置页用。 */
  mcpProbe(server: MCPServerConfig): Promise<MCPProbeResult> {
    this.assertReady();
    return this.transport.request<MCPProbeResult>("mcp/probe", { server });
  }

  // ---------- 模型服务 ----------
  //
  // 都是配置页上的同步操作。Key 从这里进（create / update），但永远不从这里出：
  // 内核只回 apiKeySet。

  async providerList(): Promise<ProviderView[]> {
    this.assertReady();
    const result = await this.transport.request<{ providers: ProviderView[] }>("provider/list", {});
    return result.providers ?? [];
  }

  providerCreate(params: ProviderCreateParams): Promise<ProviderView> {
    this.assertReady();
    return this.transport.request<ProviderView>("provider/create", params);
  }

  providerUpdate(params: ProviderUpdateParams): Promise<ProviderView> {
    this.assertReady();
    return this.transport.request<ProviderView>("provider/update", params);
  }

  providerDelete(id: number): Promise<unknown> {
    this.assertReady();
    return this.transport.request("provider/delete", { id });
  }

  /** 到该服务的端点拉一遍模型名。不落库，由界面决定要不要写进清单。 */
  async providerModels(id: number): Promise<string[]> {
    this.assertReady();
    const result = await this.transport.request<{ models: string[] }>("provider/models", { id });
    return result.models ?? [];
  }

  /** 按 models.dev 给这个服务的模型补上能力标记。只加不减。 */
  providerAutoMark(id: number): Promise<ProviderAutoMarkResult> {
    this.assertReady();
    return this.transport.request<ProviderAutoMarkResult>("provider/autoMark", { id });
  }

  // ---------- 搜索引擎 ----------

  async searchList(): Promise<SearchEngineView[]> {
    this.assertReady();
    const result = await this.transport.request<{ engines: SearchEngineView[] }>("search/list", {});
    return result.engines ?? [];
  }

  searchCreate(params: SearchEngineCreateParams): Promise<SearchEngineView> {
    this.assertReady();
    return this.transport.request<SearchEngineView>("search/create", params);
  }

  searchUpdate(params: SearchEngineUpdateParams): Promise<SearchEngineView> {
    this.assertReady();
    return this.transport.request<SearchEngineView>("search/update", params);
  }

  searchDelete(id: number): Promise<unknown> {
    this.assertReady();
    return this.transport.request("search/delete", { id });
  }

  /** 用一个引擎真搜一次，配置页的「试一下」。 */
  searchTest(id: number, query: string): Promise<SearchEngineTestResult> {
    this.assertReady();
    return this.transport.request<SearchEngineTestResult>("search/test", { id, query });
  }

  // ---------- 插件与通道 ----------
  //
  // 插件的记录与配置在内核那边的应用库里；秘密从这里进（setConfig），
  // 永远不从这里出。

  async pluginList(): Promise<PluginView[]> {
    this.assertReady();
    const result = await this.transport.request<{ plugins: PluginView[] }>("plugin/list", {});
    return result.plugins ?? [];
  }

  /** 从一个目录装插件。装好是停用的。 */
  pluginInstall(path: string): Promise<PluginView> {
    this.assertReady();
    return this.transport.request<PluginView>("plugin/install", { path });
  }

  pluginToggle(uuid: string, enabled: boolean): Promise<unknown> {
    this.assertReady();
    return this.transport.request("plugin/toggle", { uuid, enabled });
  }

  pluginDelete(uuid: string): Promise<unknown> {
    this.assertReady();
    return this.transport.request("plugin/delete", { uuid });
  }

  async pluginConfig(uuid: string): Promise<PluginConfigField[]> {
    this.assertReady();
    const result = await this.transport.request<{ fields: PluginConfigField[] }>("plugin/config", { uuid });
    return result.fields ?? [];
  }

  /** 空串表示清掉这一项。 */
  pluginSetConfig(uuid: string, key: string, value: string): Promise<unknown> {
    this.assertReady();
    return this.transport.request("plugin/setConfig", { uuid, key, value });
  }

  /** 启用中的插件贡献给会话的东西。开会话前拿一次。 */
  pluginContributions(): Promise<PluginContributions> {
    this.assertReady();
    return this.transport.request<PluginContributions>("plugin/contributions", {});
  }

  async channelStatus(): Promise<ChannelStatusView[]> {
    this.assertReady();
    const result = await this.transport.request<{ channels: ChannelStatusView[] }>("channel/status", {});
    return result.channels ?? [];
  }

  async channelBindings(): Promise<ChannelBindingView[]> {
    this.assertReady();
    const result = await this.transport.request<{ bindings: ChannelBindingView[] }>("channel/bindings", {});
    return result.bindings ?? [];
  }

  channelAuthorize(params: ChannelAuthorizeParams): Promise<unknown> {
    this.assertReady();
    return this.transport.request("channel/authorize", params);
  }

  channelRevoke(key: ChannelBindingKey): Promise<unknown> {
    this.assertReady();
    return this.transport.request("channel/revoke", key);
  }

  /**
   * 把角色配置推给内核，给通道会话（微信、企业微信）用。
   *
   * 那些会话是内核自己建的，拿不到这边的配置：不推的话用户发来的图没有视觉模型
   * 转述、语音文件没有听写。启动后与每次保存设置后各推一次。
   */
  channelMedia(roles: RoleModels): Promise<unknown> {
    this.assertReady();
    return this.transport.request("channel/media", { roles });
  }

  wechatLoginStart(): Promise<WeChatLoginStartResult> {
    this.assertReady();
    return this.transport.request<WeChatLoginStartResult>("wechat/loginStart", {});
  }

  wechatLoginPoll(uuid: string, token: string): Promise<WeChatLoginPollResult> {
    this.assertReady();
    return this.transport.request<WeChatLoginPollResult>("wechat/loginPoll", { uuid, token });
  }

  async sessionList(): Promise<SessionSummary[]> {
    this.assertReady();
    const result = await this.transport.request<{ sessions: SessionSummary[] }>("session/list", {});
    return result.sessions ?? [];
  }

  /** 按关键词找会话：标题与正文都找。关键词为空等于列全部。 */
  async sessionSearch(keyword: string): Promise<SessionSummary[]> {
    this.assertReady();
    const result = await this.transport.request<{ sessions: SessionSummary[] }>(
      "session/search",
      { keyword },
    );
    return result.sessions ?? [];
  }

  sessionDelete(sessionId: string): Promise<unknown> {
    this.assertReady();
    return this.transport.request("session/delete", { sessionId });
  }

  /** 取会话历史，还原成可直接渲染的时间线条目。 */
  async sessionHistory(sessionId: string): Promise<Item[]> {
    const result = await this.transport.request<SessionHistoryResult>("session/history", {
      sessionId,
    });
    return result.items ?? [];
  }

  /** 换掉会话正在用的模型。轮次进行中调用是安全的，下一次采样才生效。 */
  /**
   * 改一个已存在会话的模型或工作区。
   *
   * workspace 传 undefined 表示不动它；传空串表示清掉（回到「没设置」）。
   * model.model 为空表示这次只改工作区。
   */
  sessionConfigure(
    sessionId: string,
    model: ModelConfig,
    workspace?: string,
  ): Promise<{ model: ModelConfig }> {
    return this.transport.request("session/configure", { sessionId, model, workspace });
  }

  /**
   * 起一轮。images 是随这条消息发给模型的图片，base64 的 PNG/JPEG；
   * audioPaths 是磁盘上的音频文件，由内核用听写模型转成文字并进消息
   *（给路径不给字节：行协议单帧 16MB 装不下一段录音）。
   */
  turnStart(
    sessionId: string,
    text: string,
    images?: string[],
    audioPaths?: string[],
  ): Promise<TurnStartResult> {
    this.assertReady();
    return this.transport.request<{ turnId: string }>("turn/start", {
      sessionId, text, images, audioPaths,
    });
  }

  turnInterrupt(sessionId: string): Promise<unknown> {
    this.assertReady();
    return this.transport.request("turn/interrupt", { sessionId });
  }

  /** 回应一次审批。 */
  /**
   * 回应一次审批。
   *
   * scope 为 "session" 表示「本次会话这个目录都允许」——只有审批请求带了
   * scopePath 时才有意义，内核据此记下授权。不给就是只这一次。
   */
  respondApproval(id: number | string, approved: boolean, scope?: "once" | "session"): void {
    this.transport.respond(id, { approved, scope });
  }

  private assertReady(): void {
    if (!this.initialized) {
      throw new Error("claw-agent 尚未初始化；先调用 start()");
    }
  }
}
