// Package protocol 定义 claw-agent 与桌面宿主之间的线协议。
//
// 形态是「一行一个 JSON-RPC 2.0 帧」走 stdio，与 MCP 一致，也与 Codex 的
// app-server 一致。沿用这个形态不是巧合：会话（session）→ 轮次（turn）→
// 条目（item）这套三层模型在几个成熟实现里收敛到了一起，自研时照着做
// 比另发明一套省事，也省得宿主侧为每种事件写特例。
//
// 与 Codex 的差别在于这里只定义我们真正需要的：它的通知有七十多种，
// 这里十几种就够了。
package protocol

import "encoding/json"

// ---------- 宿主 → agent 的请求 ----------

const (
	MethodInitialize       = "initialize"
	MethodSessionStart     = "session/start"
	MethodSessionResume    = "session/resume"
	MethodSessionList      = "session/list"
	MethodSessionSearch    = "session/search"
	MethodSessionDelete    = "session/delete"
	MethodSessionConfigure = "session/configure"
	MethodSessionHistory   = "session/history"
	MethodTurnStart        = "turn/start"
	MethodTurnInterrupt    = "turn/interrupt"
	MethodApprovalRespond  = "approval/respond"
	MethodMCPProbe         = "mcp/probe"
	MethodProviderList     = "provider/list"
	MethodProviderCreate   = "provider/create"
	MethodProviderUpdate   = "provider/update"
	MethodProviderDelete   = "provider/delete"
	MethodProviderModels   = "provider/models"
	MethodShutdown         = "shutdown"
)

// ---------- agent → 宿主的通知 ----------

const (
	NotifyTurnStarted   = "turn/started"
	NotifyTurnCompleted = "turn/completed"
	NotifyItemStarted   = "item/started"
	NotifyItemDelta     = "item/delta"
	NotifyItemCompleted = "item/completed"
	NotifyError         = "error"
)

// ---------- agent → 宿主的请求（需要回应） ----------

const (
	RequestApproval = "approval/request"
	// RequestComputer 请宿主代做一次屏幕操作。
	//
	// 截屏与输入都要走宿主：截屏用 Electron 的 desktopCapturer（走应用自己的
	// 屏幕录制授权，比 shell 出去可靠），输入要按平台合成事件。内核这边只管
	// 把工具调用翻译成请求。
	RequestComputer = "computer/request"
)

// ComputerAction 是一次屏幕操作。
type ComputerAction string

const (
	ComputerScreenshot  ComputerAction = "screenshot"
	ComputerClick       ComputerAction = "click"
	ComputerDoubleClick ComputerAction = "doubleClick"
	ComputerRightClick  ComputerAction = "rightClick"
	ComputerMove        ComputerAction = "move"
	ComputerType        ComputerAction = "type"
	ComputerKey         ComputerAction = "key"
	ComputerScroll      ComputerAction = "scroll"
)

type ComputerRequestParams struct {
	SessionID string         `json:"sessionId"`
	TurnID    string         `json:"turnId"`
	Action    ComputerAction `json:"action"`
	/** 点击 / 移动 / 滚动的坐标，单位是逻辑像素，原点左上。 */
	X int `json:"x,omitempty"`
	Y int `json:"y,omitempty"`
	/** 滚动量；正数向下 / 向右。 */
	DX int `json:"dx,omitempty"`
	DY int `json:"dy,omitempty"`
	/** type 的文本。 */
	Text string `json:"text,omitempty"`
	/** key 的按键组合，例如 "cmd+s"、"Return"。 */
	Keys string `json:"keys,omitempty"`
}

type ComputerResult struct {
	/** 给模型看的一句话。 */
	Text string `json:"text"`
	/** 截屏时的 PNG，base64。其余动作为空。 */
	ImageBase64 string `json:"imageBase64,omitempty"`
	/** 屏幕逻辑尺寸，截屏时带上——模型要靠它把图上的位置换算成点击坐标。 */
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
}

type InitializeParams struct {
	ClientName    string `json:"clientName"`
	ClientVersion string `json:"clientVersion"`
}

type InitializeResult struct {
	Version  string   `json:"version"`
	DataHome string   `json:"dataHome"`
	Tools    []string `json:"tools"`
}

