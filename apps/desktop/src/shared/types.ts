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
  /** 模型所属的模型服务 id；0 表示走环境变量的 Key。 */
  providerId: number;
  /** 本次会话挂上的技能名。 */
  skills?: string[];
  /** 代码模式下被收进 exec 的工具名：tools 里只剩 exec，但这些在脚本里仍能调。 */
  foldedTools?: string[];
  /** 恢复旧会话时带回的时间线条目；新会话没有。 */
  history?: HistoryItemView[];
}

/** 一条消息里 @ 引用的另一个会话。 */
export interface ThreadRefView {
  id: string;
  title: string;
}

/** 还原历史用的条目，字段与 claw-agent 的 Item 一致。 */
export interface HistoryItemView {
  artifacts?: string[];
 requestId?: string;
  id: string;
  kind: string;
  /** 用户消息与助手消息的时间（Unix 毫秒）。 */
  at?: number;
  text?: string;
  /** 用户消息带的图片，base64 的 JPEG。 */
  images?: string[];
  /** 用户消息里 @ 引用的会话。 */
  references?: ThreadRefView[];
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

/**
 * 一个模型服务：OpenAI 兼容端点 + Key + 模型清单。与 agent-client 的
 * ProviderView 同形；Key 只进不出，这里只有 apiKeySet 一位。
 */
export interface ProviderView {
  id: number;
  name: string;
  /** openai / qwen / kimi / openrouter / openai-compatible / claude / gemini */
  type: string;
  /** 空表示用该类型的默认端点。 */
  baseUrl: string;
  apiKeySet: boolean;
  models: string[];
  enabled: boolean;
}

/** 一个联网搜索引擎。与 agent-client 的 SearchEngineView 同形；Key 只报 apiKeySet。 */
export interface SearchEngineView {
  id: number;
  /** tavily / serpapi / aliyun-iqs */
  provider: string;
  name: string;
  baseUrl: string;
  apiKeySet: boolean;
  enabled: boolean;
}

export interface SearchHitView {
  title: string;
  url: string;
  snippet: string;
}

/** 一个插件。与 agent-client 的 PluginView 同形。 */
export interface PluginView {
  uuid: string;
  pluginId?: string;
  name: string;
  description?: string;
  version?: string;
  /** builtin 随应用分发，只能停用不能删；local 是用户从目录装的。 */
  source: string;
  enabled: boolean;
  skills: number;
  mcp: number;
  tools: number;
  channels: number;
  permissions: string[];
  missingConfig: string[];
  /** 渠道插件的连接（一个微信号、一个企微机器人一个）。别的插件是空的。 */
  connections: ChannelConnectionView[];
}

/** 渠道插件的一个连接。 */
export interface ChannelConnectionView {
  uuid: string;
  name: string;
  /** 这个连接还没填的必填配置；非空时它不会启动。 */
  missingConfig: string[];
}

/** 插件的一个配置项。秘密只报 isSet，值永不回传。 */
export interface PluginConfigFieldView {
  key: string;
  type: string;
  description?: string;
  required: boolean;
  secret: boolean;
  isSet: boolean;
  value?: string;
}

export interface ChannelStatusView {
  pluginUuid: string;
  pluginName: string;
  channelId: string;
  displayName?: string;
  /** 这个实例服务的连接。 */
  connectionId?: string;
  connectionName?: string;
  state: string;
  attempts?: number;
  lastError?: string;
}

/** 通道见过的一个外部会话。未授权的也在列表里，等用户放行。 */
export interface ChannelBindingView {
  pluginUuid: string;
  channelId: string;
  /** 从哪个连接进来的。 */
  connectionId: string;
  connectionName?: string;
  externalKey: string;
  displayName?: string;
  sessionId?: string;
  providerId?: number;
  model?: string;
  allowed: boolean;
  allowedTools: string[];
  lastMessage?: string;
}

/** 启用中的插件贡献了什么。MCP 页与技能页据此标出「来自插件」的条目。 */
export interface PluginContributionsView {
  mcpServers: Record<string, { command?: string; url?: string }>;
  skills: { dir: string; pluginName: string; pluginUuid: string }[];
  computerUse: boolean;
  email?: boolean;
}

/** 「测试邮箱」的结果。 */
export interface EmailTestView {
  ok: boolean;
  error?: string;
  imapHost?: string;
  imapPort?: number;
  smtpHost?: string;
  smtpPort?: number;
}

/** 试连一个 MCP server 的结果。连不上不算错误，原因在 error 里。 */
export interface McpProbeView {
  ok: boolean;
  error?: string;
  tools?: { name: string; description?: string; readOnly?: boolean }[];
}

/** 用户自己配的 MCP server。 */
export interface McpServerView {
 oauth?: boolean;
 oauthClientId?: string;
 oauthScope?: string;
 oauthRedirectPort?: number;
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
  /** 会话 id → 分组 id。不在表里的会话是「未分组」（渠道会话是「渠道会话」）。 */
  assignments: Record<string, string>;
  /** 分组 id → 是否折叠。没记的：渠道会话默认折叠，其它默认展开。 */
  collapsed: Record<string, boolean>;
  /** 分组 id → 上次展开看过的时间（毫秒）。 */
  seenAt: Record<string, number>;
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
  /** 子 agent 的父会话 id；普通会话没有。侧边栏据此把它挂在父会话下面。 */
  parentId?: string;
  /** 归档的时刻（ISO）；只有归档列表里的有。 */
  archivedAt?: string;
}

/** 一个角色用哪个模型。providerId 为 0 表示没配。 */
export interface RoleModelView {
  providerId: number;
  model: string;
}

/** 对话之外的角色模型：看图、听写、朗读、画图。 */
export interface RoleConfigView {
  vision: RoleModelView;
  stt: RoleModelView;
  tts: RoleModelView;
  image: RoleModelView;
}

export interface AppConfigView {
  /** 界面语言。 */
  language: "en" | "zh-CN";
  /** 新会话默认用的模型服务 id；0 表示没选。 */
  providerId: number;
  model: string;
  /** 角色模型。没配的角色对应的工具不会注册。 */
  roles: RoleConfigView;
  reasoningEffort: string;
  /** 模型上下文窗口（token）；0 表示未知，内核退回被动压缩。 */
  contextWindow: number;
  /** 审批策略。没有沙箱之后这是唯一的安全档位。 */
  profile: "on-write" | "always" | "never" | "bypass";
  retentionDays: number;
  /** 没经过确认的命令跑不跑在 macOS 沙箱里。默认开。 */
  sandboxCommands: boolean;
  /** 代码模式：把工具收进一个 exec 工具，模型写 JavaScript 调用。默认关。 */
  codeMode: boolean;
  /** 浏览器工具：独立窗口，模型按元素编号操作。默认关。 */
  browser: boolean;
  /** 浏览器工具在哪儿干活：自带窗口，或用户自己的浏览器（经扩展）。 */
  browserBackend: "builtin" | "extension";
  /** 与浏览器扩展配对的密钥。设置页显示给用户复制。 */
  browserPairToken: string;
}

/** 模型向用户提的一个问题（ask_user），对话里显示成一张带选项的卡片。 */
export interface QuestionPayload {
  id: string;
  sessionId: string;
  turnId: string;
  question: string;
  options: { label: string; description?: string }[];
  multiSelect: boolean;
  /** 提问的时间（毫秒）。 */
  at: number;
}

/** 用户对一个问题的回答。 */
export interface QuestionAnswer {
  selected?: string[];
  text?: string;
  skipped?: boolean;
}

/** 设置 → 用量。形状与内核 store.UsageSummary 一致。 */
export interface UsageSummaryView {
  since: number;
  totals: {
    input: number;
    output: number;
    total: number;
    modelCalls: number;
    modelFailed: number;
    toolCalls: number;
    toolFailed: number;
    sessions: number;
  };
  days: { day: string; input: number; output: number; modelCalls: number; toolCalls: number }[];
  models: { model: string; input: number; output: number; calls: number; failed: number }[];
  tools: { tool: string; source: string; calls: number; failed: number; avgMs: number }[];
  skills: { skill: string; uses: number }[];
  sessions: { sessionId: string; title: string; total: number; calls: number }[];
  firstAt: number;
}

/** 浏览器扩展的连接状态，设置页显示。 */
export interface BrowserBridgeView {
  listening: boolean;
  port: number;
  error?: string;
  /** 连上的浏览器（Chrome / Microsoft Edge …）。没连上是空串。 */
  browser: string;
  extensionVersion: string;
  /** 扩展目录：用户在 chrome://extensions 里「加载已解压的扩展程序」选它。 */
  extensionDir: string;
  /** 正在配对时两边显示的四位代码；不在配对是空串。 */
  pairingCode: string;
  /** 本机装了的、能装这个扩展的浏览器（设置页据此给「在 X 中打开扩展页」按钮）。 */
  browsers: {
    id: string;
    name: string;
    /** 系统默认浏览器（排在第一个）。 */
    isDefault: boolean;
    /** 已上架：从商店装。否则走「加载已解压的扩展程序」。 */
    fromStore: boolean;
  }[];
}
