/**
 * 与 tools/claw-agent/internal/protocol/protocol.go 一一对应的 TS 类型。
 *
 * 两边手工保持同步。协议是我们自己的，字段不多，暂不引入代码生成——
 * 改一边记得改另一边，方法名与字段名以 Go 侧为准。
 */

export type ApprovalPolicy = "on-write" | "always" | "never";

export interface ModelConfig {
  /**
   * 模型服务的 id。填了它，Key 与端点由内核按 id 到模型配置库里查，
   * baseUrl 传什么都会被库里的盖掉；不填则走进程环境变量的 Key 与这里的 baseUrl。
   */
  providerId?: number;
  baseUrl: string;
  model: string;
  reasoningEffort?: string;
  temperature?: number;
  maxTokens?: number;
  /**
   * 模型的上下文窗口（token）。内核据此在撑满之前主动压缩历史。
   * 不填也能跑，代价是只能等上游报「超出上下文」再被动压缩，白花一次请求。
   */
  contextWindow?: number;
}

/**
 * 一个要挂载的 MCP server。
 *
 * 两种传输二选一：填了 url 走 Streamable HTTP（远程），否则把 command
 * 当子进程拉起来走 stdio（本地）。
 */
export interface MCPServerConfig {
  /** stdio：可执行文件与参数。 */
  command?: string;
  args?: string[];
  /** stdio：追加到子进程环境的变量。凭据只走这里，不进任何文件。 */
  env?: Record<string, string>;
  /** HTTP：server 地址。填了就走 HTTP，command 被忽略。 */
  url?: string;
  /** HTTP：附加到每条请求的头，鉴权走这里。 */
  headers?: Record<string, string>;
  /** 工具白名单；空表示这个 server 的工具全挂。 */
  enabledTools?: string[];
  /**
   * 这个 server 的工具调用前不再问用户。
   *
   * 只给宿主自己挂上的内置 server 用（比如应用内的搜索引擎）：代码在本仓库里，
   * 副作用是已知的。用户自己配的第三方 MCP server 不给——来源与副作用都未知。
   */
  trusted?: boolean;
}

export interface SessionStartParams {
  model: ModelConfig;
  /**
   * 会话工作区：相对路径的基准，也是「写这里不用问」的范围。
   *
   * 可以为空（用户没设）：那时相对路径按主目录解析，任何写入都要确认。
   * 它不是围墙——读可以在外面，命令也能跑到别处；围墙要沙箱，这一版没有。
   */
  workdir?: string;
  /** 宿主追加的敏感路径，命中即拒绝访问（不是「问一句」）。 */
  protectedPaths?: string[];
  /**
   * 代码模式：把全部工具收进一个 `exec` 工具，模型写 JavaScript 来调用。
   *
   * 默认关。省下的是上下文地板（161 个工具的定义从约 110KB 降到 7.4KB）
   * 与来回次数（十几次查询写成一段循环，中间结果不进上下文）。代价是
   * 模型要会写对代码，小模型在这上面更吃力。
   */
  codeMode?: boolean;
  /**
   * 关掉命令沙箱（macOS）。**默认是开的**，所以这里是「关掉」而不是「启用」：
   * 布尔的零值必须落在安全那一侧——字段缺失、旧存档、忘了传，沙箱都还在。
   */
  disableSandbox?: boolean;
  instructions?: string;
  approvalPolicy?: ApprovalPolicy;
  mcpServers?: Record<string, MCPServerConfig>;
  enabledBuiltins?: string[];
  /**
   * 是否启用 computer use（截屏 + 鼠标键盘）。默认关。
   * 开了之后模型能看见并操作**整个屏幕**，不只是工作目录。
   */
  enableComputerUse?: boolean;
  /**
   * 技能目录列表。每一项是一个技能自己的目录（里面直接放 SKILL.md），
   * 不是装着若干技能的根目录——技能散在好几个地方且结构各异，
   * 发现逻辑统一放在宿主侧（apps/desktop 的 skill-roots.ts）。
   */
  skillDirs?: string[];
  /**
   * 长期记忆文件。跨会话，每次开会话原样进系统提示词。
   * 与会话上下文是两件事：后者撑满了会被压缩，前者不会。
   */
  memoryFile?: string;
}

/**
 * 恢复会话时用**当前**宿主配置覆盖掉存档的那几项。
 *
 * 存档里的 MCP server、技能是建会话那一刻的，而应用一启动就接着
 * 上次的会话——不覆盖的话，用户后来加的东西永远挂不上，表现是「配了没用」。
 * 工作目录与模型不在里面：换工作目录会让历史里的路径对不上，模型是会话
 * 自己的选择（端点由宿主用 session/configure 另行对齐）。
 *
 * 给了就整份生效（空对象 = 一个 MCP server 都不挂），不做「空的就跳过」。
 */
export interface SessionRefresh {
  mcpServers: Record<string, MCPServerConfig>;
  skillDirs: string[];
  memoryFile: string;
  enableComputerUse: boolean;
  disableSandbox: boolean;
  codeMode: boolean;
  approvalPolicy: ApprovalPolicy;
}

