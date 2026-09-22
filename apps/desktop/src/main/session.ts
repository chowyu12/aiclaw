import { EventEmitter } from "node:events";
import { existsSync, mkdirSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import {
  ClawAgentClient,
  type AgentNotification,
  type ApprovalPolicy,
  type Item,
  type MCPProbeResult,
  type ChannelAuthorizeParams,
  type ChannelBindingKey,
  type ChannelBindingView,
  type ChannelStatusView,
  type MCPServerConfig,
  type PendingApproval,
  type PluginConfigField,
  type PluginContributions,
  type PluginView,
  type ProviderCreateParams,
  type ProviderUpdateParams,
  type ProviderView,
  type SessionRefresh,
  type SessionStartParams,
  type SessionSummary,
  type WeChatLoginPollResult,
  type WeChatLoginStartResult,
} from "@aiclaw/agent-client";
import type { AppConfig, ConfigStore, McpServer } from "./config.js";
import { ComputerController } from "./computer.js";
import type { SkillManager } from "./skills.js";

/**
 * 审批档位。**本版本不做 OS 级沙箱**（已确认的产品决定），
 * 所以档位只剩审批策略这一个维度，没有「沙箱模式」可选。
 *
 * 只在这里定义一次，UI 从这里取，避免界面写的与实际下发的策略漂移。
 */
export interface ProfileSpec {
  id: ApprovalPolicy;
  label: string;
  description: string;
  /** 定时任务这类无人值守场景能否选。 */
  availableForScheduled: boolean;
}

export const PROFILES: ProfileSpec[] = [
  {
    id: "on-write",
    label: "默认",
    description: "执行命令和调用外部工具前请你确认；读文件、写工作目录内的文件不问。",
    availableForScheduled: false,
  },
  {
    id: "always",
    label: "严格",
    description: "所有有副作用的操作（含写文件）都先请你确认。",
    availableForScheduled: false,
  },
  {
    id: "never",
    label: "无人值守",
    description: "不弹确认；需要确认的操作直接失败。给定时任务用，别在交互会话里选。",
    availableForScheduled: true,
  },
];

/** 一个会话起来之后宿主需要知道的东西。 */
export interface SessionInfo {
  sessionId: string;
  tools: string[];
  mcpStatus: Record<string, string>;
  /** 会话工作区。空串表示没设置。 */
  workspace: string;
  /** 这个会话当前用的模型。恢复旧会话时可能与配置页的默认值不同。 */
  model: string;
  /** 模型所属的模型服务 id；0 表示走环境变量的 Key。 */
  providerId: number;
  /** 本次会话挂上的技能名。 */
  skills: string[];
  /** 恢复旧会话时带回的时间线；新会话为空。 */
  history?: Item[];
}

export interface SessionManagerEvents {
  /** 透传给渲染层的 agent 事件。 */
  event: (method: string, params: unknown) => void;
  approval: (request: PendingApproval) => void;
  status: (status: "starting" | "ready" | "stopped" | "failed", detail?: string) => void;
  /** 一段要进诊断日志的输出。source 是 kernel / app。 */
  log: (source: string, chunk: string) => void;
}

export declare interface SessionManager {
  on<E extends keyof SessionManagerEvents>(event: E, listener: SessionManagerEvents[E]): this;
  emit<E extends keyof SessionManagerEvents>(
    event: E,
    ...args: Parameters<SessionManagerEvents[E]>
  ): boolean;
}

/**
 * 管理本机 claw-agent 进程的生命周期，并把应用配置翻译成会话配置。
 *
 * 一个应用进程只拉起一个 claw-agent；会话是它内部的概念。
 */
export class SessionManager extends EventEmitter {
  private client: ClawAgentClient | null = null;
  private readonly store: ConfigStore;
  /** 技能发现归它管——会话启动时问一次「现在有哪些启用的技能」。 */
  private readonly skills: SkillManager;
  private readonly computer = new ComputerController();
  private readonly agentBin: string;
  /** 最近一段 stderr，失败时拼进错误信息，省得用户去翻日志。 */
  private lastStderr = "";
  /** 最近一次会话的挂载结果，诊断报告里要。 */
  private mounts: Record<string, string> = {};
  /** 最近一次会话的工作区，诊断报告里要。 */
  private workspace = "";

  constructor(store: ConfigStore, skills: SkillManager) {
    super();
    this.store = store;
    this.skills = skills;
    this.agentBin = resolveBin("CLAW_AGENT_BIN", "claw-agent");
  }

  get running(): boolean {
    return this.client?.running ?? false;
  }

  async start(): Promise<void> {
    if (this.client) return;

    this.emit("status", "starting");

    // 模型 Key 不经这里：内核按会话的 providerId 到 --app-db 那个库里查。
    const client = new ClawAgentClient({
      command: this.agentBin,
      args: ["serve", `--data-home=${this.store.agentHome}`, `--app-db=${this.store.appDbPath}`],
      env: { ...process.env },
    });

    client.on("notification", (n: AgentNotification) => this.emit("event", n.method, n.params));
    client.on("approval", (request) => this.emit("approval", request));
    // 屏幕操作在宿主这边做：截屏要走应用自己的屏幕录制授权，输入要按平台
    // 合成事件，而且只有宿主知道「最前面的应用是不是我自己」。
    client.on("computer", async ({ request, respond, fail }) => {
      try {
        respond(await this.computer.perform(request));
      } catch (error) {
        // 失败也必须回：不回那一轮会一直等到超时。原因原样带给模型，
        // 它据此改做法（比如先去授权、或者换个别的办法）。
        fail(error instanceof Error ? error.message : String(error));
      }
    });
    client.on("stderr", (chunk) => {
      this.lastStderr = (this.lastStderr + chunk).slice(-2000);
      // 同一段也进诊断日志：lastStderr 只留最后 2000 字且启动成功后就没人看了，
      // 而「昨天它报过一句什么」正是排查时要的。
      this.emit("log", "kernel", chunk.toString());
    });
    client.on("exit", (code) => {
      this.client = null;
      this.emit("status", code === 0 ? "stopped" : "failed", this.lastStderr);
    });

    try {
      await client.start();
      this.client = client;
      this.emit("status", "ready");
    } catch (error) {
      await client.stop().catch(() => undefined);
      this.emit("status", "failed", `${String(error)}\n${this.lastStderr}`);
      throw error;
    }
  }

  async stop(): Promise<void> {
    const client = this.client;
    this.client = null;
    await client?.stop();
  }

  /**
   * 开一个新会话，返回 sessionId 与挂载结果。
   *
   * 模型用配置页的默认值——顶部切模型只改当前会话，新会话回到默认值，
   * 这是刻意的：一次临时换模型不该悄悄变成长期设置。
   *
   * workspace 默认是**空的**：新会话不预设工作区，用户想好了再指。
   * 早先这里用一个全局工作目录，结果是所有会话共用一个目录，
   * 而用户真正想让 Agent 干活的地方在别处——他得先把文件搬进来。
   */
  async startSession(workspace?: string): Promise<SessionInfo> {
    const client = this.requireClient();
    const config = this.store.readConfig();
    const params = await this.buildParams(config, workspace);
    if (params.workdir) mkdirSync(params.workdir, { recursive: true });
    this.workspace = params.workdir ?? "";
    this.skills.setWorkspace(this.workspace);
    const result = await client.sessionStart(params);
    this.mounts = result.mcpStatus ?? {};
    return {
      sessionId: result.sessionId,
      tools: result.tools,
      mcpStatus: result.mcpStatus ?? {},
      workspace: result.workspace ?? "",
      model: params.model.model,
      providerId: result.providerId ?? 0,
      skills: result.skills ?? [],
    };
  }

  /**
   * 恢复一个会话，连同它的历史一起返回。
   *
   * 历史必须一起给：内核存的是给模型看的消息，宿主自己没留时间线，
   * 不还原的话点开旧会话是一片空白，看起来像记录丢了。
   */
  async resumeSession(sessionId: string): Promise<SessionInfo> {
    const client = this.requireClient();
    const result = await client.sessionResume(sessionId, await this.buildRefresh());
    // 端点与 Key 不用在这里对齐：会话记的是 providerId，内核恢复时按 id 到库里
    // 取**当前**的端点与 Key。用户改了端点，旧会话点开就是新地址。
    this.mounts = result.mcpStatus ?? {};
    // 技能发现要看这个会话的工作区（项目级 .claude/skills）。
    this.workspace = result.workspace ?? "";
    this.skills.setWorkspace(this.workspace);
    return {
      sessionId,
      tools: result.tools,
      mcpStatus: result.mcpStatus ?? {},
      workspace: result.workspace ?? "",
      model: result.model ?? this.store.readConfig().model,
      providerId: result.providerId ?? 0,
      skills: result.skills ?? [],
      history: await client.sessionHistory(sessionId),
    };
  }

  /**
   * 换当前会话的模型（可以连模型服务一起换）。
   *
   * 上下文窗口跟着一起下发：不同模型窗口差一个数量级，沿用上一个模型的值
   * 会让内核要么过早压缩、要么撑爆窗口。不知道就传 0，内核退回「等上游报错再压」。
   */
  async configureSession(
    sessionId: string,
    providerId: number,
    model: string,
    contextWindow: number,
  ): Promise<void> {
    const config = this.store.readConfig();
    await this.requireClient().sessionConfigure(sessionId, {
      providerId: providerId || undefined,
      baseUrl: "",
      model,
      reasoningEffort: config.reasoningEffort || undefined,
      contextWindow: contextWindow || undefined,
    });
  }

  async sendTurn(input: {
    sessionId: string;
    text: string;
    images?: string[];
  }): Promise<string> {
    const result = await this.requireClient().turnStart(input.sessionId, input.text, input.images);
    return result.turnId;
  }

  async interrupt(sessionId: string): Promise<void> {
    await this.requireClient().turnInterrupt(sessionId);
  }

  listSessions(): Promise<SessionSummary[]> {
    return this.requireClient().sessionList();
  }

  /**
   * 改一个会话的工作区。传空串表示清掉（回到「没设置」）。
   *
   * 中途能改是刻意的：用户开着会话聊着聊着才想起「这事要在那个仓库里做」，
   * 让他为此新开一个会话、把上文再说一遍，只是把界面问题变成他的负担。
   */
  async setWorkspace(sessionId: string, workspace: string): Promise<string> {
    const client = this.requireClient();
    const trimmed = workspace.trim();
    // model 留空表示「这次只改工作区」，内核那边据此跳过换模型。
    await client.sessionConfigure(sessionId, { baseUrl: "", model: "" }, trimmed);
    this.workspace = trimmed;
    this.skills.setWorkspace(trimmed);
    return trimmed;
  }

  /** 最近一次会话的工作区。诊断报告用。 */
  currentWorkspace(): string {
    return this.workspace;
  }

  /** 最近一次会话的 MCP 挂载结果。诊断报告用。 */
  lastMounts(): Record<string, string> {
    return this.mounts;
  }

  searchSessions(keyword: string): Promise<SessionSummary[]> {
    return this.requireClient().sessionSearch(keyword);
  }

  async deleteSession(sessionId: string): Promise<void> {
    await this.requireClient().sessionDelete(sessionId);
  }

  approve(request: PendingApproval, approved: boolean, scope?: "once" | "session"): void {
    this.requireClient().respondApproval(request.id, approved, scope);
  }

  /**
   * 把应用配置翻译成会话配置。
   *
   * 插件的贡献在这里并进来：启用中的插件带的 MCP server、技能目录，
   * 以及 computer-use 插件是否启用——那个开关只有这一处，配置页上没有第二个。
   */
  private async buildParams(config: AppConfig, workspace?: string): Promise<SessionStartParams> {
    const contributions = await this.contributions();
    const mcpServers = this.buildMcpServers(contributions);
    return {
      model: {
        // 端点与 Key 由内核按 providerId 查；这里不传 baseUrl。
        providerId: config.providerId || undefined,
        baseUrl: "",
        model: config.model,
        reasoningEffort: config.reasoningEffort || undefined,
        contextWindow: config.contextWindow || undefined,
      },
      workdir: (workspace ?? "").trim(),
      approvalPolicy: config.profile,
      // 应用自己存凭据的目录：模型读它没有任何正当用途，而读到之后
      // 可以顺着任何一个对外的工具送出去。硬拒绝，不是「问一句」。
      protectedPaths: [this.store.dataDir],
      // 配置里是「开沙箱」，协议里是「关沙箱」：零值要落在安全那一侧。
      disableSandbox: config.sandboxCommands === false,
      codeMode: config.codeMode === true,
      mcpServers,
      skillDirs: this.skills.enabledDirs(),
      memoryFile: this.store.memoryFile,
      enableComputerUse: contributions.computerUse,
    };
  }

  /**
   * 向内核要一份启用中的插件贡献，并把技能目录交给 SkillManager。
   *
   * 每次开会话都重新要：用户刚在插件页开了一个，下一个会话就该带上。
   * 拿不到（内核报错）就当没有插件——开会话不该因为插件系统的一个错误而失败。
   */
  private async contributions(): Promise<PluginContributions> {
    let contributions: PluginContributions = { mcpServers: {}, skills: [], computerUse: false };
    try {
      contributions = await this.requireClient().pluginContributions();
    } catch (error) {
      this.emit("log", "app", `读取插件贡献失败：${String(error)}`);
    }
    this.skills.setPluginSkills(contributions.skills);
    return contributions;
  }

  /**
   * 拼出这次要挂的全部 MCP server。
   *
   * 每次开会话都重新拼：用户在 MCP 页改完，下一次挂载就生效。
   */
  private buildMcpServers(contributions: PluginContributions): Record<string, MCPServerConfig> {
    // 插件带的 server 先进：它们的代码随插件一起被用户装进来并启用了，
    // 但仍是第三方的——不标 trusted，照常走审批。
    const mcpServers: Record<string, MCPServerConfig> = { ...contributions.mcpServers };
    // 名字会成为工具名前缀——撞名时内核会在挂载阶段报重复注册，比静默遮蔽好查。
    for (const server of this.store.readMcpServers()) {
      if (!server.enabled) continue;
      // 第三方 server 不标 trusted：来源与副作用都未知，照常走审批
      //（声明了 readOnlyHint 的工具由内核自己放过）。
      mcpServers[server.label || server.id] =
        server.transport === "http"
          ? { url: server.url, headers: server.headers }
          : { command: server.command, args: server.args, env: server.env };
    }
    return mcpServers;
  }

  /**
   * 恢复会话时要用当前配置盖掉存档的那几项。
   *
   * 存档里的是**建会话那一刻**的配置，而应用一启动就接着上次的会话：
   * 不盖的话，用户后来加的 MCP server、新装的技能
   * 一个都挂不上，而界面上什么都不会说——用户只会得出「配了没用」。
   * 工作目录和模型不在里面，理由见 protocol 里 SessionRefresh 的说明。
   */
  private async buildRefresh(): Promise<SessionRefresh> {
    const config = this.store.readConfig();
    const contributions = await this.contributions();
    return {
      mcpServers: this.buildMcpServers(contributions),
      skillDirs: this.skills.enabledDirs(),
      memoryFile: this.store.memoryFile,
      enableComputerUse: contributions.computerUse,
      disableSandbox: config.sandboxCommands === false,
      codeMode: config.codeMode === true,
      approvalPolicy: config.profile,
    };
  }

  // ---------- 模型服务 ----------
  //
  // 配置页的增删改查直接透传给内核：库在内核那边打开着，宿主不碰 SQLite，
  // Key 也就从来不经过宿主的内存。

  listProviders(): Promise<ProviderView[]> {
    return this.requireClient().providerList();
  }

  createProvider(params: ProviderCreateParams): Promise<ProviderView> {
    return this.requireClient().providerCreate(params);
  }

  updateProvider(params: ProviderUpdateParams): Promise<ProviderView> {
    return this.requireClient().providerUpdate(params);
  }

  async deleteProvider(id: number): Promise<void> {
    await this.requireClient().providerDelete(id);
  }

  fetchProviderModels(id: number): Promise<string[]> {
    return this.requireClient().providerModels(id);
  }

  // ---------- 插件与通道 ----------
  //
  // 记录与配置在内核那边的应用库里，这里只透传。

  listPlugins(): Promise<PluginView[]> {
    return this.requireClient().pluginList();
  }

  installPlugin(path: string): Promise<PluginView> {
    return this.requireClient().pluginInstall(path);
  }

  async togglePlugin(uuid: string, enabled: boolean): Promise<void> {
    await this.requireClient().pluginToggle(uuid, enabled);
  }

  async deletePlugin(uuid: string): Promise<void> {
    await this.requireClient().pluginDelete(uuid);
  }

  pluginConfig(uuid: string): Promise<PluginConfigField[]> {
    return this.requireClient().pluginConfig(uuid);
  }

  async setPluginConfig(uuid: string, key: string, value: string): Promise<void> {
    await this.requireClient().pluginSetConfig(uuid, key, value);
  }

  pluginContributions(): Promise<PluginContributions> {
    return this.contributions();
  }

  channelStatus(): Promise<ChannelStatusView[]> {
    return this.requireClient().channelStatus();
  }

  channelBindings(): Promise<ChannelBindingView[]> {
    return this.requireClient().channelBindings();
  }

  async authorizeChannel(params: ChannelAuthorizeParams): Promise<void> {
    await this.requireClient().channelAuthorize(params);
  }

  async revokeChannel(key: ChannelBindingKey): Promise<void> {
    await this.requireClient().channelRevoke(key);
  }

  wechatLoginStart(): Promise<WeChatLoginStartResult> {
    return this.requireClient().wechatLoginStart();
  }

  wechatLoginPoll(uuid: string, token: string): Promise<WeChatLoginPollResult> {
    return this.requireClient().wechatLoginPoll(uuid, token);
  }

  /** 试连一个 MCP server 并列出它的工具。配置页用，与会话无关。 */
  async probeMcp(server: McpServer): Promise<MCPProbeResult> {
    const client = this.requireClient();
    return client.mcpProbe(
      server.transport === "http"
        ? { url: server.url, headers: server.headers }
        : { command: server.command, args: server.args, env: server.env },
    );
  }

  static profiles(): ProfileSpec[] {
    return PROFILES;
  }

  private requireClient(): ClawAgentClient {
    if (!this.client) {
      throw new Error("本地运行时未启动");
    }
    return this.client;
  }
}

/**
 * 定位随应用分发的 Go 二进制。
 *
 * 开发时在仓库里，打包后在 app 的 resources 下。环境变量优先，便于本机调试
 * 指向刚编出来的那个。找不到时退回裸命令名交给 PATH，真正的失败由启动报错说明。
 */
function resolveBin(envVar: string, name: string): string {
  const override = process.env[envVar];
  if (override) return override;

  // Windows 上可执行文件带 .exe。打包时 extraResource 原样放进 Resources，
  // 这里不补后缀的话打出来的 Windows 包一启动就是「找不到 claw-agent」。
  const exe = process.platform === "win32" ? `${name}.exe` : name;
  const here = dirname(fileURLToPath(import.meta.url));
  const candidates = [
    join(process.resourcesPath ?? "", exe),
    // 开发：apps/desktop/dist/main → 仓库根 → tools/<name>/<name>
    resolve(here, `../../../../tools/${name}/${exe}`),
  ];
  for (const candidate of candidates) {
    if (candidate && existsSync(candidate)) return candidate;
  }
  return exe;
}
