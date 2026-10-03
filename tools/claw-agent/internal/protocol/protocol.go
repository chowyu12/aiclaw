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
	MethodInitialize    = "initialize"
	MethodSessionStart  = "session/start"
	MethodSessionResume = "session/resume"
	MethodSessionList   = "session/list"
	MethodSessionSearch = "session/search"
	MethodSessionRename = "session/rename"
	MethodSessionDelete = "session/delete"
	// MethodSessionArchive 归档 / 恢复一个会话（连同它开出的子 agent）。
	MethodSessionArchive = "session/archive"
	// MethodSessionArchived 列出归档了的会话。
	MethodSessionArchived  = "session/listArchived"
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
	MethodProviderAutoMark = "provider/autoMark"
	MethodPluginList       = "plugin/list"
	MethodPluginInstall    = "plugin/install"
	MethodPluginToggle     = "plugin/toggle"
	MethodPluginDelete     = "plugin/delete"
	MethodPluginConfig     = "plugin/config"
	MethodPluginSetConfig  = "plugin/setConfig"
	MethodPluginContrib    = "plugin/contributions"
	MethodChannelStatus    = "channel/status"
	MethodChannelBindings  = "channel/bindings"
	MethodChannelAuthorize = "channel/authorize"
	MethodChannelRevoke    = "channel/revoke"
	MethodConnectionCreate = "channel/connectionCreate"
	MethodConnectionRename = "channel/connectionRename"
	MethodConnectionDelete = "channel/connectionDelete"
	MethodChannelMedia     = "channel/media"
	MethodUsageSummary     = "usage/summary"
	MethodWeChatLoginStart = "wechat/loginStart"
	MethodWeChatLoginPoll  = "wechat/loginPoll"
	MethodSearchList       = "search/list"
	MethodSearchCreate     = "search/create"
	MethodSearchUpdate     = "search/update"
	MethodSearchDelete     = "search/delete"
	MethodSearchTest       = "search/test"
	MethodEmailTest        = "plugin/emailTest"
	// 界面语言：内核给人看的提示、报错跟着它（启动时由环境变量 AICLAW_LOCALE 带进来）。
	MethodConfigLocale    = "config/locale"
	MethodAudioTranscribe = "audio/transcribe"
	MethodShutdown        = "shutdown"
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
	// RequestBrowser 请宿主在应用自带的浏览器窗口里做一步：打开、点、填、读。
	// 浏览器是宿主（Electron）的，内核只翻译工具调用。
	RequestBrowser = "browser/request"
	// RequestUserInput 请用户回答模型提的一个问题（ask_user 工具）。宿主在对话里
	// 显示一张带选项的卡片，用户选了、写了或跳过之后回应。
	RequestUserInput = "userInput/request"
	// RequestSchedule 请宿主管理定时任务（建、列、删）。调度在宿主：到点时由宿主用
	// 当前的应用配置开一个新会话发出任务，内核只翻译工具调用。
	RequestSchedule = "schedule/request"
)

// ScheduleRequestParams 是模型对定时任务的一次操作。
type ScheduleRequestParams struct {
	RunID     string `json:"runId,omitempty"`
	Result    string `json:"result,omitempty"`
	ResultKey string `json:"resultKey,omitempty"`
	Complete  bool   `json:"complete,omitempty"`
	SessionID string `json:"sessionId"`
	/** list / create / delete */
	Action string `json:"action"`
	/** delete 时给。 */
	ID   string             `json:"id,omitempty"`
	Task *ScheduleTaskInput `json:"task,omitempty"`
}

// ScheduleTaskInput 是新建的定时任务。时间规则与宿主 shared/schedule.ts 一致。
type ScheduleTaskInput struct {
	Mode               string `json:"mode,omitempty"`
	NotificationPolicy string `json:"notificationPolicy,omitempty"`
	StopWhen           string `json:"stopWhen,omitempty"`
	Name               string `json:"name"`
	Prompt             string `json:"prompt"`
	/** daily / weekdays / weekly / interval / once */
	Kind string `json:"kind"`
	/** HH:MM */
	Time string `json:"time,omitempty"`
	/** 0 = 周日 … 6 = 周六 */
	Days         []int  `json:"days,omitempty"`
	EveryMinutes int    `json:"everyMinutes,omitempty"`
	At           string `json:"at,omitempty"`
	/** 任务跑在哪个工作区；空表示不设。 */
	Workspace string `json:"workspace,omitempty"`
}

