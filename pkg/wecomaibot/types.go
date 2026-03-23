package wecomaibot

import "encoding/json"

// WsCmd WebSocket 命令（与企微智能机器人长连接协议一致）。
var WsCmd = struct {
	SUBSCRIBE        string
	HEARTBEAT        string
	RESPONSE         string
	RESPONSE_WELCOME string
	RESPONSE_UPDATE  string
	SEND_MSG         string
	CALLBACK         string
	EVENT_CALLBACK   string
}{
	SUBSCRIBE:        "aibot_subscribe",
	HEARTBEAT:        "ping",
	RESPONSE:         "aibot_respond_msg",
	RESPONSE_WELCOME: "aibot_respond_welcome_msg",
	RESPONSE_UPDATE:  "aibot_respond_update_msg",
	SEND_MSG:         "aibot_send_msg",
	CALLBACK:         "aibot_msg_callback",
	EVENT_CALLBACK:   "aibot_event_callback",
}

// MessageType 入站消息 msgtype。
type MessageType string

const (
	MessageTypeText  MessageType = "text"
	MessageTypeImage MessageType = "image"
	MessageTypeMixed MessageType = "mixed"
	MessageTypeVoice MessageType = "voice"
	MessageTypeFile  MessageType = "file"
)

// EventType 事件 eventtype。
type EventType string

const (
	EventTypeEnterChat         EventType = "enter_chat"
	EventTypeTemplateCardEvent EventType = "template_card_event"
	EventTypeFeedbackEvent     EventType = "feedback_event"
)

// WsFrame WebSocket 帧。
type WsFrame struct {
	Cmd     string          `json:"cmd,omitempty"`
	Headers WsFrameHeaders  `json:"headers"`
	Body    json.RawMessage `json:"body,omitempty"`
	ErrCode int             `json:"errcode,omitempty"`
	ErrMsg  string          `json:"errmsg,omitempty"`
}

// WsFrameHeaders 帧头。
type WsFrameHeaders struct {
	ReqID string `json:"req_id"`
}

// MessageFrom 发送者。
type MessageFrom struct {
	UserID string `json:"userid"`
}

// TextContent 文本块。
type TextContent struct {
	Content string `json:"content"`
}

// ImageContent 图片。
type ImageContent struct {
	URL    string `json:"url"`
	AesKey string `json:"aeskey"`
}

// MixedMsgItem 混排子项。
type MixedMsgItem struct {
	MsgType string        `json:"msgtype"`
	Text    *TextContent  `json:"text,omitempty"`
	Image   *ImageContent `json:"image,omitempty"`
}

// MixedContent 混排。
type MixedContent struct {
	MsgItem []MixedMsgItem `json:"msg_item"`
}

// BaseMessage 公共字段。
type BaseMessage struct {
	MsgID    string      `json:"msgid"`
	AibotID  string      `json:"aibotid"`
	ChatID   string      `json:"chatid,omitempty"`
	ChatType string      `json:"chattype"`
	From     MessageFrom `json:"from"`
	MsgType  string      `json:"msgtype"`
}

// TextMessage 纯文本。
type TextMessage struct {
	BaseMessage
	Text TextContent `json:"text"`
}

// ImageMessage 纯图片。
type ImageMessage struct {
	BaseMessage
	Image ImageContent `json:"image"`
}

// MixedMessage 图文混排。
type MixedMessage struct {
	BaseMessage
	Mixed MixedContent `json:"mixed"`
}

// ReplyMsgItem 流式回复中的图片项（协议字段，当前 hub 未使用）。
type ReplyMsgItem struct {
	MsgType string `json:"msgtype"`
	Image   struct {
		Base64 string `json:"base64"`
		MD5    string `json:"md5"`
	} `json:"image"`
}

// ReplyFeedback 流式回复反馈（协议字段，当前 hub 未使用）。
type ReplyFeedback struct {
	ID string `json:"id"`
}

// WSClientOptions 客户端选项。
type WSClientOptions struct {
	BotID                string
	Secret               string
	ReconnectInterval    int
	MaxReconnectAttempts int
	HeartbeatInterval    int
	WSURL                string
	Logger               Logger
}
