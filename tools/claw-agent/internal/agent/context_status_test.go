package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

func TestContextStatusSeparatesEstimatesFromProviderUsage(t *testing.T) {
	s := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	s.config.Model.ContextWindow = 100000
	s.lastInputTokens = 1234
	s.messages = []llm.Message{{Content: "Hello 中文", Images: [][]byte{make([]byte, 100000)}}}
	result, err := s.ContextStatus()
	if err != nil || result.Window != 100000 || result.LastInputTokens != 1234 || !result.HasImages || result.EstimatedTokens <= 0 {
		t.Fatalf("bad status: %+v %v", result, err)
	}
	s.messages[0].Images = nil
	plain, _ := s.ContextStatus()
	if plain.EstimatedTokens != result.EstimatedTokens {
		t.Fatal("image bytes counted as text tokens")
	}
	s.config.Model.ContextWindow = 0
	s.lastInputTokens = 0
	unknown, _ := s.ContextStatus()
	if unknown.Window != 0 || unknown.LastInputTokens != 0 {
		t.Fatal("unknown values inferred")
	}
}

func TestCompactionMetadataOnlyRecordsSuccessfulCompaction(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		count          int
		failed         bool
	}{
		{"success", sseText("task summary"), 1, false},
		{"empty summary", sseText(""), 0, true},
		{"provider failure", "!500 unavailable", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestSession(t, &fakeModel{script: []string{tc.response}}, protocol.ApprovalBypass)
			persistTestSession(t, s)
			s.appendMessage(llm.Message{Role: llm.RoleUser, Content: "Keep this task"})
			s.appendMessage(llm.Message{Role: llm.RoleAssistant, Content: strings.Repeat("completed work ", 100)})
			err := s.compact(context.Background(), "turn", &recordingEmitter{})
			if (err != nil) != tc.failed {
				t.Fatalf("compaction error: %v", err)
			}
			status, err := s.ContextStatus()
			if err != nil || status.Compactions != tc.count || len(status.Records) != tc.count {
				t.Fatalf("unexpected metadata: %+v %v", status, err)
			}
		})
	}
}
func TestCompactionRecordsPersistAndAreBounded(t *testing.T) {
	s := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	db := persistTestSession(t, s)
	before := []llm.Message{{Content: strings.Repeat("long history ", 100)}}
	after := []llm.Message{{Content: "summary"}}
	for i := 0; i < 55; i++ {
		if err := s.recordCompaction(before, after); err != nil {
			t.Fatal(err)
		}
	}
	// A new session object reads persisted metadata, not an in-memory counter.
	restored := &Session{ID: s.ID, db: db, registry: s.registry, config: s.config, messages: after}
	result, err := restored.ContextStatus()
	if err != nil || result.Compactions != 55 || len(result.Records) != 50 {
		t.Fatalf("records: %+v %v", result, err)
	}
	if result.Records[0].Before <= result.Records[0].After || result.Records[0].At <= 0 {
		t.Fatal("invalid recorded estimate")
	}
}