// ScheduleResult 是宿主的回应：写好给模型看的一段话。
type ScheduleResult struct {
	Text string `json:"text"`
}

// UserInputOption 是问题的一个选项。
type UserInputOption struct {
	Label string `json:"label"`
	/** 选项的补充说明，卡片上以小字显示。 */
	Description string `json:"description,omitempty"`
}

// UserInputRequestParams 是模型要问用户的一个问题。
type UserInputRequestParams struct {
	SessionID string            `json:"sessionId"`
	TurnID    string            `json:"turnId"`
	Question  string            `json:"question"`
	Options   []UserInputOption `json:"options,omitempty"`
	/** 可以选多个。 */
	MultiSelect bool `json:"multiSelect,omitempty"`
}

// UserInputResponse 是用户的回答。
type UserInputResponse struct {
	/** 选中的选项（label）。 */
	Selected []string `json:"selected,omitempty"`
	/** 用户自己写的回答。 */
	Text string `json:"text,omitempty"`
	/** 用户跳过了这个问题。 */
	Skipped bool `json:"skipped,omitempty"`
}

// BrowserAction 是一次浏览器操作。
type BrowserAction string

const (
	BrowserNavigate   BrowserAction = "navigate"
	BrowserSnapshot   BrowserAction = "snapshot"
	BrowserClick      BrowserAction = "click"
	BrowserType       BrowserAction = "type"
	BrowserSelect     BrowserAction = "select"
	BrowserScroll     BrowserAction = "scroll"
	BrowserBack       BrowserAction = "back"
	BrowserKey        BrowserAction = "key"
	BrowserExtract    BrowserAction = "extract"
	BrowserScreenshot BrowserAction = "screenshot"
	// BrowserTabs 列出用户浏览器里的网页标签页（只在「用我的浏览器」模式下有意义）。
	BrowserTabs BrowserAction = "tabs"
	// BrowserUseTab 接管用户浏览器里一个已经打开的标签页。
	BrowserUseTab BrowserAction = "use_tab"
)

type BrowserRequestParams struct {
	SessionID string        `json:"sessionId"`
	TurnID    string        `json:"turnId"`
	Action    BrowserAction `json:"action"`
	/** navigate 的网址。 */
	URL string `json:"url,omitempty"`
	/** click / type / select / scroll 的元素编号，来自快照。-1 表示没给。 */
	Index int `json:"index"`
	/** type 的文本。 */
	Text string `json:"text,omitempty"`
	/** type 填完是否按回车。 */
	Submit bool `json:"submit,omitempty"`
	/** select 的选项。 */
	Value string `json:"value,omitempty"`
	/** scroll 的纵向滚动量，正数向下。 */
	DY int `json:"dy,omitempty"`
	/** key 的按键名。 */
	Keys string `json:"keys,omitempty"`
	/** use_tab 的标签页编号，来自 tabs。 */
	TabID int `json:"tabId,omitempty"`
}

