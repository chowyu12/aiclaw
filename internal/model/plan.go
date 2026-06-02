package model

import "time"

type PlanStatus string

const (
	PlanStatusActive    PlanStatus = "active"
	PlanStatusCompleted PlanStatus = "completed"
	PlanStatusFailed    PlanStatus = "failed"
)

type PlanItemStatus string

const (
	PlanItemPending   PlanItemStatus = "pending"
	PlanItemRunning   PlanItemStatus = "running"
	PlanItemCompleted PlanItemStatus = "completed"
	PlanItemBlocked   PlanItemStatus = "blocked"
	PlanItemFailed    PlanItemStatus = "failed"
	PlanItemSkipped   PlanItemStatus = "skipped"
)

type PlanRun struct {
	ID             int64      `json:"id" gorm:"primaryKey;autoIncrement"`
	UUID           string     `json:"uuid" gorm:"uniqueIndex;size:36;not null"`
	ConversationID int64      `json:"conversation_id" gorm:"index;not null"`
	MessageID      int64      `json:"message_id" gorm:"index;default:0"`
	Goal           string     `json:"goal" gorm:"type:text"`
	Status         PlanStatus `json:"status" gorm:"size:50;not null;default:active"`
	RevisionReason string     `json:"revision_reason,omitzero" gorm:"type:text"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type PlanItem struct {
	ID        int64          `json:"id" gorm:"primaryKey;autoIncrement"`
	PlanRunID int64          `json:"plan_run_id" gorm:"index;not null"`
	ItemKey   string         `json:"item_key" gorm:"size:100;not null"`
	Title     string         `json:"title" gorm:"type:text;not null"`
	Detail    string         `json:"detail,omitzero" gorm:"type:text"`
	Status    PlanItemStatus `json:"status" gorm:"size:50;not null;default:pending"`
	Reason    string         `json:"reason,omitzero" gorm:"type:text"`
	StepID    int64          `json:"step_id,omitzero" gorm:"default:0"`
	SortOrder int            `json:"sort_order" gorm:"default:0"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type PlanState struct {
	ID             int64      `json:"id"`
	UUID           string     `json:"uuid"`
	ConversationID int64      `json:"conversation_id"`
	MessageID      int64      `json:"message_id,omitzero"`
	Goal           string     `json:"goal,omitzero"`
	Status         PlanStatus `json:"status"`
	RevisionReason string     `json:"revision_reason,omitzero"`
	Items          []PlanItem `json:"items"`
	UpdatedAt      time.Time  `json:"updated_at"`
}
