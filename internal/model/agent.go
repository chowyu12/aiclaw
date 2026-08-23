package model

// Agent is retained only as a read model for the one-time migration from the
// former agents/conversations schema into native Threads. New databases do not
// create or write the agents table, and the desktop runtime has no Agent CRUD.
type Agent struct {
	ID             int64  `gorm:"primaryKey;autoIncrement"`
	UUID           string `gorm:"size:36"`
	ProviderID     int64
	ModelName      string
	SearchEngineID int64
	WorkingDir     string
}
