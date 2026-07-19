package model

import "time"

// MemoryScope defines the visibility boundary of a durable memory item.
// User memories apply across an owner's agents, while agent_user memories
// apply only when that owner talks to the selected agent.
type MemoryScope string

const (
	MemoryScopeUser      MemoryScope = "user"
	MemoryScopeAgentUser MemoryScope = "agent_user"
)

type MemoryKind string

const (
	MemoryKindPreference MemoryKind = "preference"
	MemoryKindProfile    MemoryKind = "profile"
	MemoryKindFact       MemoryKind = "fact"
	MemoryKindDecision   MemoryKind = "decision"
	MemoryKindProcedure  MemoryKind = "procedure"
	MemoryKindConstraint MemoryKind = "constraint"
)

type MemoryStatus string

const (
	MemoryStatusActive     MemoryStatus = "active"
	MemoryStatusCandidate  MemoryStatus = "candidate"
	MemoryStatusSuperseded MemoryStatus = "superseded"
	MemoryStatusDismissed  MemoryStatus = "dismissed"
	MemoryStatusDeleted    MemoryStatus = "deleted"
)

type MemorySensitivity string

const (
	MemorySensitivityNormal    MemorySensitivity = "normal"
	MemorySensitivitySensitive MemorySensitivity = "sensitive"
)

type MemoryRevisionAction string

const (
	MemoryRevisionCreated   MemoryRevisionAction = "created"
	MemoryRevisionUpdated   MemoryRevisionAction = "updated"
	MemoryRevisionApproved  MemoryRevisionAction = "approved"
	MemoryRevisionDismissed MemoryRevisionAction = "dismissed"
	MemoryRevisionForgotten MemoryRevisionAction = "forgotten"
	MemoryRevisionImported  MemoryRevisionAction = "imported"
)

type MemoryEvidenceRelation string

const (
	MemoryEvidenceSource MemoryEvidenceRelation = "source"
	MemoryEvidenceUsed   MemoryEvidenceRelation = "used"
)

// MemoryItem is the source of truth for cross-session memory. Content remains
// structured and reviewable; only a compact, relevant subset is injected into
// an Agent prompt for a given turn.
type MemoryItem struct {
	ID          int64             `json:"id" gorm:"primaryKey;autoIncrement"`
	UUID        string            `json:"uuid" gorm:"uniqueIndex;size:36;not null"`
	UserID      string            `json:"user_id" gorm:"size:100;index;not null"`
	AgentUUID   string            `json:"agent_uuid,omitzero" gorm:"size:36;index"`
	Scope       MemoryScope       `json:"scope" gorm:"size:30;index;not null"`
	Kind        MemoryKind        `json:"kind" gorm:"size:30;index;not null"`
	MemoryKey   string            `json:"memory_key" gorm:"size:200;index;not null"`
	Content     string            `json:"content" gorm:"type:text;not null"`
	Summary     string            `json:"summary" gorm:"type:text;not null"`
	Importance  int               `json:"importance" gorm:"default:50;index"`
	Confidence  float64           `json:"confidence" gorm:"default:0.8"`
	Sensitivity MemorySensitivity `json:"sensitivity" gorm:"size:30;not null;default:normal"`
	Status      MemoryStatus      `json:"status" gorm:"size:30;index;not null;default:candidate"`
	Pinned      bool              `json:"pinned" gorm:"default:false;index"`
	ExpiresAt   *time.Time        `json:"expires_at,omitzero;index"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

func (MemoryItem) TableName() string { return "memory_items" }

// MemoryRevision keeps an immutable record of every visible state change.
type MemoryRevision struct {
	ID        int64                `json:"id" gorm:"primaryKey;autoIncrement"`
	MemoryID  int64                `json:"memory_id" gorm:"index;not null"`
	Action    MemoryRevisionAction `json:"action" gorm:"size:30;not null"`
	Content   string               `json:"content" gorm:"type:text;not null"`
	Summary   string               `json:"summary" gorm:"type:text;not null"`
	Status    MemoryStatus         `json:"status" gorm:"size:30;not null"`
	Actor     string               `json:"actor" gorm:"size:100;not null"`
	CreatedAt time.Time            `json:"created_at"`
}

func (MemoryRevision) TableName() string { return "memory_revisions" }

// MemoryEvidence links a memory to the message/run that supplied it or used
// it. A MessageID of zero denotes an in-flight run and is backfilled once the
// assistant response is persisted.
type MemoryEvidence struct {
	ID             int64                  `json:"id" gorm:"primaryKey;autoIncrement"`
	MemoryID       int64                  `json:"memory_id" gorm:"index;not null"`
	Relation       MemoryEvidenceRelation `json:"relation" gorm:"size:20;not null"`
	ConversationID int64                  `json:"conversation_id,omitzero" gorm:"index"`
	MessageID      int64                  `json:"message_id,omitzero" gorm:"index"`
	RunUUID        string                 `json:"run_uuid,omitzero" gorm:"size:36;index"`
	Source         string                 `json:"source" gorm:"size:30;not null"`
	CreatedAt      time.Time              `json:"created_at"`
}

func (MemoryEvidence) TableName() string { return "memory_evidence" }

type MemoryListQuery struct {
	UserID      string
	AgentUUID   string
	Scope       MemoryScope
	Status      MemoryStatus
	Kind        MemoryKind
	Keyword     string
	Page        int
	PageSize    int
	IncludeAll  bool
	OnlyPending bool
}

type MemoryRetrieveQuery struct {
	UserID     string
	AgentUUID  string
	Query      string
	Limit      int
	TokenHint  int
	PinnedOnly bool
}

// MemoryContext is the compact, explainable set of memories selected for an
// Agent turn. It is returned to clients and persisted as usage evidence.
type MemoryContext struct {
	Items       []MemoryItem `json:"items"`
	Prompt      string       `json:"-"`
	TokenHint   int          `json:"token_hint"`
	RetrievedAt time.Time    `json:"retrieved_at"`
}

type CreateMemoryRequest struct {
	AgentUUID   string            `json:"agent_uuid,omitzero"`
	Scope       MemoryScope       `json:"scope"`
	Kind        MemoryKind        `json:"kind"`
	MemoryKey   string            `json:"memory_key"`
	Content     string            `json:"content"`
	Summary     string            `json:"summary,omitzero"`
	Importance  int               `json:"importance,omitzero"`
	Confidence  float64           `json:"confidence,omitzero"`
	Sensitivity MemorySensitivity `json:"sensitivity,omitzero"`
	Status      MemoryStatus      `json:"status,omitzero"`
	Pinned      bool              `json:"pinned"`
	ExpiresAt   *time.Time        `json:"expires_at,omitzero"`
}

type UpdateMemoryRequest struct {
	Content     *string            `json:"content,omitzero"`
	Summary     *string            `json:"summary,omitzero"`
	Importance  *int               `json:"importance,omitzero"`
	Confidence  *float64           `json:"confidence,omitzero"`
	Sensitivity *MemorySensitivity `json:"sensitivity,omitzero"`
	Status      *MemoryStatus      `json:"status,omitzero"`
	Pinned      *bool              `json:"pinned,omitzero"`
	ExpiresAt   *time.Time         `json:"expires_at,omitzero"`
}
