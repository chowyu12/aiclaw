package model

import "time"

// AgentRunStatus describes the lifecycle of one durable Agent execution.
type AgentRunStatus string

const (
	AgentRunRunning   AgentRunStatus = "running"
	AgentRunSucceeded AgentRunStatus = "succeeded"
	AgentRunFailed    AgentRunStatus = "failed"
	AgentRunCancelled AgentRunStatus = "cancelled"
)

// AgentRun is the durable execution boundary for an Agent turn. Messages and
// plans remain conversation-centric; the run links them into one replayable
// background job.
type AgentRun struct {
	ID               int64          `json:"id" gorm:"primaryKey;autoIncrement"`
	UUID             string         `json:"uuid" gorm:"uniqueIndex;size:36;not null"`
	AgentID          int64          `json:"agent_id" gorm:"index;not null"`
	AgentUUID        string         `json:"agent_uuid" gorm:"size:36;index;not null"`
	ConversationID   int64          `json:"conversation_id" gorm:"index;not null"`
	ConversationUUID string         `json:"conversation_uuid" gorm:"size:36;index;not null"`
	MessageID        int64          `json:"message_id,omitzero" gorm:"index;default:0"`
	UserID           string         `json:"user_id" gorm:"size:100;index;not null"`
	Input            string         `json:"input" gorm:"type:text"`
	Content          string         `json:"content,omitzero" gorm:"type:text"`
	Status           AgentRunStatus `json:"status" gorm:"size:20;not null;default:running;index"`
	Error            string         `json:"error,omitzero" gorm:"type:text"`
	TokensUsed       int            `json:"tokens_used" gorm:"default:0"`
	DurationMs       int            `json:"duration_ms" gorm:"default:0"`
	StartedAt        time.Time      `json:"started_at"`
	FinishedAt       *time.Time     `json:"finished_at,omitzero"`
}

func (AgentRun) TableName() string { return "agent_runs" }

type AgentRunListQuery struct {
	AgentUUID        string
	ConversationUUID string
	UserID           string
	Status           AgentRunStatus
	Page             int
	PageSize         int
}

// AgentRunEvent is the in-memory subscription envelope. Chunks retain the
// existing chat stream shape so old rendering code can be reused by run SSE.
type AgentRunEvent struct {
	Type      string         `json:"type"`
	RunID     string         `json:"run_id"`
	Status    AgentRunStatus `json:"status,omitempty"`
	Run       *AgentRun      `json:"run,omitempty"`
	Chunk     *StreamChunk   `json:"chunk,omitempty"`
	Error     string         `json:"error,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

const (
	AgentRunEventStarted   = "run.started"
	AgentRunEventUpdated   = "run.updated"
	AgentRunEventCompleted = "run.completed"
	AgentRunEventFailed    = "run.failed"
	AgentRunEventCancelled = "run.cancelled"
	AgentRunEventChunk     = "run.chunk"
)
