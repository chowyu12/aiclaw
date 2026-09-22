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
  type MCPServerConfig,
  type PendingApproval,
  type SessionRefresh,
  type SessionStartParams,
  type SessionSummary,
} from "@aiclaw/agent-client";
import type { AppConfig, ConfigStore, McpServer } from "./config.js";
import { ComputerController } from "./computer.js";
import type { SkillManager } from "./skills.js";
import type { ModelCatalog } from "./models.js";

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
  /** 查模型的上下文窗口用。恢复会话对齐端点时要它。 */
  private readonly models: ModelCatalog;
  private readonly computer = new ComputerController();
  private readonly agentBin: string;
  /** 最近一段 stderr，失败时拼进错误信息，省得用户去翻日志。 */
  private lastStderr = "";
  /** 最近一次会话的挂载结果，诊断报告里要。 */
  private mounts: Record<string, string> = {};
  /** 最近一次会话的工作区，诊断报告里要。 */
  private workspace = "";

  constructor(store: ConfigStore, skills: SkillManager, models: ModelCatalog) {
    super();
    this.store = store;
    this.skills = skills;
    this.models = models;
    this.agentBin = resolveBin("CLAW_AGENT_BIN", "claw-agent");
  }

  get running(): boolean {
    return this.client?.running ?? false;
  }

  async start(): Promise<void> {
    if (this.client) return;
    const credentials = this.store.readCredentials();

    this.emit("status", "starting");

    const client = new ClawAgentClient({
      command: this.agentBin,
      args: ["serve", `--data-home=${this.store.agentHome}`],
      env: {
        ...process.env,
        // 凭据只走进程环境，不写任何配置文件、不经协议帧。
        AICLAW_LLM_KEY: credentials.llmKey,
      },
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
   * 重启运行时。改了凭据之后必须走一趟。
   *
   * **AICLAW_LLM_KEY 是在 start() 里注入子进程环境的**
   * ——凭据只进环境不进文件，这是刻意的，但代价是改完之后正在跑的那个内核
   * 仍然拿着旧值。不重启的话：换了 Key 继续聊会 401，而那个错来自上游、
   * 看起来像模型服务坏了，没人会想到是本地没生效。
   *
   * 返回是否真的重启了（本来就没在跑就不用）。
   */
  async restartIfRunning(): Promise<boolean> {
    if (!this.running) return false;
    await this.stop();
    await this.start();
    return true;
  }

  /**
   * 开一个新会话，返回 sessionId 与挂载结果。
   *
   * model 传了就用它，否则用配置页的默认模型——顶部切模型只改当前会话，
   * 新会话回到默认值，这是刻意的：一次临时换模型不该悄悄变成长期设置。
   *
   * workspace 同理，而且默认是**空的**：新会话不预设工作区，用户想好了再指。
   * 早先这里用一个全局工作目录，结果是所有会话共用一个目录，
   * 而用户真正想让 Agent 干活的地方在别处——他得先把文件搬进来。
   */
  async startSession(model?: string, workspace?: string): Promise<SessionInfo> {
    const client = this.requireClient();
    const config = this.store.readConfig();
    const params = this.buildParams(config, model, workspace);
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
    const result = await client.sessionResume(sessionId, this.buildRefresh());
    // 恢复出来的会话用的是**存档里的**模型配置，包括当时的模型端点。
    // 用户后来改了端点的话，点开旧会话仍然打旧地址——「配置改了没生效」。
    // 端点属于基础设施而不是这一轮的选择，所以强制对齐到当前配置；
    // 模型保留会话自己的（顶部切过模型的人不该被改回默认）。
    this.mounts = result.mcpStatus ?? {};
    await this.realignEndpoint(sessionId, result.model);
    // 技能发现要看这个会话的工作区（项目级 .claude/skills）。
    this.workspace = result.workspace ?? "";
    this.skills.setWorkspace(this.workspace);
    return {
      sessionId,
      tools: result.tools,
      mcpStatus: result.mcpStatus ?? {},
      workspace: result.workspace ?? "",
      model: result.model ?? this.store.readConfig().model,
      skills: result.skills ?? [],
      history: await client.sessionHistory(sessionId),
    };
  }

  /**
   * 把恢复出来的会话对齐到当前配置的模型端点。
   *
   * 上下文窗口跟着模型走，所以从目录里按模型名查；查不到就用配置里的值，
   * 再没有就传 0（未知）——内核那时退回「等上游报超窗再压缩」，能跑，只是
   * 白花一次请求。这比把窗口张冠李戴要好。
   */
  private async realignEndpoint(sessionId: string, model?: string): Promise<void> {
    const name = (model ?? "").trim();
    if (!name) return;
    const config = this.store.readConfig();
    let window = 0;
    try {
      const known = (await this.models.list()).find((item) => item.id === name);
      window = known?.contextWindow ?? 0;
    } catch {
      // 拉不到目录不该让恢复会话失败——那只是少一个窗口数字。
    }
    if (!window) window = name === config.model ? config.contextWindow : 0;
    try {
      await this.requireClient().sessionConfigure(sessionId, {
        baseUrl: config.modelBaseUrl,
        model: name,
        reasoningEffort: config.reasoningEffort || undefined,
        contextWindow: window || undefined,
      });
    } catch {
      // 对齐失败也让会话打开：至少用户能看到历史，而不是点开一片空白。
    }
  }

  /**
   * 换当前会话的模型。
   *
   * 上下文窗口跟着一起下发：不同模型窗口差一个数量级，沿用上一个模型的值
   * 会让内核要么过早压缩、要么撑爆窗口。目录里查不到就传 0（未知），
   * 内核退回「等上游报错再压」。
   */
  async configureSession(sessionId: string, model: string, contextWindow: number): Promise<void> {
    const config = this.store.readConfig();
    await this.requireClient().sessionConfigure(sessionId, {
      baseUrl: config.modelBaseUrl,
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

  /** 把应用配置翻译成会话配置。 */
  private buildParams(
    config: AppConfig,
    modelOverride?: string,
    workspace?: string,
  ): SessionStartParams {
    const mcpServers = this.buildMcpServers();
    return {
      model: {
        baseUrl: config.modelBaseUrl,
        model: modelOverride || config.model,
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
      enableComputerUse: config.enableComputerUse,
    };
  }

  /**
   * 拼出这次要挂的全部 MCP server。
   *
   * 每次开会话都重新拼：用户在 MCP 页改完，下一次挂载就生效。
   */
  private buildMcpServers(): Record<string, MCPServerConfig> {
    const mcpServers: Record<string, MCPServerConfig> = {};
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
  private buildRefresh(): SessionRefresh {
    const config = this.store.readConfig();
    return {
      mcpServers: this.buildMcpServers(),
      skillDirs: this.skills.enabledDirs(),
      memoryFile: this.store.memoryFile,
      enableComputerUse: config.enableComputerUse,
      disableSandbox: config.sandboxCommands === false,
      codeMode: config.codeMode === true,
      approvalPolicy: config.profile,
    };
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
