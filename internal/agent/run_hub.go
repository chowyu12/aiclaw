package agent

import (
	"context"
	"sync"

	"github.com/chowyu12/aiclaw/internal/model"
)

const (
	agentRunReplayLimit      = 128
	agentRunSubscriberBuffer = 256
)

// agentRunHub decouples an Agent execution from any one SSE connection. It is
// deliberately best-effort: persistent state remains the source of truth once
// the bounded live-event replay has expired.
type agentRunHub struct {
	mu     sync.Mutex
	active map[string]*activeAgentRun
	nextID uint64
}

type activeAgentRun struct {
	cancel      context.CancelFunc
	replay      []model.AgentRunEvent
	subscribers map[uint64]chan model.AgentRunEvent
}

func newAgentRunHub() *agentRunHub {
	return &agentRunHub{active: make(map[string]*activeAgentRun)}
}

func (h *agentRunHub) start(runID string, cancel context.CancelFunc) {
	if h == nil || runID == "" {
		return
	}
	h.mu.Lock()
	if _, exists := h.active[runID]; !exists {
		h.active[runID] = &activeAgentRun{cancel: cancel, subscribers: make(map[uint64]chan model.AgentRunEvent)}
	}
	h.mu.Unlock()
}

func (h *agentRunHub) publish(runID string, event model.AgentRunEvent) {
	if h == nil || runID == "" {
		return
	}
	event.RunID = runID
	h.mu.Lock()
	defer h.mu.Unlock()
	run := h.active[runID]
	if run == nil {
		return
	}
	run.replay = append(run.replay, event)
	if len(run.replay) > agentRunReplayLimit {
		run.replay = append([]model.AgentRunEvent(nil), run.replay[len(run.replay)-agentRunReplayLimit:]...)
	}
	for _, subscriber := range run.subscribers {
		select {
		case subscriber <- event:
		default:
			// A slow or disconnected client must never stall the Agent.
		}
	}
}

func (h *agentRunHub) subscribe(runID string) (<-chan model.AgentRunEvent, func(), bool) {
	if h == nil {
		return nil, func() {}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	run := h.active[runID]
	if run == nil {
		return nil, func() {}, false
	}
	h.nextID++
	id := h.nextID
	ch := make(chan model.AgentRunEvent, agentRunSubscriberBuffer)
	for _, event := range run.replay {
		ch <- event
	}
	run.subscribers[id] = ch
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if current := h.active[runID]; current != nil {
				if subscriber, ok := current.subscribers[id]; ok {
					delete(current.subscribers, id)
					close(subscriber)
				}
			}
		})
	}, true
}

func (h *agentRunHub) cancel(runID string) bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	run := h.active[runID]
	h.mu.Unlock()
	if run == nil || run.cancel == nil {
		return false
	}
	run.cancel()
	return true
}

func (h *agentRunHub) complete(runID string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	run := h.active[runID]
	delete(h.active, runID)
	h.mu.Unlock()
	if run == nil {
		return
	}
	for _, subscriber := range run.subscribers {
		close(subscriber)
	}
}
