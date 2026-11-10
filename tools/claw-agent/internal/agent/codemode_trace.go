package agent

import (
	"context"
	"time"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/store"
)

// Child evidence is UI state, not model messages. Retain both admission and completion
// so a crash never turns an operation with an unknown outcome into a successful step.
func (s *Session) recordCodeTrace(callID string, item protocol.Item) {
	if callID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	item.ToolResult = s.historyOutput(ctx, item.ToolResult)
	s.codeTraceMu.Lock()
	defer s.codeTraceMu.Unlock()
	if s.codeTraces == nil {
		s.codeTraces = map[string][]protocol.Item{}
	}
	items := s.codeTraces[callID]
	found := false
	for i := range items {
		if items[i].ID == item.ID {
			items[i] = item
			found = true
			break
		}
	}
	if !found {
		items = append(items, item)
	}
	s.codeTraces[callID] = items
	if s.db != nil {
		if err := s.db.PutState(ctx, s.ID, "code-trace:"+callID, items); err != nil {
			s.mu.Lock()
			s.persistenceErr = err
			s.mu.Unlock()
		}
	}
}

func (s *Session) saveCodeTraces(ctx context.Context, db *store.Store) error {
	if s.db == db {
		return nil
	}
	s.codeTraceMu.Lock()
	defer s.codeTraceMu.Unlock()
	for id, items := range s.codeTraces {
		if err := db.PutState(ctx, s.ID, "code-trace:"+id, items); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) withCodeTraces(items []protocol.Item) []protocol.Item {
	var result []protocol.Item
	for _, item := range items {
		result = append(result, item)
		if item.CallID == "" || item.ToolName != "exec" {
			continue
		}
		s.codeTraceMu.Lock()
		children := append([]protocol.Item(nil), s.codeTraces[item.CallID]...)
		s.codeTraceMu.Unlock()
		if children == nil && s.db != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = s.db.State(ctx, s.ID, "code-trace:"+item.CallID, &children)
			cancel()
		}
		for _, child := range children {
			child.ParentID = item.ID
			result = append(result, child)
		}
	}
	if len(result) > len(items) {
		seq := 0
		for i := range result {
			if result[i].Kind == protocol.ItemUserMessage {
				seq = 0
			}
			if result[i].Kind == protocol.ItemLLM || result[i].Kind == protocol.ItemToolCall {
				seq++
				result[i].Seq = seq
			}
		}
	}
	return result
}
