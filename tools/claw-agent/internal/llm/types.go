package llm

import (
	"encoding/json"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ToolCall 是模型发起的一次工具调用。
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Message 是一条对话消息。
//
// ToolCallID 只在 RoleTool 上有值：Chat Completions 要求工具结果消息
// 通过它关联回是哪一次调用，缺了上游会报 400。
type Message struct {
	ID         string `json:",omitempty"`
	RequestID  string `json:",omitempty"`
	Role       Role
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
	// ProviderState 保存原生输出，供同一端点、协议和模型重放；不进入 UI。
	ProviderState *ProviderState `json:"providerState,omitempty"`
	// Artifacts and Sources are local UI metadata, omitted from model requests.
	Artifacts     []string                `json:"artifacts,omitempty"`
	OutputContent []protocol.ToolContent  `json:"outputContent,omitempty"`
	Sources       []protocol.SearchSource `json:"sources,omitempty"`
	// Images 是随这条消息一起发给模型的图片（PNG 字节）。
	//
	// 只在 RoleUser 上有意义：Chat Completions 的 tool 结果消息必须是纯字符串，
	// 没有地方放图。所以截屏这类工具的图是作为**紧随其后的一条 user 消息**
	// 送进去的，不是塞在工具结果里。
	Images [][]byte
	// At 是这条消息进历史的时间（Unix 毫秒）。只给界面显示用，不发给上游
	//（线格式见 wireMessage）。旧存档里没有这一项，是 0，界面上就不显示时间。
	At int64 `json:",omitempty"`
	// Shown 是界面上这条消息的样子，只在它和发给模型的不一样时才有值；
	// 也不发给上游。
	//
	// 不一样的情况：对话模型看不了图，图转成文字接在正文后面；音频的转写
	// 接在正文后面；截屏、中断标记、压缩摘要以 user 消息送进去（那不是用户说的话）。
	// 没有它，切回会话时还原出来的是给模型看的那一版：用户发的是一张图，
	// 看到的却是一大段转述。
	Shown *Shown `json:",omitempty"`
}

// Shown 见 Message.Shown。
type Shown struct {
	Text   string
	Images [][]byte `json:",omitempty"`
	// Hidden 表示这条不是用户说的（工具截屏的画面、中断标记、压缩摘要），
	// 时间线上不显示。
	Hidden bool `json:",omitempty"`
	// References 是这条消息里 @ 引用的会话，切回会话时还原成那排引用标签。
	References []Reference `json:",omitempty"`
}

// Reference 是一条消息里引用的另一个会话。
type Reference struct {
	ID    string
	Title string
}

// Tool 是提供给模型的工具定义。Parameters 是 JSON Schema。
type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

type Request struct {
	Model           string
	Messages        []Message
	Tools           []Tool
	Temperature     *float64
	MaxTokens       int
	ReasoningEffort string
	// A preference applies only when model metadata confirms support. Explicit
	// requests keep their value and are validated rather than silently downgraded.
	ReasoningPreference bool
	// DisableThinking requests a quick response on endpoints with known non-thinking controls.
	DisableThinking bool
}

type Usage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

// Response 是一轮模型调用的结果。
type Response struct {
	LocalItemID   string
	Content       string
	ToolCalls     []ToolCall
	FinishReason  string
	Usage         Usage
	ProviderState *ProviderState
}

// Delta 是流式过程中推给上层的增量。
type Delta struct {
	// Content 是正文增量。
	Content string
	// Reasoning 是推理摘要增量（部分模型会给）。
	Reasoning string
}
