package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/chowyu12/aiclaw/internal/i18n"
	"strings"
	"time"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/store"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/tools"
)

func (s *Session) SetPersistence(db *store.Store) { s.db = db }

// Checkpoint must succeed before another model request or tool batch starts.
func (s *Session) Checkpoint() error {
	if s.db == nil {
		return nil
	}
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s.mu.Lock()
	err := s.persistenceErr
	s.mu.Unlock()
	if err != nil {
		return err
	}
	s.mu.Lock()
	for i := range s.messages {
		if s.messages[i].ID == "" {
			s.messages[i].ID = newID("message")
		}
	}
	s.mu.Unlock()
	if err = s.Save(ctx, s.db); err != nil {
		return err
	}
	if s.persistedMessages == nil {
		s.persistedMessages = map[string]bool{}
	}
	for i, m := range s.snapshotMessages() {
		if m.Role == llm.RoleSystem {
			continue
		}
		if m.ID == "" {
			m.ID = fmt.Sprintf("legacy_%d", i)
		}
		if s.persistedMessages[m.ID] {
			continue
		}
		if err = s.db.RecordMessage(ctx, s.ID, m.ID, m); err != nil {
			return err
		}
		s.persistedMessages[m.ID] = true
	}
	return nil
}

func (s *Session) historyMessages() []llm.Message {
	history, err := s.readHistoryMessages()
	if err != nil {
		return s.snapshotMessages()
	}
	return history
}
func (s *Session) readHistoryMessages() ([]llm.Message, error) {
	if s.db != nil {
		rows, err := s.db.Transcript(context.Background(), s.ID)
		if err != nil {
			return nil, err
		}
		if len(rows) > 0 {
			out := make([]llm.Message, 0, len(rows))
			for _, raw := range rows {
				var m llm.Message
				if err = json.Unmarshal(raw, &m); err != nil {
					return nil, err
				}
				out = append(out, m)
			}
			return out, nil
		}
	}
	return s.snapshotMessages(), nil
}
func reusableMessage(m llm.Message) bool {
	if m.Role == llm.RoleSystem {
		return false
	}
	return m.Shown == nil || !m.Shown.Hidden || !(strings.HasPrefix(m.Content, goalContextHeading) || strings.HasPrefix(m.Content, summaryPrefix) || strings.HasPrefix(m.Content, legacySummaryPrefix))
}

// ForkAt copies a stable, complete prefix of the visible transcript. File state is shared.
func (s *Session) ForkAt(ctx context.Context, id, itemID string, model *protocol.ModelConfig, options ...Option) (*Session, error) {
	if !s.runMu.TryLock() {
		return nil, i18n.E("请等待当前轮次结束")
	}
	defer s.runMu.Unlock()
	if err := s.Checkpoint(); err != nil {
		return nil, err
	}
	history, err := s.readHistoryMessages()
	if err != nil {
		return nil, err
	}
	end := len(history)
	if itemID != "" {
		end = -1
		for i, m := range history {
			if m.ID == itemID || historyID("user", i) == itemID || historyID("msg", i) == itemID {
				end = i + 1
				if len(m.ToolCalls) > 0 {
					for end < len(history) && history[end].Role == llm.RoleTool {
						end++
					}
				}
				break
			}
		}
		if end < 0 {
			return nil, i18n.E("要分叉的消息已不存在")
		}
	}
	history = completeHistory(history[:end])
	s.mu.Lock()
	config := s.config
	title := s.Title
	s.mu.Unlock()
	config.ParentID = ""
	config.AgentPath = ""
	config.Title = title + " (fork)"
	config.ForkSourceID = s.ID
	config.ForkItemID = itemID
	if model != nil {
		config.Model = *model
	}
	child, err := New(ctx, id, config, s.keyFor, options...)
	if err != nil {
		return nil, err
	}
	for _, m := range history {
		if reusableMessage(m) {
			child.messages = append(child.messages, m)
			if m.Role == llm.RoleUser && (m.Shown == nil || !m.Shown.Hidden) {
				child.turnCount++
			}
		}
	}
	child.messages = append(child.messages, llm.Message{Role: llm.RoleUser, Content: "This is a new chat forked from an earlier conversation. The copied conversation is historical context. No goal or queued action is active in this new chat. Follow the new user request rather than automatically continuing unfinished source work.", Shown: &llm.Shown{Hidden: true}})
	return child, nil
}

func (s *Session) ForkOrigin() (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.config.ForkSourceID, s.config.ForkItemID
}
func (s *Session) UndoChange(ctx context.Context, id string) error {
	if !s.runMu.TryLock() {
		return i18n.E("请等待当前轮次结束")
	}
	defer s.runMu.Unlock()
	if s.db == nil {
		return i18n.E("文件改动记录不可用")
	}
	s.mu.Lock()
	config := s.config
	s.mu.Unlock()
	env := &tools.Env{Workspace: config.Workdir, Home: userHome(), ProtectedPaths: config.ProtectedPaths}
	return s.db.UndoChange(ctx, s.ID, id, func(path string) error { _, _, err := env.ResolveWrite(path); return err })
}
func (s *Session) RecoverInterrupted() error {
	if s.db == nil {
		return nil
	}
	pending, err := s.db.PendingSubmissions(context.Background(), s.ID)
	if err != nil {
		return err
	}
	var execution store.Execution
	_ = s.db.State(context.Background(), s.ID, "execution", &execution)
	interrupted := execution.State == "uncertain"
	for _, r := range pending {
		interrupted = interrupted || r.State == "uncertain"
	}
	if interrupted {
		history, err := s.readHistoryMessages()
		if err != nil {
			return err
		}
		s.mu.Lock()
		fresh := s.messages[0]
		s.messages = []llm.Message{fresh}
		for _, m := range history {
			if reusableMessage(m) {
				s.messages = append(s.messages, m)
			}
		}
		s.mu.Unlock()
		s.recordStopped(recoveryMarker)
		return s.Checkpoint()
	}
	return nil
}

func (s *Session) journalMessage(m llm.Message) {
	if s.db == nil {
		return
	}
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	if s.persistedMessages == nil {
		s.persistedMessages = map[string]bool{}
	}
	if s.persistedMessages[m.ID] {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.db.RecordMessage(ctx, s.ID, m.ID, m); err != nil {
		s.mu.Lock()
		s.persistenceErr = err
		s.mu.Unlock()
	} else {
		s.persistedMessages[m.ID] = true
	}
}

// MarkDeleted prevents late turn/checkpoint saves from recreating a deleted chat.
func (s *Session) MarkDeleted() { s.saveMu.Lock(); s.deleted = true; s.saveMu.Unlock(); s.Interrupt() }
