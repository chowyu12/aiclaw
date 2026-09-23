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
	ID         int64     `json:"id" gorm:"primaryKey;autoIncrement"`
	PluginUUID string    `json:"plugin_uuid" gorm:"size:36;not null;uniqueIndex:idx_plugin_config_key"`
	Key        string    `json:"key" gorm:"size:200;not null;uniqueIndex:idx_plugin_config_key"`
	Value      string    `json:"-" gorm:"type:text"`
	Secret     bool      `json:"secret" gorm:"not null"`
	UpdatedAt  time.Time `json:"updated_at"`
}