type BrowserResult struct {
	/** 给模型看的文本：页面状态、编号列表或正文。 */
	Text string `json:"text"`
	/** 截图时的 PNG，base64。 */
	ImageBase64 string `json:"imageBase64,omitempty"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
}

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
	BaseURL   string `json:"baseUrl"`
	APIKeySet bool   `json:"apiKeySet"`
	/**
	 * 模型清单。带能力标记的写成 `名字#vision,image`（见 roles.go）；
	 * 没有 `#` 的就是只做对话——旧版写下的清单原样可读。
	 */
	Models  []string `json:"models"`
	Enabled bool     `json:"enabled"`
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

// ProviderAutoMarkResult 是按公开能力表（models.dev + LiteLLM）自动标记能力的结果。
//
// 带上匹配与未匹配的数量：表未必收了用户用的每个模型，说清楚「有几个
// 没查到」用户才知道剩下的要自己勾，而不是以为同步没生效。
type ProviderAutoMarkResult struct {
	Provider  ProviderView `json:"provider"`
	Matched   int          `json:"matched"`
	Unmatched int          `json:"unmatched"`
	/** 表里没有、按模型名猜出来的数量。猜的只加不减。 */
	Guessed int `json:"guessed,omitempty"`
	/** 某份表没拉到时的说明；两份都齐时为空。 */
	Note string `json:"note,omitempty"`
}

// ---------- 搜索引擎 ----------
//
// 联网搜索引擎（Tavily / SerpAPI / 阿里云 IQS）各带 Key。搜索工具是随内核分发的
// MCP server（`claw-agent mcp-search`），宿主在有启用中的引擎时挂进会话。

type SearchEngineView struct {
	ID int64 `json:"id"`
	/** tavily / serpapi / aliyun-iqs */
	Provider string `json:"provider"`
	Name     string `json:"name"`
	/** 空表示用该类型的默认端点。 */
	BaseURL   string `json:"baseUrl"`
	APIKeySet bool   `json:"apiKeySet"`
	Enabled   bool   `json:"enabled"`
}

type SearchEngineCreateParams struct {
	Provider string `json:"provider"`
	Name     string `json:"name,omitempty"`
	BaseURL  string `json:"baseUrl,omitempty"`
	APIKey   string `json:"apiKey,omitempty"`
	Enabled  bool   `json:"enabled,omitempty"`
}

// SearchEngineUpdateParams 里 nil 的字段不动。APIKey 指向空串表示清掉。
type SearchEngineUpdateParams struct {
	ID       int64   `json:"id"`
	Provider *string `json:"provider,omitempty"`
	Name     *string `json:"name,omitempty"`
	BaseURL  *string `json:"baseUrl,omitempty"`
	APIKey   *string `json:"apiKey,omitempty"`
	Enabled  *bool   `json:"enabled,omitempty"`
}

type SearchEngineIDParams struct {
	ID int64 `json:"id"`
}

// SearchEngineTestParams 用一个引擎真搜一次，配置页的「试一下」。
type SearchEngineTestParams struct {
	ID    int64  `json:"id"`
	Query string `json:"query"`
}

type SearchHit struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

type SearchEngineTestResult struct {
	Provider string      `json:"provider"`
	Results  []SearchHit `json:"results"`
}

// ---------- 插件 ----------
//
// 插件是一个带 plugin.json 的目录（见 internal/plugin 的说明）：声明权限、
// 配置项，以及它贡献的技能、MCP server、宿主能力（computer use）与通道
// （微信、企业微信）。记录与配置都在模型配置库那个 SQLite 里。

type PluginView struct {
	UUID        string `json:"uuid"`
	PluginID    string `json:"pluginId,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version,omitempty"`
	/** builtin 随应用分发，只能停用不能删；local 是用户从目录装的。 */
	Source  string `json:"source"`
	Enabled bool   `json:"enabled"`
	/** 各类贡献的数量，列表页上一眼看出这个插件是干什么的。 */
	Skills   int `json:"skills"`
	MCP      int `json:"mcp"`
	Tools    int `json:"tools"`
	Channels int `json:"channels"`
	/** 启用后会拿到的权限名。 */
	Permissions []string `json:"permissions"`
	/** 还没填的必填配置项；非空时启用会被拒绝。 */
	MissingConfig []string `json:"missingConfig"`
	/** 渠道插件的连接（一个微信号、一个企微机器人一个）。别的插件是空的。 */
	Connections []ChannelConnectionView `json:"connections"`
}

// ChannelConnectionView 是渠道插件的一个连接。
type ChannelConnectionView struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
	/** 这个连接还没填的必填配置；非空时它不会启动。 */
	MissingConfig []string `json:"missingConfig"`
}

type ConnectionCreateParams struct {
	PluginUUID string `json:"pluginUuid"`
	Name       string `json:"name"`
}

type ConnectionRenameParams struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

// PluginConfigParams 读一个插件（或它的某个连接）的配置。
type PluginConfigParams struct {
	UUID string `json:"uuid"`
	/** 渠道插件的连接 id；空是插件本身的配置。 */
	ConnectionID string `json:"connectionId,omitempty"`
}

type PluginInstallParams struct {
	/** 插件目录的绝对路径。宿主用系统对话框选出来后传进来。 */
	Path string `json:"path"`
}

type PluginToggleParams struct {
	UUID    string `json:"uuid"`
	Enabled bool   `json:"enabled"`
}

type PluginUUIDParams struct {
	UUID string `json:"uuid"`
}

// PluginConfigField 是一个配置项。秘密只报「配了没有」，值永不回传。
type PluginConfigField struct {
	Key         string `json:"key"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required"`
	Secret      bool   `json:"secret"`
	IsSet       bool   `json:"isSet"`
	Value       string `json:"value,omitempty"`
}

type PluginConfigResult struct {
	Fields []PluginConfigField `json:"fields"`
}

type PluginSetConfigParams struct {
	UUID string `json:"uuid"`
	/** 渠道插件的连接 id；空是插件本身的配置。 */
	ConnectionID string `json:"connectionId,omitempty"`
	Key          string `json:"key"`
	/** 空串表示清掉。 */
	Value string `json:"value"`
}

