package model

import "time"

// Thread is the durable, Codex-style unit of agent state. A thread owns its
// configuration snapshot and an append-only rollout; Message is retained only
// as a legacy projection during the migration from the web implementation.
type Thread struct {
	ID                     int64        `json:"id" gorm:"primaryKey;autoIncrement"`
	UUID                   string       `json:"uuid" gorm:"uniqueIndex;size:36;not null"`
	SessionID              string       `json:"session_id" gorm:"size:36;index;not null"`
	ParentUUID             string       `json:"parent_uuid,omitzero" gorm:"size:36;index"`
	LegacyConversationUUID string       `json:"legacy_conversation_uuid,omitzero" gorm:"index;size:36"`
	UserID                 string       `json:"user_id" gorm:"size:100;index;not null"`
	ProjectUUID            string       `json:"project_uuid,omitzero" gorm:"size:36;index"`
	AgentUUID              string       `json:"agent_uuid" gorm:"size:36;index;not null"`
	ProviderID             int64        `json:"provider_id" gorm:"index;not null"`
	ModelName              string       `json:"model_name" gorm:"size:200;not null"`
	SearchEngineID         int64        `json:"search_engine_id,omitzero" gorm:"index"`
	WorkingDir             string       `json:"working_dir" gorm:"size:1000"`
	Title                  string       `json:"title" gorm:"size:500"`
	Status                 ThreadStatus `json:"status" gorm:"size:20;not null;default:active;index"`
	ConfigSnapshot         JSON         `json:"config_snapshot,omitzero" gorm:"type:text"`
	CreatedAt              time.Time    `json:"created_at"`
	UpdatedAt              time.Time    `json:"updated_at"`
	ArchivedAt             *time.Time   `json:"archived_at,omitzero"`
}

// Project groups local threads in the desktop sidebar. It deliberately has no
// provider/model settings: every conversation chooses its own model snapshot.
type Project struct {
	ID        int64     `json:"id" gorm:"primaryKey;autoIncrement"`
	UUID      string    `json:"uuid" gorm:"uniqueIndex;size:36;not null"`
	UserID    string    `json:"user_id" gorm:"size:100;index;not null"`
	Name      string    `json:"name" gorm:"size:200;not null"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ThreadStatus string

const (
	ThreadStatusActive   ThreadStatus = "active"
	ThreadStatusArchived ThreadStatus = "archived"
)

// RolloutItem is an immutable protocol event in a thread. Ordinal is assigned
// by the store, so recovery can replay model-visible state in exact order.
type RolloutItem struct {
	ID           int64       `json:"id" gorm:"primaryKey;autoIncrement"`
	ThreadID     int64       `json:"thread_id" gorm:"uniqueIndex:idx_rollout_thread_ordinal,priority:1;index;not null"`
	Ordinal      int64       `json:"ordinal" gorm:"uniqueIndex:idx_rollout_thread_ordinal,priority:2;not null"`
	TurnID       string      `json:"turn_id,omitzero" gorm:"size:36;index"`
	Kind         RolloutKind `json:"kind" gorm:"size:80;index;not null"`
	ModelVisible bool        `json:"model_visible" gorm:"not null;default:false"`
	Payload      JSON        `json:"payload" gorm:"type:text;not null"`
	CreatedAt    time.Time   `json:"created_at"`
}

type RolloutKind string

const (
	RolloutThreadStarted   RolloutKind = "thread.started"
	RolloutTurnStarted     RolloutKind = "turn.started"
	RolloutUserMessage     RolloutKind = "input.user_message"
	RolloutAssistantDelta  RolloutKind = "output.assistant_delta"
	RolloutAssistantFinal  RolloutKind = "output.assistant_final"
	RolloutToolRequested   RolloutKind = "tool.requested"
	RolloutToolCompleted   RolloutKind = "tool.completed"
	RolloutPlanUpdated     RolloutKind = "plan.updated"
	RolloutTurnCompleted   RolloutKind = "turn.completed"
	RolloutTurnFailed      RolloutKind = "turn.failed"
	RolloutContextSnapshot RolloutKind = "context.snapshot"
)
