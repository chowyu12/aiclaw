package model

import "time"

// Plugin is a locally installed Codex-style extension bundle. A bundle may
// contribute skills and MCP server definitions while remaining independently
// discoverable and switchable from the desktop settings UI.
type Plugin struct {
	ID          int64     `json:"id" gorm:"primaryKey;autoIncrement"`
	UUID        string    `json:"uuid" gorm:"uniqueIndex;size:36;not null"`
	Name        string    `json:"name" gorm:"size:200;not null"`
	Description string    `json:"description" gorm:"type:text"`
	Version     string    `json:"version" gorm:"size:50"`
	InstallDir  string    `json:"install_dir" gorm:"size:1000;not null"`
	Manifest    JSON      `json:"manifest" gorm:"type:text"`
	Enabled     bool      `json:"enabled" gorm:"not null"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
