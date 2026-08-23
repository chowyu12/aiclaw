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

type PlanSource string

const (
	PlanSourceModel   PlanSource = "model"
	PlanSourceHarness PlanSource = "harness"
)

type PlanItem struct {
	ItemKey   string         `json:"item_key" gorm:"size:100;not null"`
	Title     string         `json:"title" gorm:"type:text;not null"`
	Detail    string         `json:"detail,omitzero" gorm:"type:text"`
	Status    PlanItemStatus `json:"status" gorm:"size:50;not null;default:pending"`
	Reason    string         `json:"reason,omitzero" gorm:"type:text"`
	SortOrder int            `json:"sort_order" gorm:"default:0"`
}

type PlanState struct {
	ID             int64      `json:"id"`
	UUID           string     `json:"uuid"`
	ConversationID int64      `json:"conversation_id"`
	MessageID      int64      `json:"message_id,omitzero"`
	Goal           string     `json:"goal,omitzero"`
	Source         PlanSource `json:"source,omitzero"`
	Status         PlanStatus `json:"status"`
	RevisionReason string     `json:"revision_reason,omitzero"`
	Items          []PlanItem `json:"items"`
	UpdatedAt      time.Time  `json:"updated_at"`
}