// ModelConfig 是打模型所需的全部配置。
//
// APIKey 不在这里——它不经过协议帧，免得凭据出现在宿主的日志或崩溃转储里。
// 填了 ProviderID 时，Key 与端点由内核按 id 到模型配置库里查（见 providers 包）；
// 没填时退回进程环境变量 AICLAW_LLM_KEY 与这里的 BaseURL。
type ModelConfig struct {
	/** 模型服务的 id。填了它，BaseURL 以库里的为准，传来的会被盖掉。 */
	ProviderID      int64   `json:"providerId,omitempty"`
	BaseURL         string  `json:"baseUrl"`
	Model           string  `json:"model"`
	ReasoningEffort string  `json:"reasoningEffort,omitempty"`
	Temperature     float64 `json:"temperature,omitempty"`
	MaxTokens       int     `json:"maxTokens,omitempty"`
	/**
	 * 模型的上下文窗口（token）。用于在撑满之前主动压缩历史。
	 *
	 * 不填也能跑：那种情况下只能等上游报「超出上下文」再被动压缩，
	 * 代价是白花一次请求。端点一般不提供按模型查窗口的接口，
	 * 所以这个值由宿主的模型配置给出。
	 */
	ContextWindow int `json:"contextWindow,omitempty"`
}

// ---------- 模型服务 ----------
//
// 一个模型服务是一个 OpenAI 兼容端点加它的 Key 与模型清单。会话按 id 选。
// Key 只进库不出库：这里只有 APIKeySet 一位。

type ProviderView struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	/** openai / qwen / kimi / openrouter / openai-compatible / claude / gemini */
	Type string `json:"type"`
	/** 空表示用该类型的默认端点。 */
	BaseURL   string   `json:"baseUrl"`
	APIKeySet bool     `json:"apiKeySet"`
	Models    []string `json:"models"`
	Enabled   bool     `json:"enabled"`
}

type ProviderListResult struct {
	Providers []ProviderView `json:"providers"`
}

type ProviderCreateParams struct {
	Name    string   `json:"name"`
	Type    string   `json:"type,omitempty"`
	BaseURL string   `json:"baseUrl,omitempty"`
	APIKey  string   `json:"apiKey,omitempty"`
	Models  []string `json:"models,omitempty"`
	Enabled *bool    `json:"enabled,omitempty"`
}

// ProviderUpdateParams 里 nil 的字段不动。APIKey 指向空串表示清掉。
type ProviderUpdateParams struct {
	ID      int64    `json:"id"`
	Name    *string  `json:"name,omitempty"`
	Type    *string  `json:"type,omitempty"`
	BaseURL *string  `json:"baseUrl,omitempty"`
	APIKey  *string  `json:"apiKey,omitempty"`
	Models  []string `json:"models,omitempty"`
	Enabled *bool    `json:"enabled,omitempty"`
}

type ProviderIDParams struct {
	ID int64 `json:"id"`
}

// ProviderModelsResult 是到端点 /models 拉到的模型名，不落库。
type ProviderModelsResult struct {
	Models []string `json:"models"`
}

// MCPServerConfig 是一个要挂载的 MCP server。
//
// 两种传输二选一：填了 URL 走 Streamable HTTP（远程），否则把 Command
// 当子进程拉起来走 stdio（本地）。
type MCPServerConfig struct {
	/** stdio：可执行文件与参数。 */
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	/** stdio：追加到子进程环境的变量。凭据只走这里，不进任何文件。 */
	Env map[string]string `json:"env,omitempty"`
	/** HTTP：server 地址。填了就走 HTTP，Command 被忽略。 */
	URL string `json:"url,omitempty"`
	/** HTTP：附加到每条请求的头，鉴权走这里。 */
	Headers map[string]string `json:"headers,omitempty"`
	/** 工具白名单；空表示这个 server 的工具全挂。 */
	EnabledTools []string `json:"enabledTools,omitempty"`
	/**
	 * 这个 server 的工具调用前不再问用户。
	 *
	 * 这是一条**策略**，与工具自己声明的 readOnlyHint 是两回事：
	 * 后者是事实（这个工具改不改东西），前者是决定（我们信不信这个来源）。
	 * 分开放是为了不去篡改事实——把一个写操作标成只读来绕过审批，
	 * 会让 readOnlyHint 从此不可信，而客户端还要靠它判断第三方 server。
	 *
	 * 只给宿主自己挂上的内置 server 用（比如应用内的搜索引擎）：代码在本仓库里，
	 * 副作用是已知的。用户自己配的第三方 MCP server 不给——它们的来源与副作用
	 * 都未知，没有沙箱之后宁可多问。
	 */
	Trusted bool `json:"trusted,omitempty"`
}

