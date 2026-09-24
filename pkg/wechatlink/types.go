package wechatlink

// 微信 iLink Bot API 类型定义（协议参考 github.com/fastclaw-ai/weclaw）。

const (
	MsgTypeUser = 1
	MsgTypeBot  = 2

	MsgStateFinish = 2

	ItemTypeText  = 1
	ItemTypeImage = 2
	ItemTypeVoice = 3
	ItemTypeFile  = 4
	ItemTypeVideo = 5

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
//
// 语音不单列：微信自己把语音转成了文字（voice_item.text），接在 Text 里。
// 转不出文字的语音记一句说明，不下载原音频——那是 silk 编码，听写模型不认。
type Message struct {
	FromUserID   string
	Text         string
	Images       []ImageSource
	Files        []FileSource
	ContextToken string
}

// MessageItem 消息内容项。
type MessageItem struct {
	Type      int        `json:"type"`
	TextItem  *TextItem  `json:"text_item,omitempty"`
	ImageItem *ImageItem `json:"image_item,omitempty"`
	VoiceItem *VoiceItem `json:"voice_item,omitempty"`
	FileItem  *FileItem  `json:"file_item,omitempty"`
	VideoItem *VideoItem `json:"video_item,omitempty"`
	// RefMsg 是被引用的那条消息：用户常常引用一张图再问「这是什么」。
	RefMsg *RefMessage `json:"ref_msg,omitempty"`
}

// RefMessage 引用的消息。
type RefMessage struct {
	MessageItem *MessageItem `json:"message_item,omitempty"`
	Title       string       `json:"title,omitempty"`
}

// VoiceItem 语音。Text 是微信转写好的文字。
type VoiceItem struct {
	Media    *MediaInfo `json:"media,omitempty"`
	Playtime int        `json:"playtime,omitempty"`
	Text     string     `json:"text,omitempty"`
}

// FileItem 文件。
type FileItem struct {
	Media    *MediaInfo `json:"media,omitempty"`
	FileName string     `json:"file_name,omitempty"`
	Len      string     `json:"len,omitempty"`
}

// VideoItem 视频。
type VideoItem struct {
	Media *MediaInfo `json:"media,omitempty"`
}

// TextItem 文本内容。
type TextItem struct {
	Text string `json:"text"`
}

// MediaInfo CDN 加密媒体引用。
type MediaInfo struct {
	EncryptQueryParam string `json:"encrypt_query_param"`
	AESKey            string `json:"aes_key"`
	EncryptType       int    `json:"encrypt_type"`
	// FullURL 是服务端给好的完整下载地址；有它就不自己拼。
	FullURL string `json:"full_url,omitempty"`
}

// ImageItem 图片内容。
type ImageItem struct {
	URL   string     `json:"url,omitempty"`
	Media *MediaInfo `json:"media,omitempty"`
	// AESKey 是 16 字节密钥的 hex 串。有它时优先于 Media.AESKey（官方插件的做法）。
	AESKey  string `json:"aeskey,omitempty"`
	MidSize int    `json:"mid_size,omitempty"`
}

// ImageSource 归一化后的图片来源。
type ImageSource struct {
	URL   string     // 直接可访问的 URL（可能为空）
	Media *MediaInfo // CDN 加密引用（可能为 nil）
	// HexKey 是 image_item.aeskey（hex），优先于 Media.AESKey。
	HexKey string
}

// FileSource 归一化后的文件来源。
type FileSource struct {
	Name  string
	Media *MediaInfo
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
	Ret                  int            `json:"ret"`
	ErrMsg               string         `json:"errmsg,omitempty"`
	Msgs                 []rawWeixinMsg `json:"msgs"`
	GetUpdatesBuf        string         `json:"get_updates_buf"`
	LongPollingTimeoutMs int            `json:"longpolling_timeout_ms,omitempty"`
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