// PluginContributions 是启用中的插件合起来贡献给会话的东西。
// 宿主开会话时把它并进 session/start 的参数。
type PluginContributions struct {
	/** 键是挂载名（也是工具名前缀）。 */
	MCPServers map[string]MCPServerConfig `json:"mcpServers"`
	Skills     []PluginSkill              `json:"skills"`
	/** 有启用中的插件贡献了 computer use。 */
	ComputerUse bool `json:"computerUse"`
	/** 启用了邮件插件（且填好了邮箱）。 */
	Email bool `json:"email,omitempty"`
}

// AudioTranscribeParams 是语音输入：宿主录好的一段音频，用听写角色转成文字。
// 与会话无关——转出来的字先回到输入框，用户看过、改过才发出去。
type AudioTranscribeParams struct {
	/** 音频内容，base64。 */
	Audio string `json:"audio"`
	/** 文件名，只用来带出格式（扩展名）。 */
	Name string    `json:"name"`
	Role RoleModel `json:"role"`
}

type AudioTranscribeResult struct {
	Text string `json:"text"`
}

// EmailTestResult 是「测试邮箱」的结果：连得上时带回实际用的服务器，界面上好让用户看见。
type EmailTestResult struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	IMAPHost string `json:"imapHost,omitempty"`
	IMAPPort int    `json:"imapPort,omitempty"`
	SMTPHost string `json:"smtpHost,omitempty"`
	SMTPPort int    `json:"smtpPort,omitempty"`
}

// PluginSkill 是插件带来的一个技能目录（里面直接放 SKILL.md）。
type PluginSkill struct {
	Dir string `json:"dir"`
	/** 界面上「来自哪儿」显示插件名。 */
	PluginName string `json:"pluginName"`
	PluginUUID string `json:"pluginUuid"`
}

// ---------- 通道 ----------

// ChannelStatusView 是一个正在被托管的通道的健康状况。
type ChannelStatusView struct {
	PluginUUID  string `json:"pluginUuid"`
	PluginName  string `json:"pluginName"`
	ChannelID   string `json:"channelId"`
	DisplayName string `json:"displayName,omitempty"`
	/** 这个实例服务的连接。 */
	ConnectionID   string `json:"connectionId,omitempty"`
	ConnectionName string `json:"connectionName,omitempty"`
	/** starting / running / retrying / failed / stopped */
	State     string `json:"state"`
	Attempts  int    `json:"attempts,omitempty"`
	LastError string `json:"lastError,omitempty"`
}

// ChannelBindingView 是通道见过的一个外部会话（群或单聊）。
//
// 未授权的也在列表里：收到消息只是记下来等用户放行，绝不因此起一轮——
// 一轮会跑工具，而发消息的人不是本机用户。
type ChannelBindingView struct {
	PluginUUID string `json:"pluginUuid"`
	ChannelID  string `json:"channelId"`
	/** 从哪个连接进来的。 */
	ConnectionID   string `json:"connectionId"`
	ConnectionName string `json:"connectionName,omitempty"`
	ExternalKey    string `json:"externalKey"`
	DisplayName    string `json:"displayName,omitempty"`
	/** 这个外部会话对应的本地会话 id；还没聊过是空。 */
	SessionID  string `json:"sessionId,omitempty"`
	ProviderID int64  `json:"providerId,omitempty"`
	Model      string `json:"model,omitempty"`
	Allowed    bool   `json:"allowed"`
	/** 除只读工具外还放开了哪些内置工具。 */
	AllowedTools []string `json:"allowedTools"`
	LastMessage  string   `json:"lastMessage,omitempty"`
}

type ChannelBindingKey struct {
	PluginUUID   string `json:"pluginUuid"`
	ChannelID    string `json:"channelId"`
	ConnectionID string `json:"connectionId"`
	ExternalKey  string `json:"externalKey"`
}

type ChannelAuthorizeParams struct {
	ChannelBindingKey
	ProviderID   int64    `json:"providerId"`
	Model        string   `json:"model"`
	AllowedTools []string `json:"allowedTools,omitempty"`
}

// ---------- 微信扫码登录 ----------

type WeChatLoginStartResult struct {
	/** 轮询时用来标识这次登录。 */
	Token string `json:"token"`
	/** 二维码，PNG data URI，直接放进 <img src>。 */
	Image string `json:"image"`
}