// ApprovalPolicy 决定什么动作需要问用户。
//
// 不做沙箱之后这是唯一的闸，所以默认值取得保守：
// 执行命令和工作目录外的写入都问。
type ApprovalPolicy string

const (
	// ApprovalOnWrite 对执行命令与工作目录外的写入弹确认。默认。
	ApprovalOnWrite ApprovalPolicy = "on-write"
	// ApprovalAlways 对所有有副作用的工具都弹确认。
	ApprovalAlways ApprovalPolicy = "always"
	// ApprovalNever 不弹确认。无人值守场景用；需要审批的调用直接失败。
	ApprovalNever ApprovalPolicy = "never"
)

type SessionStartParams struct {
	Model ModelConfig `json:"model"`
	/**
	 * 会话工作区：相对路径的基准，也是「写这里不用问」的范围。
	 *
	 * **可以为空**（用户没设）：那时相对路径按主目录解析，任何写入都要确认。
	 * 它不是围墙——读可以在工作区之外，命令也能跑到别处去；围墙需要沙箱，
	 * 这一版没有。字段名沿用 workdir 是为了让旧存档仍然读得出来。
	 */
	Workdir string `json:"workdir,omitempty"`
	/**
	 * 宿主追加的敏感路径，命中即拒绝访问（不是「问一句」）。
	 *
	 * 典型是应用自己存凭据的那个目录：模型读到它没有任何正当用途，
	 * 而读到之后可以顺着任何一个对外的工具送出去。
	 */
	ProtectedPaths []string `json:"protectedPaths,omitempty"`
	/** 系统提示词。 */
	Instructions   string                     `json:"instructions,omitempty"`
	ApprovalPolicy ApprovalPolicy             `json:"approvalPolicy,omitempty"`
	MCPServers     map[string]MCPServerConfig `json:"mcpServers,omitempty"`
	/** 内置工具白名单；空表示全开。 */
	EnabledBuiltins []string `json:"enabledBuiltins,omitempty"`
	/**
	 * 技能目录列表。**每一项都是一个技能自己的目录**（里面直接放 SKILL.md），
	 * 不是「装着若干技能的根目录」。
	 *
	 * 为什么由宿主给出具体目录而不是给一个根：技能散在好几个地方——我们自己的
	 * ~/.upstream/skills、Claude Code 的 ~/.claude/skills、Codex 的
	 * ~/.codex/skills、npx 装的 CLI 缓存、npm 全局包，每种的目录结构还不一样
	 * （带版本号的、带 @scope 的）。发现逻辑只该有一份，而它必须在宿主侧——
	 * 界面要把「这个技能从哪儿来」显示出来，还要记住用户关掉了哪些。
	 */
	SkillDirs []string `json:"skillDirs,omitempty"`
	/**
	 * 长期记忆文件。跨会话，每次开会话原样进系统提示词。
	 * 留空表示不启用。与会话上下文是两件事：后者会被压缩，前者不会。
	 */
	MemoryFile string `json:"memoryFile,omitempty"`
	/**
	 * 代码模式：把全部工具收进一个 `exec` 工具，模型写 JavaScript 来调用。
	 *
	 * 默认关。开了之后省下的是**上下文地板**（161 个工具的定义从约 110KB
	 * 降到 7.4KB）与**来回次数**（十几次查询写成一段循环，中间结果不进
	 * 上下文）。代价是模型要会写对代码——小模型在这上面会更吃力，所以
	 * 交给用户自己决定。
	 */
	CodeMode bool `json:"codeMode,omitempty"`
	/**
	 * 关掉命令沙箱。
	 *
	 * **默认是开的**，所以这里用「关掉」而不是「启用」——布尔的零值是 false，
	 * 而一个安全开关的零值必须落在安全那一侧。字段缺失、旧存档、忘了传，
	 * 三种情况下沙箱都还在。
	 */
	DisableSandbox bool `json:"disableSandbox,omitempty"`
	/**
	 * 是否启用 computer use（截屏 + 鼠标键盘）。
	 *
	 * 默认关。开了之后模型能看见并操作**整个屏幕**，不只是工作目录——
	 * 这是本应用里权限最大的一组工具，见 tools 包里 computer 的说明。
	 */
	EnableComputerUse bool `json:"enableComputerUse,omitempty"`
}

