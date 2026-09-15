// Package connector holds what the bundled inbound connectors share: turning a
// turn's protocol events into one user-visible reply, and keeping concurrent
// inbound messages from trampling each other.
package connector

import (
	"strings"
	"sync"

	"github.com/chowyu12/aiclaw/internal/protocol"
)

// Reply accumulates the visible outcome of one turn.
//
// A turn emits incremental assistant deltas and then a final message. A
// connector that can stream sends the deltas as they arrive; one that cannot
// sends Text() once. Either way the failure path is explicit: a turn that
// failed must not be reported to the sender as an empty success.
type Reply struct {
	mu      sync.Mutex
	deltas  strings.Builder
	final   string
	failure string
	onDelta func(string) error
	// deltaErr keeps the first streaming error; streaming is best effort and
	// must not abandon the final reply.
	deltaErr error
}

// NewReply collects a turn's output. onDelta may be nil for a connector that
// can only send a complete message.
func NewReply(onDelta func(string) error) *Reply {
	return &Reply{onDelta: onDelta}
}

// Observe implements the event sink passed to the gateway.
func (r *Reply) Observe(event protocol.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch event.Kind {
	case protocol.EventAssistantDelta:
		if event.Delta == "" {
			return nil
		}
		r.deltas.WriteString(event.Delta)
		if r.onDelta != nil {
			if err := r.onDelta(event.Delta); err != nil && r.deltaErr == nil {
				r.deltaErr = err
			}
		}
	case protocol.EventTurnCompleted:
		if event.Item != nil {
			r.final = completedContent(event)
		}
		if r.final == "" {
			r.final = event.Output
		}
	case protocol.EventTurnFailed:
		r.failure = event.Error
	}
	return nil
}

// Text is what should be sent to the conversation.
func (r *Reply) Text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.TrimSpace(r.final) != "" {
		return r.final
	}
	return strings.TrimSpace(r.deltas.String())
}

// Failed reports whether the turn ended in failure.
func (r *Reply) Failed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.failure != ""
}

// StreamError is the first error a streaming sink returned, if any.
func (r *Reply) StreamError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.deltaErr
}

func completedContent(event protocol.Event) string {
	if event.Item == nil {
		return ""
	}
	var payload struct {
		Content string `json:"content"`
	}
	if unmarshal(event.Item.Payload, &payload) != nil {
		return ""
	}
	return payload.Content
}
