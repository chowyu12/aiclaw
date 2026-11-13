package agent

import (
	"context"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"time"
)

// Phase evidence is kept outside model history and compaction input.
func (s *Session) recordPhase(item protocol.Item) {
	s.mu.Lock()
	anchor := ""
	for i := len(s.messages) - 1; i >= 0; i-- {
		m := s.messages[i]
		if m.Role == llm.RoleAssistant && m.Sampling != nil {
			anchor = m.Sampling.ID
			break
		}
		if m.Role == llm.RoleUser && m.ID != "" {
			anchor = m.ID
			break
		}
	}
	s.mu.Unlock()
	if anchor == "" {
		return
	}
	s.codeTraceMu.Lock()
	defer s.codeTraceMu.Unlock()
	if s.phaseTraces == nil {
		s.phaseTraces = map[string][]protocol.Item{}
	}
	s.phaseTraces[anchor] = append(s.phaseTraces[anchor], item)
	if s.db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.db.PutState(ctx, s.ID, "ui-phase:"+anchor, s.phaseTraces[anchor]); err != nil {
			s.mu.Lock()
			s.persistenceErr = err
			s.mu.Unlock()
		}
	}
}
func (s *Session) withPhases(items []protocol.Item) []protocol.Item {
	var out []protocol.Item
	for _, item := range items {
		out = append(out, item)
		if item.Kind != protocol.ItemUserMessage && item.Kind != protocol.ItemLLM {
			continue
		}
		s.codeTraceMu.Lock()
		phases := append([]protocol.Item(nil), s.phaseTraces[item.ID]...)
		s.codeTraceMu.Unlock()
		if phases == nil && s.db != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = s.db.State(ctx, s.ID, "ui-phase:"+item.ID, &phases)
			cancel()
		}
		out = append(out, phases...)
	}
	return out
}
