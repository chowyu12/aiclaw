package model

import "time"

// ChannelThread 将外部会话线程与站内 Conversation UUID 绑定（与 goclaw thread binding 类似）。
type ChannelThread struct {
	ID               int64     `json:"id" gorm:"primaryKey;autoIncrement"`
	ChannelID        int64     `json:"channel_id" gorm:"not null;uniqueIndex:idx_channel_thread"`
	ThreadKey        string    `json:"thread_key" gorm:"size:512;not null;uniqueIndex:idx_channel_thread"`
	ConversationUUID string    `json:"conversation_uuid" gorm:"size:36;not null"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}