// SessionResumeParams 恢复一个会话。
//
// Refresh 是**当前**宿主配置里那几项「跟着配置走」的东西。不给的话恢复出来的
// 会话用的全是存档里的配置——用户新加的 MCP server、新装的技能、刚同步到的
// 内部平台能力一个都不会挂上，而应用启动就接着上次的会话，于是「配了就是不生效」，
// 除非他自己想到去开个新会话。工作目录和模型不在里面：前者换了会让历史里的
// 文件路径对不上，后者是会话自己的选择（端点由宿主另行对齐）。
type SessionResumeParams struct {
	SessionID string          `json:"sessionId"`
	Refresh   *SessionRefresh `json:"refresh,omitempty"`
}

// SessionRefresh 是恢复会话时用当前配置覆盖掉存档的那几项。
//
// 给了就整份生效（空 map = 一个 MCP server 都不挂），不做「空的就跳过」——
// 那样用户删掉最后一个 server 之后反而删不掉。
type SessionRefresh struct {
	MCPServers        map[string]MCPServerConfig `json:"mcpServers"`
	SkillDirs         []string                   `json:"skillDirs"`
	MemoryFile        string                     `json:"memoryFile"`
	EnableComputerUse bool                       `json:"enableComputerUse"`
	DisableSandbox    bool                       `json:"disableSandbox"`
	CodeMode          bool                       `json:"codeMode"`
	ApprovalPolicy    ApprovalPolicy             `json:"approvalPolicy"`
}

// SessionSearchParams 按关键词找会话。关键词为空时等于列全部。
type SessionSearchParams struct {
	Keyword string `json:"keyword"`
}

// MCPProbeParams 试连一个 MCP server 并列出它的工具。
//
// 与会话无关：用户在配置页填完地址就想知道「通不通、有哪些工具」，
// 而挂载只发生在开会话时，等到那时候报错，他已经离开配置页了。
type MCPProbeParams struct {
	Server MCPServerConfig `json:"server"`
}

// MCPProbeResult 是一次试连的结果。
//
// 失败不走 JSON-RPC 错误：这是「那个 server 连不上」，不是「这次调用坏了」，
// 宿主要把原因原样显示在那一条 server 上，而不是弹一个全局错误条。
type MCPProbeResult struct {
	OK    bool          `json:"ok"`
	Error string        `json:"error,omitempty"`
	Tools []MCPToolInfo `json:"tools,omitempty"`
}

// MCPToolInfo 是试连时列出的一个工具。
type MCPToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	/** 工具自己声明的 readOnlyHint。没声明就是 false——按有副作用处理。 */
	ReadOnly bool `json:"readOnly,omitempty"`
}

type SessionStartResult struct {
	SessionID string   `json:"sessionId"`
	Tools     []string `json:"tools"`
	/** MCP server 挂载结果，失败的在这里带原因，不让整个会话起不来。 */
	MCPStatus map[string]string `json:"mcpStatus,omitempty"`
	/**
	 * 会话当前用的模型。恢复旧会话时它可能与宿主配置的默认值不同——
	 * 会话记着自己的模型，顶部要显示的是这个而不是默认值。
	 */
	Model string `json:"model,omitempty"`
	/** 会话用的模型服务 id；0 表示走环境变量的 Key。 */
	ProviderID int64 `json:"providerId,omitempty"`
	/** 本次会话挂上的技能名。 */
	Skills []string `json:"skills,omitempty"`
	/** 会话工作区。空串表示没设置——界面要如实显示这一点。 */
	Workspace string `json:"workspace,omitempty"`
}

