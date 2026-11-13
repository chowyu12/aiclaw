package agent

import (
	"context"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/store"
	"time"
)

// Timing and failed-sampling UI evidence stay outside model history and compaction input.
func (s *Session) recordPhase(item protocol.Item) {
	s.mu.Lock()
	anchor := ""
	for i := len(s.messages) - 1; i >= 0; i-- {
		m := s.messages[i]
		if m.Role == llm.RoleTool && m.Sampling != nil {
			anchor = m.Sampling.ID
			break
		}
		if m.Role == llm.RoleAssistant && m.Sampling != nil {
			anchor = m.Sampling.ID
			if m.Content != "" && m.ID != "" {
				anchor = m.ID
			}
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
func (s *Session) savePhaseTraces(ctx context.Context, db *store.Store) error {
	if s.db == db {
		return nil
	}
	s.codeTraceMu.Lock()
	defer s.codeTraceMu.Unlock()
	for anchor, items := range s.phaseTraces {
		if err := db.PutState(ctx, s.ID, "ui-phase:"+anchor, items); err != nil {
			return err
		}
	}
	return nil
}
func (s *Session) withPhases(items []protocol.Item) []protocol.Item {
	// Accepted history can contain an attempt already recorded as interrupted UI evidence.
	seen := map[string]bool{}
	for _, item := range items {
		seen[item.ID] = true
	}
	// A parent tool's traces belong after all its nested child evidence.
	anchors := map[int][]string{}
	for i, item := range items {
		end := i
		for j := i + 1; j < len(items) && items[j].ParentID == item.ID; j++ {
			end = j
		}
		anchors[end] = append(anchors[end], item.ID)
	}
	var out []protocol.Item
	for i, item := range items {
		out = append(out, item)
		for _, anchor := range anchors[i] {
			s.codeTraceMu.Lock()
			phases := append([]protocol.Item(nil), s.phaseTraces[anchor]...)
			s.codeTraceMu.Unlock()
			if phases == nil && s.db != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				_ = s.db.State(ctx, s.ID, "ui-phase:"+anchor, &phases)
				cancel()
			}
			for _, phase := range phases {
				if !seen[phase.ID] {
					out = append(out, phase)
					seen[phase.ID] = true
				}
			}
		}
	}
	return out
}
