package model

import "time"

// Conversation and Message are read-only compatibility models used solely by
// the one-time migration into Thread/Rollout. Fresh databases do not create
// these tables and the desktop runtime never writes them.
type Conversation struct {
	ID        int64  `gorm:"primaryKey;autoIncrement"`
	UUID      string `gorm:"size:36;index"`
	UserID    string `gorm:"size:100;index"`
	AgentUUID string `gorm:"size:36;index"`
	Title     string `gorm:"size:500"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Message struct {
	ID             int64  `gorm:"primaryKey;autoIncrement"`
	ConversationID int64  `gorm:"index"`
	Role           string `gorm:"size:50"`
	Content        string `gorm:"type:text"`
	CreatedAt      time.Time
}

type StepStatus string

const (
	StepSuccess StepStatus = "success"
	StepError   StepStatus = "error"
	StepPending StepStatus = "pending"
	StepRunning StepStatus = "running"
)

// ExecutionStep is an in-memory transport shape. Tool lifecycle state is
// persisted as Rollout items, not in the former execution_steps table.
type ExecutionStep struct {
	Name       string     `json:"name"`
	Input      string     `json:"input,omitzero"`
	Output     string     `json:"output,omitzero"`
	Status     StepStatus `json:"status"`
	Error      string     `json:"error,omitzero"`
	DurationMS int64      `json:"duration_ms,omitzero"`
}