/** 试连一个 MCP server 的结果。连不上不算错误，原因在 error 里。 */
/**
 * 一个模型服务：OpenAI 兼容端点 + Key + 模型清单。会话按 id 选。
 * Key 只进库不出库——这里只有 apiKeySet 一位。
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

export interface ProviderCreateParams {
  name: string;
  type?: string;
  baseUrl?: string;
  apiKey?: string;
  models?: string[];
  enabled?: boolean;
}

/** 没给的字段不动；apiKey 给空串表示清掉。 */
export interface ProviderUpdateParams {
  id: number;
  name?: string;
  type?: string;
  baseUrl?: string;
  apiKey?: string;
  models?: string[];
  enabled?: boolean;
}

// ---------- 搜索引擎 ----------
//
// 联网搜索引擎各带 Key；搜索工具是随内核分发的 MCP server（claw-agent mcp-search），
// 宿主在有启用中的引擎时挂进会话。

export interface SearchEngineView {
  id: number;
  /** tavily / serpapi / aliyun-iqs */
  provider: string;
  name: string;
  /** 空表示用该类型的默认端点。 */
  baseUrl: string;
  apiKeySet: boolean;
  enabled: boolean;
}

export interface SearchEngineCreateParams {
  provider: string;
  name?: string;
  baseUrl?: string;
  apiKey?: string;
  enabled?: boolean;
}

/** 没给的字段不动；apiKey 给空串表示清掉。 */
export interface SearchEngineUpdateParams {
  id: number;
  provider?: string;
  name?: string;
  baseUrl?: string;
  apiKey?: string;
  enabled?: boolean;
}

export interface SearchHit {
  title: string;
  url: string;
  snippet: string;
}

export interface SearchEngineTestResult {
  provider: string;
  results: SearchHit[];
}

// ---------- 插件 ----------
//
// 插件是一个带 plugin.json 的目录，声明权限、配置项，以及贡献的技能、MCP server、
// 宿主能力（computer use）与通道（微信、企业微信）。与 Go 侧 protocol 同形。

export interface PluginView {
  uuid: string;
  pluginId?: string;
  name: string;
  description?: string;
  version?: string;
  /** builtin 随应用分发，只能停用不能删；local 是用户从目录装的。 */
  source: "builtin" | "local" | string;
  enabled: boolean;
  skills: number;
  mcp: number;
  tools: number;
  channels: number;
  /** 启用后会拿到的权限名。 */
  permissions: string[];
  /** 还没填的必填配置项；非空时启用会被拒绝。 */
  missingConfig: string[];
}

/** 一个配置项。秘密只报 isSet，值永不回传。 */
export interface PluginConfigField {
  key: string;
  type: string;
  description?: string;
  required: boolean;
  secret: boolean;
  isSet: boolean;
  value?: string;
}

/** 启用中的插件合起来贡献给会话的东西，开会话时并进 SessionStartParams。 */
export interface PluginContributions {
  mcpServers: Record<string, MCPServerConfig>;
  skills: PluginSkill[];
  computerUse: boolean;
}

export interface PluginSkill {
  /** 技能目录（里面直接放 SKILL.md）。 */
  dir: string;
  pluginName: string;
  pluginUuid: string;
}

export interface ChannelStatusView {
  pluginUuid: string;
  pluginName: string;
  channelId: string;
  displayName?: string;
  state: "starting" | "running" | "retrying" | "failed" | "stopped" | string;
  attempts?: number;
  lastError?: string;
}

/** 通道见过的一个外部会话。未授权的也在列表里，等用户放行。 */
export interface ChannelBindingView {
  pluginUuid: string;
  channelId: string;
  externalKey: string;
  displayName?: string;
  sessionId?: string;
  providerId?: number;
  model?: string;
  allowed: boolean;
  allowedTools: string[];
  lastMessage?: string;
}

export interface ChannelBindingKey {
  pluginUuid: string;
  channelId: string;
  externalKey: string;
}

export interface ChannelAuthorizeParams extends ChannelBindingKey {
  providerId: number;
  model: string;
  allowedTools?: string[];
}

export interface WeChatLoginStartResult {
  token: string;
  /** 二维码，PNG data URI。 */
  image: string;
}

export interface WeChatLoginPollResult {
  status: "wait" | "scaned" | "confirmed" | "expired" | string;
  saved: boolean;
}

export interface MCPProbeResult {
  ok: boolean;
  error?: string;
  tools?: MCPToolInfo[];
}

export interface MCPToolInfo {
  name: string;
  description?: string;
  /** 工具自己声明的 readOnlyHint。没声明就是 false——按有副作用处理。 */
  readOnly?: boolean;
}

