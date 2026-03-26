package wechatlink

// 微信 iLink Bot API 类型定义（协议参考 github.com/fastclaw-ai/weclaw）。

const (
	MsgTypeUser = 1
	MsgTypeBot  = 2

	MsgStateFinish = 2

	ItemTypeText  = 1
	ItemTypeImage = 2

	TypingStatusTyping = 1
)

// Credentials 扫码登录成功后返回的凭据。
type Credentials struct {
	BotToken    string `json:"bot_token"`
	ILinkBotID  string `json:"ilink_bot_id"`
	BaseURL     string `json:"base_url"`
	ILinkUserID string `json:"ilink_user_id"`
}

// QRCodeResult 二维码获取结果。
type QRCodeResult struct {
	QRCode    string `json:"qrcode"`
	QRCodeURL string `json:"qrcode_url"`
}

// QRStatusResult 扫码状态轮询结果。
type QRStatusResult struct {
	Status string `json:"status"` // "wait" / "scaned" / "confirmed" / "expired"
	Credentials
}

// Message 归一化后的入站消息。
type Message struct {
	FromUserID   string
	Text         string
	ImageURLs    []string
	ContextToken string
}

// MessageItem 消息内容项。
type MessageItem struct {
	Type      int        `json:"type"`
	TextItem  *TextItem  `json:"text_item,omitempty"`
	ImageItem *ImageItem `json:"image_item,omitempty"`
}

// TextItem 文本内容。
type TextItem struct {
	Text string `json:"text"`
}

// ImageItem 图片内容。
type ImageItem struct {
	URL string `json:"url,omitempty"`
}

// ────────────────────── 内部协议结构 ──────────────────────

type baseInfo struct {
	ChannelVersion string `json:"channel_version,omitempty"`
}

type qrCodeResp struct {
	QRCode           string `json:"qrcode"`
	QRCodeImgContent string `json:"qrcode_img_content"`
}

type qrStatusResp struct {
	Status      string `json:"status"`
	BotToken    string `json:"bot_token"`
	ILinkBotID  string `json:"ilink_bot_id"`
	BaseURL     string `json:"baseurl"`
	ILinkUserID string `json:"ilink_user_id"`
}

type getUpdatesReq struct {
	GetUpdatesBuf string   `json:"get_updates_buf"`
	BaseInfo      baseInfo `json:"base_info"`
}

type getUpdatesResp struct {
	Ret           int            `json:"ret"`
	ErrMsg        string         `json:"errmsg,omitempty"`
	Msgs          []rawWeixinMsg `json:"msgs"`
	GetUpdatesBuf string         `json:"get_updates_buf"`
}

type rawWeixinMsg struct {
	FromUserID   string        `json:"from_user_id"`
	ToUserID     string        `json:"to_user_id"`
	MessageType  int           `json:"message_type"`
	MessageState int           `json:"message_state"`
	ItemList     []MessageItem `json:"item_list"`
	ContextToken string        `json:"context_token"`
}

type sendMessageReq struct {
	Msg      sendMsg  `json:"msg"`
	BaseInfo baseInfo `json:"base_info"`
}

type sendMsg struct {
	FromUserID   string        `json:"from_user_id"`
	ToUserID     string        `json:"to_user_id"`
	ClientID     string        `json:"client_id"`
	MessageType  int           `json:"message_type"`
	MessageState int           `json:"message_state"`
	ItemList     []MessageItem `json:"item_list"`
	ContextToken string        `json:"context_token"`
}

type sendMessageResp struct {
	Ret    int    `json:"ret"`
	ErrMsg string `json:"errmsg,omitempty"`
}

type getConfigReq struct {
	ILinkUserID  string   `json:"ilink_user_id"`
	ContextToken string   `json:"context_token,omitempty"`
	BaseInfo     baseInfo `json:"base_info"`
}

type getConfigResp struct {
	Ret          int    `json:"ret"`
	ErrMsg       string `json:"errmsg,omitempty"`
	TypingTicket string `json:"typing_ticket,omitempty"`
}

type sendTypingReq struct {
	ILinkUserID  string   `json:"ilink_user_id"`
	TypingTicket string   `json:"typing_ticket"`
	Status       int      `json:"status"`
	BaseInfo     baseInfo `json:"base_info"`
}

type sendTypingResp struct {
	Ret    int    `json:"ret"`
	ErrMsg string `json:"errmsg,omitempty"`
}
