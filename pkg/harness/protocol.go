package harness

import "time"

// ProtocolVersion identifies the wire format emitted by this package.
const ProtocolVersion = "harness.v1"

// Layer identifies the harness responsibility that produced an event.
type Layer string

const (
	LayerLifecycle   Layer = "lifecycle"
	LayerContext     Layer = "context"
	LayerModel       Layer = "model"
	LayerAction      Layer = "action"
	LayerControl     Layer = "control"
	LayerPersistence Layer = "persistence"
)

// EventType is the stable event vocabulary exposed by the harness.
type EventType string

const (
	EventRunStarted     EventType = "run.started"
	EventRunCompleted   EventType = "run.completed"
	EventRunFailed      EventType = "run.failed"
	EventTurnStarted    EventType = "turn.started"
	EventTurnCompleted  EventType = "turn.completed"
	EventContextBuilt   EventType = "context.built"
	EventModelStarted   EventType = "model.started"
	EventModelDelta     EventType = "model.delta"
	EventModelCompleted EventType = "model.completed"
	EventModelFailed    EventType = "model.failed"
	EventActionStarted  EventType = "action.started"
	EventActionFinished EventType = "action.finished"
	EventControlUpdate  EventType = "control.update"
	EventPersisted      EventType = "persistence.persisted"
)

// Status is shared by run, turn, and item lifecycle events.
type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
	StatusSkipped   Status = "skipped"
)

// Event is the protocol boundary between the harness and clients.
// Payloads are intentionally JSON-shaped so clients can evolve without importing
// internal agent structs.
type Event struct {
	Version      string         `json:"version"`
	Type         EventType      `json:"type"`
	Layer        Layer          `json:"layer"`
	RunID        string         `json:"run_id,omitempty"`
	TurnID       string         `json:"turn_id,omitempty"`
	ItemID       string         `json:"item_id,omitempty"`
	ParentItemID string         `json:"parent_item_id,omitempty"`
	Name         string         `json:"name,omitempty"`
	Status       Status         `json:"status,omitempty"`
	Delta        string         `json:"delta,omitempty"`
	Input        any            `json:"input,omitempty"`
	Output       any            `json:"output,omitempty"`
	Error        string         `json:"error,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
}

// Sink consumes harness events. Implementations can bridge to SSE, JSON-RPC,
// tests, or durable traces.
type Sink interface {
	Emit(Event) error
}

// EventFunc adapts a function into a Sink.
type EventFunc func(Event) error

func (f EventFunc) Emit(e Event) error {
	if f == nil {
		return nil
	}
	return f(e)
}

// NoopSink discards all events.
type NoopSink struct{}

func (NoopSink) Emit(Event) error { return nil }