type WeChatLoginPollParams struct {
	UUID  string `json:"uuid"`
	Token string `json:"token"`
	/** 给哪个连接重新登录；空表示登录成功后新建一个连接（添加一个微信号）。 */
	ConnectionID string `json:"connectionId,omitempty"`
}

type WeChatLoginPollResult struct {
	/** wait / scaned / confirmed / expired */
	Status string `json:"status"`
	/** 凭据已写进插件配置。凭据本身不回传。 */
	Saved bool `json:"saved"`
	/** 凭据写进了哪个连接（新建的或原来的）。 */
	ConnectionID string `json:"connectionId,omitempty"`
}

// MCPServerConfig 是一个要挂载的 MCP server。
//
// 两种传输二选一：填了 URL 走 Streamable HTTP（远程），否则把 Command
// 当子进程拉起来走 stdio（本地）。
type MCPServerConfig struct {
	OAuth bool `json:"oauth,omitempty"`
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
	// 通道会话（微信、企业微信）用的就是它：另一头没有人能点「允许」。
	ApprovalNever ApprovalPolicy = "never"
	// ApprovalBypass 不弹确认，需要审批的调用直接放行。危险命令的硬拒绝不受影响。
	// 与 never 的区别是失败还是执行：这是给完全信任这台机器上的任务用的档位。
	ApprovalBypass ApprovalPolicy = "bypass"
)

type SessionStartParams struct {
	ForkSourceID string      `json:"forkSourceId,omitempty"`
	ForkItemID   string      `json:"forkItemId,omitempty"`
	Model        ModelConfig `json:"model"`
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
	 * ~/.agents/skills、Claude Code 的 ~/.claude/skills、Codex 的
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
	/**
	 * 是否启用浏览器工具：宿主开一个独立的浏览器窗口，模型按元素编号打开、点、填、读。
	 * 默认关。它只能碰那一个窗口，比 computer use 的权限小得多，但打开网址仍要确认。
	 */
	EnableBrowser bool `json:"enableBrowser,omitempty"`
	/**
	 * 是否挂上邮件工具（列信、读信、发信、回信）。宿主按设置传（默认开）；内核在
	 * 没配邮箱时把它当成关，所以模型看不到一组永远报「没配置」的工具。
	 */
	EnableEmail bool `json:"enableEmail,omitempty"`
	/** 是否挂上定时任务工具（建、列、删）。只给用户自己的会话。 */
	EnableSchedule bool `json:"enableSchedule,omitempty"`
	/** 会话一开始的标题。空的话由第一条消息生成；定时任务用它写上任务名。 */
	Title string `json:"title,omitempty"`
	/** 界面语言（en / zh-CN）。决定模型默认用什么语言回复；用户用别的语言提问时跟着用户。 */
	Locale string `json:"locale,omitempty"`
	/** 子 agent：父会话的 id 与自己在协作树上的路径（/root/research）。普通会话为空。 */
	ParentID  string `json:"parentId,omitempty"`
	AgentPath string `json:"agentPath,omitempty"`
	/**
	 * 对话之外的角色模型（看图、听写、朗读、画图）。见 roles.go。
	 * 没配的角色对应的工具不注册——模型看不到一个用不了的工具。
	 */
	Roles RoleModels `json:"roles,omitzero"`
	/** 对话模型自己看得懂图。宿主按模型清单里的标记给出，决定要不要走视觉旁路。 */
	ModelSeesImages bool `json:"modelSeesImages,omitempty"`
}

// UsageSummaryParams 查最近多少天的用量（设置页「用量」）。结果的形状见 store.UsageSummary。
type UsageSummaryParams struct {
	Days int `json:"days"`
}

// ChannelMediaParams 是通道会话处理图片与语音要用的角色配置。
//
// 通道会话由内核自己建，拿不到宿主的配置；宿主在启动时和每次保存设置时推一份
// 过来。没推之前通道会话不走视觉旁路——模型认图就直接看，不认图就只能说看不了。
type ChannelMediaParams struct {
	Roles RoleModels `json:"roles,omitzero"`
}

// SessionResumeParams 恢复一个会话。
//
// Refresh 是**当前**宿主配置里那几项「跟着配置走」的东西。不给的话恢复出来的
// 会话用的全是存档里的配置——用户新加的 MCP server、新装的技能、刚同步到的
// 新加的 server 一个都不会挂上，而应用启动就接着上次的会话，于是「配了就是不生效」，
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
	EnableBrowser     bool                       `json:"enableBrowser,omitempty"`
	EnableEmail       bool                       `json:"enableEmail,omitempty"`
	EnableSchedule    bool                       `json:"enableSchedule,omitempty"`
	Locale            string                     `json:"locale,omitempty"`
	DisableSandbox    bool                       `json:"disableSandbox"`
	CodeMode          bool                       `json:"codeMode"`
	ApprovalPolicy    ApprovalPolicy             `json:"approvalPolicy"`
	Roles             RoleModels                 `json:"roles,omitzero"`
	ModelSeesImages   bool                       `json:"modelSeesImages,omitempty"`
}

