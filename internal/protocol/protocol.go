// Package protocol defines the transport-neutral local desktop runtime
// contract. Wails calls this boundary in-process without reviving an HTTP API.
package protocol

import "github.com/chowyu12/aiclaw/internal/model"

type CommandKind string

const (
	CommandCreateThread  CommandKind = "thread.create"
	CommandResumeThread  CommandKind = "thread.resume"
	CommandStartTurn     CommandKind = "turn.start"
	CommandCancelTurn    CommandKind = "turn.cancel"
	CommandArchiveThread CommandKind = "thread.archive"
)

type Command struct {
	Kind           CommandKind `json:"kind"`
	ThreadID       string      `json:"thread_id,omitzero"`
	UserID         string      `json:"user_id,omitzero"`
	ProjectUUID    string      `json:"project_uuid,omitzero"`
	ProviderID     int64       `json:"provider_id,omitzero"`
	ModelName      string      `json:"model_name,omitzero"`
	SearchEngineID int64       `json:"search_engine_id,omitzero"`
	Input          string      `json:"input,omitzero"`
	Attachments    []string    `json:"attachments,omitzero"`
	WorkingDir     string      `json:"working_dir,omitzero"`
}

type EventKind string

const (
	EventThreadCreated  EventKind = "thread.created"
	EventTurnStarted    EventKind = "turn.started"
	EventAssistantDelta EventKind = "assistant.delta"
	EventToolLifecycle  EventKind = "tool.lifecycle"
	EventPlanUpdated    EventKind = "plan.updated"
	EventTurnCompleted  EventKind = "turn.completed"
	EventTurnFailed     EventKind = "turn.failed"
)

type Event struct {
	Kind       EventKind          `json:"kind"`
	ThreadID   string             `json:"thread_id"`
	TurnID     string             `json:"turn_id,omitzero"`
	Item       *model.RolloutItem `json:"item,omitzero"`
	CallID     string             `json:"call_id,omitzero"`
	Name       string             `json:"name,omitzero"`
	Status     string             `json:"status,omitzero"`
	Message    string             `json:"message,omitzero"`
	Input      string             `json:"input,omitzero"`
	Output     string             `json:"output,omitzero"`
	Delta      string             `json:"delta,omitzero"`
	Error      string             `json:"error,omitzero"`
	StartedAt  int64              `json:"started_at,omitzero"`
	DurationMS int64              `json:"duration_ms,omitzero"`
}
