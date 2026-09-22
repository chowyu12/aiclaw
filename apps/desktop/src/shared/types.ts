/**
 * 主进程与渲染层共用的纯类型。
 *
 * 单独一个文件是因为它必须能被三种模块格式一起用：主进程是 ESM、
 * preload 在 sandbox:true 下必须是 CJS、渲染层由 Vite 打包。
 * 类型在编译期就被抹掉，所以放这里不会带来格式问题；
 * 运行时常量在 ipc.cts 里，那个只给主进程和 preload 用。
 */

export interface RuntimeStatus {
  state: "starting" | "ready" | "stopped" | "failed";
  detail?: string;
}

/** 审批请求，与 claw-agent 的 ApprovalRequestParams 对应，外加回应用的 id。 */
export interface ApprovalPayload {
  id: string;
  sessionId: string;
  kind: "exec" | "write" | "tool";
  title: string;
  /** 完整命令或路径，原样展示不截断——用户要据此判断。 */
  detail: string;
  cwd?: string;
  reason?: string;
  /** 可以按目录批准的范围。非空时界面多给一个「本次会话都允许」。 */
  scopePath?: string;
}

/** claw-agent 推上来的事件，method 见 agent-client 的 AgentNotification。 */
export interface AgentEventPayload {
  method: string;
  params: unknown;
}

export interface SessionStartView {
  sessionId: string;
  tools: string[];
  mcpStatus: Record<string, string>;
  /** 会话工作区。空串表示没设置——相对路径按主目录解析，写入都要确认。 */
  workspace: string;
  /** 会话当前用的模型。恢复旧会话时可能与配置页的默认值不同。 */
  model: string;
  /** 本次会话挂上的技能名。 */
  skills?: string[];
  /** 恢复旧会话时带回的时间线条目；新会话没有。 */
  history?: HistoryItemView[];
}

/** 还原历史用的条目，字段与 claw-agent 的 Item 一致。 */
export interface HistoryItemView {
  id: string;
  kind: string;
  text?: string;
  /** 用户消息带的图片，base64 的 JPEG。 */
  images?: string[];
  toolName?: string;
  toolArgs?: string;
  toolResult?: string;
  toolFailed?: boolean;
  summary?: string;
  /** 执行步骤的统计。还原出来的历史只有 seq / round / toolCalls，没有时间。 */
  seq?: number;
  round?: number;
  toolCalls?: number;
  startedAt?: number;
  durationMs?: number;
}

/** 端点上能调的一个模型。 */
export interface ModelChoiceView {
  id: string;
  ownedBy: string;
  /** 上下文窗口（token）。0 表示端点没有这个模型的档案，窗口未知。 */
  contextWindow: number;
  maxOutputTokens: number;
  /** 合规等级名，例如「境内合规」，帮用户判断能不能喂内部数据。 */
  securityLevel: string;
}

/** 试连一个 MCP server 的结果。连不上不算错误，原因在 error 里。 */
export interface McpProbeView {
  ok: boolean;
  error?: string;
  tools?: { name: string; description?: string; readOnly?: boolean }[];
}

/** 用户自己配的 MCP server。 */
export interface McpServerView {
  id: string;
  /** 界面上显示的名字，也用作工具名前缀。 */
  label: string;
  transport: "stdio" | "http";
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  url?: string;
  headers?: Record<string, string>;
  enabled: boolean;
}

/** 一个已安装的本地技能。 */
export interface SkillView {
  /** 启停与删除时的标识：自己目录里的用目录名，别处的用绝对路径。 */
  id: string;
  dirName: string;
  dir: string;
  name: string;
  description: string;
  enabled: boolean;
  /** 来源：AIClaw / Claude Code / Codex / 项目 / npm 全局…… */
  source: string;
  /** false 表示技能在别人的目录里：能用、能关，但不能删。 */
  writable: boolean;
}

/** 检查更新的结果。 */
export interface UpdateStatusView {
  current: string;
  latest: string;
  hasUpdate: boolean;
  /** 查不到时的原因；空串表示查成功了。网络不通不该在界面上报红。 */
  error: string;
  /** 这个平台能不能一键升级。Windows 上只能开发布页。 */
  canInstall: boolean;
  /** 界面上那个按钮该写什么。平台不同，能做到的事也不同。 */
  installLabel?: string;
}

/** 用户自建的会话分组，像文件夹。 */
export interface SessionGroupView {
  id: string;
  name: string;
}

export interface SessionGroupsView {
  groups: SessionGroupView[];
  /** 会话 id → 分组 id。不在表里的会话是「未分组」。 */
  assignments: Record<string, string>;
}

export interface SessionSummaryView {
  id: string;
  title: string;
  createdAt: string;
  updatedAt: string;
  workdir: string;
  turnCount: number;
  model: string;
  /** 搜索命中的那一小段正文。只有搜索结果有。 */
  snippet?: string;
}

export interface AppConfigView {
  modelBaseUrl: string;
  model: string;
  reasoningEffort: string;
  /** 模型上下文窗口（token）；0 表示未知，内核退回被动压缩。 */
  contextWindow: number;
  /** 审批策略。没有沙箱之后这是唯一的安全档位。 */
  profile: "on-write" | "always" | "never";
  retentionDays: number;
  /** 没经过确认的命令跑不跑在 macOS 沙箱里。默认开。 */
  sandboxCommands: boolean;
  /** 代码模式：把工具收进一个 exec 工具，模型写 JavaScript 调用。默认关。 */
  codeMode: boolean;
  /** 是否启用 computer use（截屏 + 鼠标键盘）。默认关。 */
  enableComputerUse: boolean;
}
