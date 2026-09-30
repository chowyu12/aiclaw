import { EventEmitter } from "node:events";
import { execFile, execFileSync } from "node:child_process";
import { randomBytes } from "node:crypto";
import { existsSync, mkdirSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { homedir } from "node:os";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { clipboard, shell } from "electron";
import { toolPath } from "./shell-path.js";
import {
  ClawAgentClient,
  type AgentNotification,
  type PendingUserInput,
  type PendingScheduleAction,
  type ChannelConnectionView,
  type UsageSummary,
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
  type ProviderAutoMarkResult,
  type ProviderCreateParams,
  type ProviderUpdateParams,
  type ProviderView,
  type RoleModels,
  type SearchEngineCreateParams,
  type EmailTestResult,
  type SearchEngineTestResult,
  type SearchEngineUpdateParams,
  type SearchEngineView,
  type SessionRefresh,
  type SessionStartParams,
  type SessionSummary,
  type WeChatLoginPollResult,
  type WeChatLoginStartResult,
} from "@aiclaw/agent-client";
import type { AppConfig, ConfigStore, McpServer } from "./config.js";
import { ComputerController } from "./computer.js";
import { AgentBrowser } from "./browser.js";
import { ExtensionBridge } from "./browser-bridge.js";
import { browserName } from "./browser-cdp.js";
import type { BrowserBridgeView } from "../shared/types.js";
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
    description: "不弹确认；需要确认的操作直接失败。给定时任务与外部通道用，别在交互会话里选。",
    availableForScheduled: true,
  },
  {
    id: "bypass",
    label: "全部放行",
    description: "不弹确认；需要确认的操作直接执行（rm -rf / 这类危险命令仍硬拒绝）。只在完全信任当前任务时用。",
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
  /** 代码模式下被收进 exec 的工具名。 */
  foldedTools: string[];
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
  /** 浏览器扩展的连接状态变了（连上、断开、端口被占）。 */
  browserBridge: (view: BrowserBridgeView) => void;
  /** 模型向用户提了一个问题（ask_user），等回答。 */
  userInput: (question: PendingUserInput) => void;
  /** 模型要建、列、删定时任务（schedule_* 工具）。 */
  schedule: (action: PendingScheduleAction) => void;
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
  /** 与用户浏览器里「AIClaw 浏览器助手」扩展的连接。只在选了「用我的浏览器」时监听。 */
  private readonly bridge: ExtensionBridge;
  private readonly browser: AgentBrowser;
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
    this.bridge = new ExtensionBridge(() => this.store.readConfig().browserPairToken);
    this.bridge.on("status", () => this.emit("browserBridge", this.browserBridge()));
    this.bridge.on("log", (line: string) => this.emit("log", "app", `${line}\n`));
    this.browser = new AgentBrowser(() => this.store.readConfig().browserBackend, this.bridge);
    this.agentBin = resolveBin("CLAW_AGENT_BIN", "claw-agent");
  }

  get running(): boolean {
    return this.client?.running ?? false;
  }

  /**
   * 拉起内核。已经在跑就直接返回；**正在拉的时候再调，等的是同一次**。
   *
   * this.client 要等内核握手完才赋值，早先只看它：启动那几秒里渲染层重新加载
   * 又调一次 start()，就会再起一个内核——两个进程开同一个会话库，各自连一遍
   * 微信和企业微信。
   */
  start(): Promise<void> {
    if (this.client) return Promise.resolve();
    if (!this.starting) {
      this.starting = this.launch().finally(() => {
        this.starting = null;
      });
    }
    return this.starting;
  }

  private starting: Promise<void> | null = null;
  /** 给内核的 PATH，第一次启动时从登录 shell 读一次。见 shell-path.ts。 */
  private path: string | undefined;

  private async launch(): Promise<void> {
    this.emit("status", "starting");

    // 模型 Key 不经这里：内核按会话的 providerId 到 --app-db 那个库里查。
    // PATH 换成终端里那样的：模型跑的命令、技能调的 CLI、stdio 的 MCP server 都从内核继承它。
    this.path ??= await toolPath(homedir());
    const client = new ClawAgentClient({
      command: this.agentBin,
      args: ["serve", `--data-home=${this.store.agentHome}`, `--app-db=${this.store.appDbPath}`],
      env: { ...process.env, PATH: this.path },
    });

    client.on("notification", (n: AgentNotification) => this.emit("event", n.method, n.params));
    client.on("approval", (request) => this.emit("approval", request));
    client.on("userInput", (question) => this.emit("userInput", question));
    // 模型要建、列、删定时任务：调度器在主进程，交给它。
    client.on("schedule", (action) => this.emit("schedule", action));
    // 屏幕操作在宿主这边做：截屏要走应用自己的屏幕录制授权，输入要按平台
    // 合成事件，而且只有宿主知道「最前面的应用是不是我自己」。
    // 浏览器窗口是宿主的：加载、点、填、截图都在这边做，内核只发请求。
    client.on("browser", async ({ request, respond, fail }) => {
      try {
        respond(await this.browser.perform(request));
      } catch (error) {
        fail(error instanceof Error ? error.message : String(error));
      }
    });
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
      await this.syncChannelMedia();
    } catch (error) {
      await client.stop().catch(() => undefined);
      this.emit("status", "failed", `${String(error)}\n${this.lastStderr}`);
      throw error;
    }
  }

  /**
   * 把当前的角色配置推给内核，给通道会话用（见 agent-client 的 channelMedia）。
   *
   * 失败只记一笔：推不过去的后果是通道会话暂时用不上视觉旁路，不该连累启动
   * 或者保存设置。
   */
  async syncChannelMedia(): Promise<void> {
    const client = this.client;
    if (!client) return;
    try {
      await client.channelMedia(toRoles(this.store.readConfig()));
    } catch (error) {
      this.emit("log", "kernel", `推送通道角色配置失败：${String(error)}\n`);
    }
  }

  /**
   * 按配置启停与浏览器扩展的连接：开了浏览器工具、选了「用我的浏览器」才监听。
   * 第一次选时生成配对码。启动时与每次保存设置后调。
   */
  async syncBrowserBridge(): Promise<void> {
    const config = this.store.readConfig();
    if (!config.browser || config.browserBackend !== "extension") {
      this.bridge.stop();
      this.emit("browserBridge", this.browserBridge());
      return;
    }
    if (!config.browserPairToken) this.store.writeConfig({ browserPairToken: newPairToken() });
    await this.bridge.start();
  }

  /** 最近 days 天的用量（设置 → 用量）。 */
  usageSummary(days: number): Promise<UsageSummary> {
    return this.requireClient().usageSummary(days);
  }

  /** 换一个配对码：旧的立刻作废，已连上的扩展断开，要在扩展里重新填。 */
  regeneratePairToken(): string {
    const token = newPairToken();
    this.store.writeConfig({ browserPairToken: token });
    this.bridge.disconnect();
    return token;
  }

  browserBridge(): BrowserBridgeView {
    const status = this.bridge.status();
    return {
      listening: status.listening,
      port: status.port,
      error: status.error,
      browser: status.connected ? browserName(status.connected.userAgent) : "",
      extensionVersion: status.connected?.extension ?? "",
      extensionDir: extensionDir(),
      pairingCode: status.pairing?.code ?? "",
      browsers: installedBrowsers().map(({ id, name, isDefault, storeUrl }) => ({
        id,
        name,
        isDefault,
        fromStore: storeUrl !== "",
      })),
    };
  }

  /**
   * 引导安装扩展：用选中的浏览器打开它的扩展管理页，在访达里显示扩展目录，并把目录
   * 路径放进剪贴板——「加载已解压的扩展程序」的选择框里 Cmd+Shift+G 粘贴就到了，
   * 不用在 .app 里一层层点（选择框默认进不了 .app 内部）。
   */
  async openExtensionPage(browserId: string): Promise<void> {
    const browsers = installedBrowsers();
    // 没指定就用默认浏览器（排在第一个）。
    const browser = browserId ? browsers.find((item) => item.id === browserId) : browsers[0];
    if (!browser) throw new Error(browserId ? "没找到这个浏览器" : "没找到能装扩展的浏览器（Chrome / Edge / Brave / Arc）");
    if (browser.storeUrl) {
      // 上架之后：打开商店页，用户点「获取」就装好了，之后自动弹配对页。
      await run("open", ["-a", browser.app, browser.storeUrl]);
      return;
    }
    const dir = extensionDir();
    clipboard.writeText(dir);
    await shell.openPath(dir);
    // 经 `open -a` 交给系统启动服务：浏览器在跑就在它里面开一个标签页，没跑就先启动它。
    await run("open", ["-a", browser.app, browser.extensionsPage]).catch(async () => {
      // 有的浏览器不接受从外面打开内部页：至少把它带到前台，用户自己在地址栏输入。
      await run("open", ["-a", browser.app]).catch(() => undefined);
    });
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
      foldedTools: result.foldedTools ?? [],
    };
  }

  /**
   * 给定时任务开一个后台会话并发出第一条消息。
   *
   * 与 startSession 的区别：不动「当前会话」那几样状态（工作区、挂载结果）——用户
   * 可能正在看着别的会话，定时任务在旁边跑，不该把他那边的技能发现和状态栏换掉。
   */
  async runBackgroundSession(input: { workspace: string; title: string; text: string }): Promise<string> {
    await this.start();
    const client = this.requireClient();
    const params = await this.buildParams(this.store.readConfig(), input.workspace);
    params.title = input.title;
    if (params.workdir) mkdirSync(params.workdir, { recursive: true });
    const result = await client.sessionStart(params);
    await client.turnStart(result.sessionId, input.text);
    return result.sessionId;
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
      foldedTools: result.foldedTools ?? [],
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
    audioPaths?: string[];
  }): Promise<string> {
    const result = await this.requireClient().turnStart(
      input.sessionId, input.text, input.images, input.audioPaths,
    );
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

  /** 删会话，连同它开出的子 agent。返回删掉的全部 id。 */
  deleteSession(sessionId: string): Promise<string[]> {
    return this.requireClient().sessionDelete(sessionId);
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
    const mcpServers = await this.buildMcpServers(contributions);
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
      enableEmail: contributions.email === true,
      enableSchedule: true,
      enableBrowser: config.browser === true,
      roles: toRoles(config),
      modelSeesImages: await this.modelSeesImages(config),
    };
  }

  /**
   * 对话模型自己看不看得懂图。
   *
   * 看模型清单里的 `#vision` 标记。拿不到清单时按「看得懂」处理：那时走旁路
   * 会把每张图都转成转述，而多数主流模型本来就认图——宁可让不认图的模型
   * 报一次上游错误，也不要让认图的模型永远只读到二手描述。
   */
  private async modelSeesImages(config: AppConfig): Promise<boolean> {
    if (!config.providerId || !config.model) return true;
    try {
      const provider = (await this.listProviders()).find((item) => item.id === config.providerId);
      if (!provider) return true;
      // 清单项形如 `名字#vision,image@200000`：窗口（@…）要先切掉再看标记。
      // 早先没切，`deepseek-v4.1-flash#vision@1050000` 的标记读成了「vision@1050000」，
      // 于是凡是写了窗口的模型都被当成不认图，每张图都白走一趟视觉旁路。
      // 规则与内核 protocol.ParseModelMark、渲染层 parseModelMark 一致。
      const entry = provider.models.find((item) => markedName(item) === config.model);
      if (entry === undefined) return true;
      return markedRoles(entry).includes("vision");
    } catch {
      return true;
    }
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
  private async buildMcpServers(contributions: PluginContributions): Promise<Record<string, MCPServerConfig>> {
    // 插件带的 server 先进：它们的代码随插件一起被用户装进来并启用了，
    // 但仍是第三方的——不标 trusted，照常走审批。
    const mcpServers: Record<string, MCPServerConfig> = { ...contributions.mcpServers };
    // 联网搜索：有启用且配了 Key 的引擎就把内核自带的搜索 server 挂上。
    // 它是我们自己的代码、而且只读，所以标 trusted——不然每查一次都弹一次框。
    if (await this.searchEnabled()) {
      mcpServers.web_search = {
        command: this.agentBin,
        args: ["mcp-search", `--app-db=${this.store.appDbPath}`],
        trusted: true,
      };
    }
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
      mcpServers: await this.buildMcpServers(contributions),
      skillDirs: this.skills.enabledDirs(),
      memoryFile: this.store.memoryFile,
      enableComputerUse: contributions.computerUse,
      enableEmail: contributions.email === true,
      enableSchedule: true,
      enableBrowser: config.browser === true,
      disableSandbox: config.sandboxCommands === false,
      codeMode: config.codeMode === true,
      approvalPolicy: config.profile,
      roles: toRoles(config),
      modelSeesImages: await this.modelSeesImages(config),
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

  autoMarkProvider(id: number): Promise<ProviderAutoMarkResult> {
    return this.requireClient().providerAutoMark(id);
  }

  // ---------- 搜索引擎 ----------

  listSearchEngines(): Promise<SearchEngineView[]> {
    return this.requireClient().searchList();
  }

  createSearchEngine(params: SearchEngineCreateParams): Promise<SearchEngineView> {
    return this.requireClient().searchCreate(params);
  }

  updateSearchEngine(params: SearchEngineUpdateParams): Promise<SearchEngineView> {
    return this.requireClient().searchUpdate(params);
  }

  async deleteSearchEngine(id: number): Promise<void> {
    await this.requireClient().searchDelete(id);
  }

  testSearchEngine(id: number, query: string): Promise<SearchEngineTestResult> {
    return this.requireClient().searchTest(id, query);
  }

  /** 有没有一个启用且配了 Key 的引擎。拿不到就当没有——开会话不该因此失败。 */
  private async searchEnabled(): Promise<boolean> {
    try {
      const engines = await this.requireClient().searchList();
      return engines.some((engine) => engine.enabled && engine.apiKeySet);
    } catch (error) {
      this.emit("log", "app", `读取搜索引擎配置失败：${String(error)}`);
      return false;
    }
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

  pluginConfig(uuid: string, connectionId = ""): Promise<PluginConfigField[]> {
    return this.requireClient().pluginConfig(uuid, connectionId);
  }

  async setPluginConfig(uuid: string, key: string, value: string, connectionId = ""): Promise<void> {
    await this.requireClient().pluginSetConfig(uuid, key, value, connectionId);
  }

  connectionCreate(pluginUuid: string, name = ""): Promise<ChannelConnectionView> {
    return this.requireClient().connectionCreate(pluginUuid, name);
  }

  async connectionRename(uuid: string, name: string): Promise<void> {
    await this.requireClient().connectionRename(uuid, name);
  }

  async connectionDelete(uuid: string): Promise<void> {
    await this.requireClient().connectionDelete(uuid);
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

  /**
   * 语音输入：把渲染层录好的 WAV 交给「配置 → 多模态」里的听写模型。
   * 没配听写模型时说清楚去哪儿配，而不是让按钮点了没反应。
   */
  async transcribeVoice(wav: Uint8Array): Promise<string> {
    const role = toRoles(this.store.readConfig()).stt;
    if (!role) throw new Error("还没有配听写模型：到「配置 → 多模态」里给「听写」选一个模型（比如 qwen3-asr-flash、whisper-1）");
    return this.requireClient().audioTranscribe({
      audio: Buffer.from(wav).toString("base64"),
      name: "voice.wav",
      role,
    });
  }

  emailTest(uuid: string): Promise<EmailTestResult> {
    return this.requireClient().emailTest(uuid);
  }

  wechatLoginStart(): Promise<WeChatLoginStartResult> {
    return this.requireClient().wechatLoginStart();
  }

  wechatLoginPoll(uuid: string, token: string, connectionId = ""): Promise<WeChatLoginPollResult> {
    return this.requireClient().wechatLoginPoll(uuid, token, connectionId);
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

const run = promisify(execFile);

interface KnownBrowser {
  id: string;
  name: string;
  /** .app 的完整路径。 */
  app: string;
  /** 扩展管理页。 */
  extensionsPage: string;
  /** 系统里登记的默认浏览器是它。 */
  isDefault: boolean;
  /** 商店里扩展页面的地址。空表示还没上架，走「加载已解压的扩展程序」。 */
  storeUrl: string;
}

interface BrowserCandidate {
  id: string;
  name: string;
  bundle: string;
  bundleId: string;
  extensionsPage: string;
  /** 上架后填：这个浏览器的扩展商店里「AIClaw 浏览器助手」的页面。 */
  storeUrl: string;
}

const BROWSER_CANDIDATES: BrowserCandidate[] = [
  { id: "edge", name: "Microsoft Edge", bundle: "Microsoft Edge.app", bundleId: "com.microsoft.edgemac", extensionsPage: "edge://extensions", storeUrl: "" },
  { id: "chrome", name: "Chrome", bundle: "Google Chrome.app", bundleId: "com.google.chrome", extensionsPage: "chrome://extensions", storeUrl: "" },
  { id: "brave", name: "Brave", bundle: "Brave Browser.app", bundleId: "com.brave.browser", extensionsPage: "brave://extensions", storeUrl: "" },
  { id: "arc", name: "Arc", bundle: "Arc.app", bundleId: "company.thebrowser.browser", extensionsPage: "chrome://extensions", storeUrl: "" },
  { id: "chromium", name: "Chromium", bundle: "Chromium.app", bundleId: "org.chromium.chromium", extensionsPage: "chrome://extensions", storeUrl: "" },
];

let defaultBrowserCache: { at: number; bundleId: string } | null = null;

/**
 * 系统默认浏览器的 bundle id（小写）。读 LaunchServices 里 https 的处理程序。
 * 读不到返回空串。缓存一分钟：连接状态每变一次都要列浏览器，不必每次都读。
 */
function defaultBrowserBundleId(): string {
  if (defaultBrowserCache && Date.now() - defaultBrowserCache.at < 60_000) return defaultBrowserCache.bundleId;
  let bundleId = "";
  try {
    const plist = join(homedir(), "Library/Preferences/com.apple.LaunchServices/com.apple.launchservices.secure.plist");
    const raw = execFileSync("plutil", ["-convert", "json", "-o", "-", plist], { encoding: "utf8", timeout: 2000 });
    const handlers = (JSON.parse(raw) as { LSHandlers?: { LSHandlerURLScheme?: string; LSHandlerRoleAll?: string }[] }).LSHandlers ?? [];
    bundleId = (handlers.find((handler) => handler.LSHandlerURLScheme === "https")?.LSHandlerRoleAll ?? "").toLowerCase();
  } catch {
    bundleId = "";
  }
  defaultBrowserCache = { at: Date.now(), bundleId };
  return bundleId;
}

/**
 * 本机装了哪些能装这个扩展的浏览器，默认浏览器排第一。目前只认 macOS 的位置；
 * 别的平台返回空，设置页给通用说明。
 */
function installedBrowsers(): KnownBrowser[] {
  if (process.platform !== "darwin") return [];
  const roots = ["/Applications", join(homedir(), "Applications")];
  const preferred = defaultBrowserBundleId();
  const found: KnownBrowser[] = [];
  for (const candidate of BROWSER_CANDIDATES) {
    const app = roots.map((root) => join(root, candidate.bundle)).find((path) => existsSync(path));
    if (!app) continue;
    found.push({
      id: candidate.id,
      name: candidate.name,
      app,
      extensionsPage: candidate.extensionsPage,
      isDefault: candidate.bundleId === preferred,
      storeUrl: candidate.storeUrl,
    });
  }
  return found.sort((a, b) => Number(b.isDefault) - Number(a.isDefault));
}

/** 配对码：24 个字符，够随机，又能整段复制粘贴。 */
function newPairToken(): string {
  return randomBytes(18).toString("base64url");
}

/**
 * 浏览器扩展的目录：打包后在 Resources/browser-extension，开发时在仓库的
 * apps/browser-extension。用户在 chrome://extensions 里「加载已解压的扩展程序」选它。
 */
export function extensionDir(): string {
  const here = dirname(fileURLToPath(import.meta.url));
  const candidates = [
    join(process.resourcesPath ?? "", "browser-extension"),
    resolve(here, "../../../browser-extension"),
  ];
  for (const candidate of candidates) {
    if (candidate && existsSync(join(candidate, "manifest.json"))) return candidate;
  }
  return candidates[1]!;
}

/** 清单项里的模型名：去掉 `#标记` 与 `@窗口`。 */
function markedName(entry: string): string {
  return entry.split("@")[0]!.split("#")[0]!.trim();
}

/** 清单项里的能力标记。没有 `#` 就是只做对话。 */
function markedRoles(entry: string): string[] {
  const head = entry.split("@")[0]!;
  const hash = head.indexOf("#");
  if (hash < 0) return [];
  return head
    .slice(hash + 1)
    .split(",")
    .map((mark) => mark.trim().toLowerCase())
    .filter(Boolean);
}

/**
 * 把配置里的角色翻成协议的形状。没配的角色整个省掉——协议那边 providerId 为 0
 * 就是「没配」，传一个空壳只是噪音。
 */
function toRoles(config: AppConfig): RoleModels {
  const roles: RoleModels = {};
  for (const name of ["vision", "stt", "tts", "image"] as const) {
    const role = config.roles?.[name];
    if (role?.providerId && role.model) {
      roles[name] = { providerId: role.providerId, model: role.model };
    }
  }
  return roles;
}
