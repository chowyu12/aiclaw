package model

import "time"

// ChannelType 第三方消息渠道（企微智能机器人走 WebSocket；其余多为 Webhook）。
type ChannelType string

const (
	// ChannelWeCom 企业微信「智能机器人」：WebSocket 长连接，配置 bot_id + secret（实现见 pkg/wecomaibot，协议见官方文档）。
	ChannelWeCom    ChannelType = "wecom"
	ChannelWeChatKF ChannelType = "wechat_kf" // 微信客服
	ChannelFeishu   ChannelType = "feishu"    // 飞书
	ChannelDingTalk ChannelType = "dingtalk"  // 钉钉
	ChannelWhatsApp ChannelType = "whatsapp"  // WhatsApp（如 Meta Cloud API）
	ChannelTelegram ChannelType = "telegram"  // Telegram Bot
)

var ValidChannelTypes = map[ChannelType]struct{}{
	ChannelWeCom:    {},
	ChannelWeChatKF: {},
	ChannelFeishu:   {},
	ChannelDingTalk: {},
	ChannelWhatsApp: {},
	ChannelTelegram: {},
}

func IsValidChannelType(t ChannelType) bool {
	_, ok := ValidChannelTypes[t]
	return ok
}

// Channel 渠道配置：启用后可将下方 Webhook URL 配置到各平台；WebhookToken 用于校验打到本服务的请求。
type Channel struct {
	ID           int64       `json:"id" gorm:"primaryKey;autoIncrement"`
	UUID         string      `json:"uuid" gorm:"uniqueIndex;size:36;not null"`
	Name         string      `json:"name" gorm:"size:200;not null"`
	ChannelType  ChannelType `json:"channel_type" gorm:"size:50;not null;index"`
	Enabled      bool        `json:"enabled" gorm:"not null;default:true"`
	WebhookToken string      `json:"webhook_token,omitzero" gorm:"size:256"` // 调用本服务 Webhook 时携带的共享密钥（可选）
	Config       JSON        `json:"config,omitzero" gorm:"type:text"`       // 平台凭证与参数（JSON）
	Description  string      `json:"description,omitzero" gorm:"type:text"`
	CreatedAt    time.Time   `json:"created_at"`
	UpdatedAt    time.Time   `json:"updated_at"`
}

type CreateChannelReq struct {
	Name         string      `json:"name"`
	ChannelType  ChannelType `json:"channel_type"`
	Enabled      *bool       `json:"enabled,omitzero"`
	WebhookToken string      `json:"webhook_token,omitzero"`
	Config       JSON        `json:"config,omitzero"`
	Description  string      `json:"description,omitzero"`
}

type UpdateChannelReq struct {
	Name         *string `json:"name,omitzero"`
	Enabled      *bool   `json:"enabled,omitzero"`
	WebhookToken *string `json:"webhook_token,omitzero"`
	Config       JSON    `json:"config,omitzero"`
	Description  *string `json:"description,omitzero"`
}

// ChannelConversationItem 渠道会话列表项（用于渠道页面内嵌会话视图）。
type ChannelConversationItem struct {
	ConversationID   int64     `json:"conversation_id"`
	ConversationUUID string    `json:"conversation_uuid"`
	Title            string    `json:"title"`
	UserID           string    `json:"user_id"`
	SenderID         string    `json:"sender_id"`
	ThreadKeys       []string  `json:"thread_keys,omitzero"`
	MessageCount     int64     `json:"message_count"`
	LastUserMessage  string    `json:"last_user_message,omitzero"`
	LastReplyMessage string    `json:"last_reply_message,omitzero"`
	UpdatedAt        time.Time `json:"updated_at"`
	CreatedAt        time.Time `json:"created_at"`
}