// SessionArchiveParams 归档（archived=true）或恢复一个会话。
type SessionArchiveParams struct {
	SessionID string `json:"sessionId"`
	Archived  bool   `json:"archived"`
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
	/**
	 * 代码模式下被收进 exec 的工具名。Tools 里只剩 exec 一个，但这些工具模型
	 * 仍然能在脚本里调——界面不列出来的话，用户会以为它们没挂上。
	 */
	FoldedTools []string `json:"foldedTools,omitempty"`
	/** 会话工作区。空串表示没设置——界面要如实显示这一点。 */
	Workspace string `json:"workspace,omitempty"`
}

type TurnStartParams struct {
	IdleOnly  bool   `json:"idleOnly,omitempty"`
	RequestID string `json:"requestId,omitempty"`
	SessionID string `json:"sessionId"`
	Text      string `json:"text"`
	/**
	 * 随这条消息一起发给模型的图片（PNG/JPEG 字节，JSON 上是 base64）。
	 *
	 * 缩放在宿主侧做：那边有 canvas，而且越早缩越少的字节走这条管子。
	 * 内核只兜住上限——一张没缩过的 4K 截图能把上下文和会话库一起撑坏。
	 */
	Images [][]byte `json:"images,omitempty"`
	/**
	 * 随这条消息附上的音频文件路径，由内核用听写模型转成文字并进消息。
	 *
	 * 给路径而不是字节：行协议的单帧上限是 16MB，而一段几分钟的录音 base64
	 * 之后就超了——超了的表现是「协议帧解析失败」，没人能从那句话联想到
	 * 是附件太大。宿主把文件暂存到磁盘，这里只传路径。
	 */
	AudioPaths []string `json:"audioPaths,omitempty"`
	/**
	 * 这条消息里 @ 引用的其他会话（参照 Codex 的 task mentions）。内核在给模型的那份
	 * 消息里附上引用说明，要求它先用 read_thread 读；界面上显示的仍是用户的原话。
	 */
	References []ThreadRef `json:"references,omitempty"`
}

// ThreadRef 是一条消息引用的另一个会话。
type ThreadRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
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
	RequestID string   `json:"requestId,omitempty"`
	ID        string   `json:"id"`
	Kind      ItemKind `json:"kind"`
	/** 用户消息与助手消息的时间（Unix 毫秒）。界面在消息下面显示；0 表示不知道（旧存档）。 */
	At int64 `json:"at,omitempty"`
	/** 文本类条目的正文。 */
	Text string `json:"text,omitempty"`
	/**
	 * 用户消息带的图片（base64）。恢复会话时要能重新画出来，所以放在条目里
	 * 而不是只留在模型的消息历史里——不然点开旧会话，问题里那张截图就没了。
	 */
	Images [][]byte `json:"images,omitempty"`
	/** 用户消息里 @ 引用的会话。 */
	References []ThreadRef `json:"references,omitempty"`
	/** 工具调用条目的字段。 */
	ToolName string `json:"toolName,omitempty"`
	ToolArgs string `json:"toolArgs,omitempty"`
	/** 工具结果；失败时是错误文案。 */
	ToolResult string `json:"toolResult,omitempty"`
	ToolFailed bool   `json:"toolFailed,omitempty"`
	/** 给人看的一句话摘要，宿主直接显示，不用自己解析参数。 */
	Summary string `json:"summary,omitempty"`
	/**
	 * 这一步产出的文件（相对工作区）：生成的图、合成的语音。
	 *
	 * 只给路径不给字节：图片进时间线与会话库会让两者都胀几个数量级，而界面
	 * 要显示时按路径读一次就够了。
	 */
	Artifacts []string `json:"artifacts,omitempty"`

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

// ConfigLocaleParams 是 config/locale 的参数。
type ConfigLocaleParams struct {
	Locale string `json:"locale"`
}

// SessionRenameParams changes only the displayed conversation name.
type SessionRenameParams struct {
	SessionID string `json:"sessionId"`
	Title     string `json:"title"`
}