type TurnStartParams struct {
	SessionID string `json:"sessionId"`
	Text      string `json:"text"`
	/**
	 * 随这条消息一起发给模型的图片（PNG/JPEG 字节，JSON 上是 base64）。
	 *
	 * 缩放在宿主侧做：那边有 canvas，而且越早缩越少的字节走这条管子。
	 * 内核只兜住上限——一张没缩过的 4K 截图能把上下文和会话库一起撑坏。
	 */
	Images [][]byte `json:"images,omitempty"`
}

type TurnStartResult struct {
	TurnID string `json:"turnId"`
	/**
	 * true 表示这条输入被排进了正在跑的轮次，没有另起一轮。
	 * TurnID 此时是那个进行中轮次的 id，宿主不会再收到一次 turn/started。
	 */
	Queued bool `json:"queued,omitempty"`
}

type SessionIDParams struct {
	SessionID string `json:"sessionId"`
}

// SessionConfigureParams 改一个已存在会话的模型或工作区。
//
// 审批策略不在这里：它变了等于换了整个会话的安全前提，而历史还留着，
// 与其让它悄悄生效不如另起一个会话。
//
// 工作区在这里是因为它**本来就该能中途改**：用户开着会话聊着聊着才想起
// 「哦这事要在那个仓库里做」，让他为此新开一个会话、把上文再说一遍，
// 只是把一个纯粹的界面问题变成了他的负担。
type SessionConfigureParams struct {
	SessionID string      `json:"sessionId"`
	Model     ModelConfig `json:"model"`
	/** 新的会话工作区。nil 表示不动；指向空串表示清掉（回到「没设置」）。 */
	Workspace *string `json:"workspace,omitempty"`
}

// ---------- 条目 ----------

// ItemKind 是一个条目的类型。宿主按它决定怎么渲染。
type ItemKind string

const (
	ItemUserMessage  ItemKind = "userMessage"
	ItemAgentMessage ItemKind = "agentMessage"
	ItemReasoning    ItemKind = "reasoning"
	ItemToolCall     ItemKind = "toolCall"
	// ItemLLM 是一次模型采样。它也是一个执行步骤——一轮对话里模型可能被调用
	// 好几次（每次工具回填之后再问一次），不记下来的话用户看到的执行过程是
	// 断的：只有工具，没有「谁决定调这些工具」。
	ItemLLM   ItemKind = "llm"
	ItemError ItemKind = "error"
	// ItemNotice 是内核对用户说的一句话（历史被压缩了、正在重连），
	// 不是模型输出也不是错误。单独一类是因为宿主对这三者的渲染不同：
	// 错误弹红条、模型输出进对话、通知只在时间线上留一行。
	ItemNotice ItemKind = "notice"
)

type Item struct {
	ID   string   `json:"id"`
	Kind ItemKind `json:"kind"`
	/** 文本类条目的正文。 */
	Text string `json:"text,omitempty"`
	/**
	 * 用户消息带的图片（base64）。恢复会话时要能重新画出来，所以放在条目里
	 * 而不是只留在模型的消息历史里——不然点开旧会话，问题里那张截图就没了。
	 */
	Images [][]byte `json:"images,omitempty"`
	/** 工具调用条目的字段。 */
	ToolName string `json:"toolName,omitempty"`
	ToolArgs string `json:"toolArgs,omitempty"`
	/** 工具结果；失败时是错误文案。 */
	ToolResult string `json:"toolResult,omitempty"`
	ToolFailed bool   `json:"toolFailed,omitempty"`
	/** 给人看的一句话摘要，宿主直接显示，不用自己解析参数。 */
	Summary string `json:"summary,omitempty"`

	// ---------- 执行步骤的计时与统计 ----------
	//
	// 这几项让宿主能把执行过程画成一张带耗时的清单。慢在哪一步是排查时第一个
	// 要问的问题，而模型调用与工具调用的耗时经常差两个数量级。

	/** 这一步在轮次内的序号，从 1 开始。 */
	Seq int `json:"seq,omitempty"`
	/** 开始时刻，Unix 毫秒。 */
	StartedAt int64 `json:"startedAt,omitempty"`
	/** 耗时，毫秒。完成时才有。 */
	DurationMS int64 `json:"durationMs,omitempty"`

	// ---------- 仅 llm 条目 ----------

	/** 这次采样用的模型。同一轮里可能换模型（用户中途切了）。 */
	Model string `json:"model,omitempty"`
	/** 这是本轮的第几次采样，从 1 开始。 */
	Round int `json:"round,omitempty"`
	/** 首字节时间，毫秒。它和总耗时分开看：慢在等模型还是慢在生成，处理方式不同。 */
	TTFTMS int64 `json:"ttftMs,omitempty"`
	/** 推理耗时，毫秒。只有会给推理增量的模型才有。 */
	ThinkMS int64 `json:"thinkMs,omitempty"`
	/** 这次采样发起了几个工具调用。 */
	ToolCalls int `json:"toolCalls,omitempty"`
	/** 这次采样的 token 用量。 */
	Usage *Usage `json:"usage,omitempty"`
}

