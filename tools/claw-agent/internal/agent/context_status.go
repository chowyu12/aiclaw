package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
)

type CompactionRecord struct {
	At     int64 `json:"at"`
	Before int   `json:"before"`
	After  int   `json:"after"`
}
type compactionHistory struct {
	Count   int                `json:"count"`
	Records []CompactionRecord `json:"records"`
}
type ContextStatus struct {
	APIProtocol     string             `json:"apiProtocol,omitempty"`
	ProtocolSource  string             `json:"protocolSource,omitempty"`
	ServiceProfile  string             `json:"serviceProfile,omitempty"`
	Model           string             `json:"model"`
	Window          int                `json:"window"`
	EstimatedTokens int                `json:"estimatedTokens"`
	LastInputTokens int                `json:"lastInputTokens"`
	HasImages       bool               `json:"hasImages"`
	Compactions     int                `json:"compactions"`
	Records         []CompactionRecord `json:"records"`
}

// Text, tool-schema and bounded audio estimate. Not provider usage or billed tokens.
func contextEstimate(messages []llm.Message, tools []llm.Tool) (int, bool) {
	used, images := 0, false
	var audioMS int64
	for _, message := range messages {
		used += approxTokens(message.Content) + 4
		images = images || len(message.Images) > 0
		// Conservative scheduling estimate; actual model billing remains authoritative.
		for _, audio := range message.Audio {
			audioMS += min(audio.DurationMS, audioBudgetMS)
		}
		for _, call := range message.ToolCalls {
			used += approxTokens(call.Name) + approxTokens(call.Arguments)
		}
	}
	used += int(min(audioMS, audioBudgetMS) * 32 / 1000)
	schema, _ := json.Marshal(tools)
	return used + approxTokens(string(schema)), images
}
func (s *Session) ContextStatus() (ContextStatus, error) {
	s.mu.Lock()
	result := ContextStatus{APIProtocol: string(s.config.Model.APIProtocol), ProtocolSource: s.config.Model.ProtocolSource, ServiceProfile: s.config.Model.ServiceProfile, Model: s.config.Model.Model, Window: s.config.Model.ContextWindow, LastInputTokens: s.lastInputTokens, Records: []CompactionRecord{}}
	messages := append([]llm.Message(nil), s.messages...)
	s.mu.Unlock()
	result.EstimatedTokens, result.HasImages = contextEstimate(messages, s.llmTools())
	if s.db != nil {
		var history compactionHistory
		if err := s.db.State(context.Background(), s.ID, "context-compactions", &history); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return result, err
		}
		result.Compactions = history.Count
		if history.Records != nil {
			result.Records = history.Records
		}
	}
	return result, nil
}
func (s *Session) recordCompaction(before, after []llm.Message) error {
	if s.db == nil {
		return nil
	}
	var history compactionHistory
	if err := s.db.State(context.Background(), s.ID, "context-compactions", &history); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	tools := s.llmTools()
	old, _ := contextEstimate(before, tools)
	next, _ := contextEstimate(after, tools)
	history.Count++
	history.Records = append(history.Records, CompactionRecord{At: time.Now().UnixMilli(), Before: old, After: next})
	if len(history.Records) > 50 {
		history.Records = history.Records[len(history.Records)-50:]
	}
	return s.db.PutState(context.Background(), s.ID, "context-compactions", history)
}