export interface SessionStartResult {
  /** 会话用的模型服务 id；0 或缺省表示走环境变量的 Key。 */
  providerId?: number;
  /** 代码模式下被收进 exec 的工具名：tools 里只剩 exec，但这些在脚本里仍能调。 */
  foldedTools?: string[];
  sessionId: string;
  tools: string[];
  mcpStatus?: Record<string, string>;
  /**
   * 会话当前用的模型。恢复旧会话时它可能与宿主配置的默认值不同——
   * 会话记着自己的模型，顶部要显示的是这个。
   */
  model?: string;
  /** 本次会话挂上的技能名。 */
  skills?: string[];
  /** 会话工作区。空串表示没设置。 */
  workspace?: string;
}

export interface SessionSummary {
  id: string;
  title: string;
  createdAt: string;
  updatedAt: string;
  workdir: string;
  turnCount: number;
  model: string;
  /** 搜索命中的那一小段正文。只有 sessionSearch 会给。 */
  snippet?: string;
}

export interface InitializeResult {
  version: string;
  dataHome: string;
  tools: string[];
}

/** notice 是内核对用户说的话（历史被压缩了、正在重试），不是模型输出也不是错误。 */
export type ItemKind =
  | "userMessage"
  | "agentMessage"
  | "reasoning"
  | "toolCall"
  | "llm"
  | "error"
  | "notice";

/**
 * 改一个已存在会话的模型。
 *
 * 只给模型，不给工作目录和审批策略：那两样变了等于换了会话的安全前提，
 * 而历史还留着——与其悄悄生效，不如让用户另起一个会话。
 */
/**
 * 会话历史还原成的时间线条目。
 *
 * 内核存的是给模型看的消息，这里给的是给人看的条目：一条带 tool_calls 的
 * assistant 消息会拆成「一句话 + 若干个工具步骤」。
 */
export interface SessionHistoryResult {
  items: Item[];
}

/** 一次屏幕操作。坐标是逻辑像素，原点左上。 */
export interface ComputerRequestParams {
  sessionId: string;
  turnId: string;
  action:
    | "screenshot"
    | "click"
    | "doubleClick"
    | "rightClick"
    | "move"
    | "type"
    | "key"
    | "scroll";
  x?: number;
  y?: number;
  dx?: number;
  dy?: number;
  text?: string;
  keys?: string;
}

export interface ComputerResult {
  /** 给模型看的一句话。 */
  text: string;
  /** 截屏时的 PNG，base64。 */
  imageBase64?: string;
  /** 屏幕逻辑尺寸；模型要靠它把图上的位置换算成点击坐标。 */
  width?: number;
  height?: number;
}

export interface SessionConfigureParams {
  sessionId: string;
  model: ModelConfig;
}

export interface TurnStartResult {
  turnId: string;
  /**
   * true 表示这条输入被排进了正在跑的轮次，没有另起一轮。
   * turnId 此时是那个进行中轮次的 id，不会再有一次 turn/started。
   */
  queued?: boolean;
}

export interface Item {
  id: string;
  kind: ItemKind;
  text?: string;
  /** 用户消息带的图片，base64 的 PNG/JPEG。恢复会话时要能重新画出来。 */
  images?: string[];
  toolName?: string;
  toolArgs?: string;
  toolResult?: string;
  toolFailed?: boolean;
  summary?: string;

  /** 这一步在轮次内的序号，从 1 开始。 */
  seq?: number;
  /** 开始时刻，Unix 毫秒。 */
  startedAt?: number;
  /** 耗时，毫秒。完成时才有。 */
  durationMs?: number;

  // ---- 仅 llm 条目 ----
  model?: string;
  /** 本轮的第几次采样。 */
  round?: number;
  /** 首字节时间，毫秒。 */
  ttftMs?: number;
  /** 推理耗时，毫秒。 */
  thinkMs?: number;
  /** 这次采样发起了几个工具调用。 */
  toolCalls?: number;
  usage?: { inputTokens: number; outputTokens: number; totalTokens: number };
}

export interface ItemNotification {
  sessionId: string;
  turnId: string;
  item: Item;
}

export interface ItemDeltaNotification {
  sessionId: string;
  turnId: string;
  itemId: string;
  delta: string;
}

export interface Usage {
  inputTokens: number;
  outputTokens: number;
  totalTokens: number;
}

export interface TurnNotification {
  sessionId: string;
  turnId: string;
  usage?: Usage;
  error?: string;
}

export type ApprovalKind = "exec" | "write" | "tool";

export interface ApprovalRequestParams {
  sessionId: string;
  turnId: string;
  kind: ApprovalKind;
  title: string;
  detail: string;
  cwd?: string;
  reason?: string;
  /**
   * 这次审批涉及的目录。非空时界面可以给出「本次会话都允许这个目录」。
   * 给的是目录不是文件——按文件批，下一个文件又要问一次。
   */
  scopePath?: string;
}

/** 服务端通知的判别联合，宿主按 method 分发。 */
export type AgentNotification =
  | { method: "turn/started"; params: TurnNotification }
  | { method: "turn/completed"; params: TurnNotification }
  | { method: "item/started"; params: ItemNotification }
  | { method: "item/delta"; params: ItemDeltaNotification }
  | { method: "item/completed"; params: ItemNotification }
  | { method: "error"; params: { sessionId?: string; message: string } };