// SessionHistoryResult 把会话历史还原成宿主能直接渲染的条目。
//
// 内核存的是给模型看的消息，宿主要的是给人看的时间线，两者形状不同：
// 带 tool_calls 的 assistant 消息在模型那边是一条，在时间线上是「一句话 +
// 若干个工具步骤」。转换放在这里做，省得每个宿主各写一遍。
type SessionHistoryResult struct {
	Items []Item `json:"items"`
}

type ItemNotification struct {
	SessionID string `json:"sessionId"`
	TurnID    string `json:"turnId"`
	Item      Item   `json:"item"`
}

type ItemDeltaNotification struct {
	SessionID string `json:"sessionId"`
	TurnID    string `json:"turnId"`
	ItemID    string `json:"itemId"`
	Delta     string `json:"delta"`
}

type TurnNotification struct {
	SessionID string `json:"sessionId"`
	TurnID    string `json:"turnId"`
	/** 仅 turn/completed。 */
	Usage *Usage `json:"usage,omitempty"`
	/** 仅 turn/completed；异常结束时带原因。 */
	Error string `json:"error,omitempty"`
}

type Usage struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
	TotalTokens  int `json:"totalTokens"`
}

type ErrorNotification struct {
	SessionID string `json:"sessionId,omitempty"`
	Message   string `json:"message"`
}

// ---------- 审批 ----------

type ApprovalKind string

const (
	ApprovalExec  ApprovalKind = "exec"
	ApprovalWrite ApprovalKind = "write"
	ApprovalTool  ApprovalKind = "tool"
)

type ApprovalRequestParams struct {
	SessionID string       `json:"sessionId"`
	TurnID    string       `json:"turnId"`
	Kind      ApprovalKind `json:"kind"`
	/** 给人看的标题，例如「执行命令」。 */
	Title string `json:"title"`
	/** 完整命令或路径，原样展示，不截断——用户要据此做判断。 */
	Detail string `json:"detail"`
	Cwd    string `json:"cwd,omitempty"`
	Reason string `json:"reason,omitempty"`
	/**
	 * 这次审批涉及的目录。非空时宿主可以给出「本次会话都允许这个目录」。
	 *
	 * 给的是**目录**不是具体文件：用户想批准的是「往这儿写东西」这件事，
	 * 而不是一个文件名——按文件批，下一个文件又要问一次，等于没批。
	 */
	ScopePath string `json:"scopePath,omitempty"`
}

// ApprovalScope 是一次同意的作用范围。
type ApprovalScope string

const (
	// ApprovalScopeOnce 只这一次。不给就是这个。
	ApprovalScopeOnce ApprovalScope = "once"
	// ApprovalScopeSession 本次会话内，这个目录都不再问。
	//
	// **只对目录生效，不对命令生效。** 按命令字符串缓存「批过一次就一直批」
	// 是另一个需要单独评审的决定：命令是一段可以任意组合的文本，
	// 而目录是一个能说清楚边界的东西。
	ApprovalScopeSession ApprovalScope = "session"
)

type ApprovalResponse struct {
	Approved bool `json:"approved"`
	/** 作用范围。空或 once 表示只这一次。 */
	Scope ApprovalScope `json:"scope,omitempty"`
}

// RawMessage 便于在不解码的情况下转发。
type RawMessage = json.RawMessage
