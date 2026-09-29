package model

import "time"

type PluginSource string

const (
	PluginSourceBuiltin PluginSource = "builtin"
	PluginSourceLocal   PluginSource = "local"
)

// Plugin is a locally installed Codex-style extension bundle. A bundle may
// contribute skills and MCP server definitions while remaining independently
// discoverable and switchable from the desktop settings UI.
type Plugin struct {
	ID   int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	UUID string `json:"uuid" gorm:"uniqueIndex;size:36;not null"`
	// PluginID is the stable identifier declared by the manifest. It is the
	// key used to upgrade bundled plugins idempotently; UUID stays local.
	PluginID    string       `json:"plugin_id,omitzero" gorm:"size:200;index"`
	Source      PluginSource `json:"source" gorm:"size:50;not null;default:local"`
	Name        string       `json:"name" gorm:"size:200;not null"`
	Description string       `json:"description" gorm:"type:text"`
	Version     string       `json:"version" gorm:"size:50"`
	InstallDir  string       `json:"install_dir" gorm:"size:1000;not null"`
	Manifest    JSON         `json:"manifest" gorm:"type:text"`
	Enabled     bool         `json:"enabled" gorm:"not null"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

// PluginConfig is one configuration value of an installed plugin.
//
// Secret values are stored the same way the application stores provider and
// search-engine credentials: encrypted at rest in the local SQLite database
// with the master key kept next to it (see internal/secrets). Secret also
// changes how the value is handled — never returned to the UI, never logged,
// never placed in a rollout or model context. See docs/design/plugin-system.md.
type PluginConfig struct {
	ID         int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	PluginUUID string `json:"plugin_uuid" gorm:"size:36;not null;uniqueIndex:idx_plugin_config_conn_key,priority:1"`
	// ConnectionID 是这个值属于哪个连接（渠道插件的一个微信号、一个企微机器人）。
	// 空串是插件本身的配置（不带渠道的插件只有这一种）。
	ConnectionID string    `json:"connection_id" gorm:"size:64;not null;default:'';uniqueIndex:idx_plugin_config_conn_key,priority:2"`
	Key          string    `json:"key" gorm:"size:200;not null;uniqueIndex:idx_plugin_config_conn_key,priority:3"`
	Value        string    `json:"-" gorm:"type:text"`
	Secret       bool      `json:"secret" gorm:"not null"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// ChannelConnection 是渠道插件的一个连接：一个微信号、一个企业微信机器人。
//
// 一个插件可以有多个连接，各有各的凭据（PluginConfig.ConnectionID）、各自一个运行
// 实例、各自的放行记录（ChannelBinding.ConnectionID）——同一个人找两个微信号，是两个
// 会话。早先一个插件只有一套凭据；升级时那一套自动变成它的第一个连接。
type ChannelConnection struct {
	ID         int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	UUID       string `json:"uuid" gorm:"uniqueIndex;size:64;not null"`
	PluginUUID string `json:"plugin_uuid" gorm:"size:36;not null;index"`
	// Name 是给人看的名字（「客服机器人」「我的小号」），会话标题里带着它。
	Name      string    `json:"name" gorm:"size:200;not null"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
