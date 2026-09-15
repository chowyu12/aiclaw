package model

import "time"

// ChannelBinding maps one external conversation to a local thread.
//
// A binding is created unauthorized: an inbound message from an unknown
// external conversation records that someone tried to reach the agent, and
// waits for the user to allow it. Receiving a message must never be enough to
// start a turn, because a turn can run tools.
type ChannelBinding struct {
	ID         int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	PluginUUID string `json:"plugin_uuid" gorm:"size:36;not null;uniqueIndex:idx_channel_binding_key"`
	ChannelID  string `json:"channel_id" gorm:"size:100;not null;uniqueIndex:idx_channel_binding_key"`
	// ExternalKey identifies the remote conversation: a group chat id where
	// there is one, otherwise the sender.
	ExternalKey string `json:"external_key" gorm:"size:200;not null;uniqueIndex:idx_channel_binding_key"`
	DisplayName string `json:"display_name" gorm:"size:200"`
	ThreadUUID  string `json:"thread_uuid" gorm:"size:36;index"`
	ProviderID  int64  `json:"provider_id"`
	ModelName   string `json:"model_name" gorm:"size:200"`
	Allowed     bool   `json:"allowed" gorm:"not null"`
	// AllowedTools are tool names this binding may use beyond the read-only
	// default set. Inbound messages are untrusted, so acting tools are opt-in
	// per binding rather than inherited from the desktop session.
	AllowedTools JSON      `json:"allowed_tools,omitzero" gorm:"type:text"`
	LastMessage  time.Time `json:"last_message,omitzero"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
