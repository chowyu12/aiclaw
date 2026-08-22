package model

import "time"

// AppSetting stores small local desktop preferences in the same SQLite
// database as threads and memories. Values are intentionally opaque to keep
// the persistence layer independent from individual UI features.
type AppSetting struct {
	Key       string    `json:"key" gorm:"primaryKey;size:200"`
	Value     string    `json:"value" gorm:"type:text;not null"`
	UpdatedAt time.Time `json:"updated_at"`
}
