package agent

import (
	"context"
	"encoding/json"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"strings"
	"testing"
)

func TestContextUsageSurvivesResumeAndFork(t *testing.T) {
	s := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	db := newTestStore(t)
	s.SetPersistence(db)
	s.config.Model.ContextWindow = 100000
	s.lastInputTokens = 95000
	if err := s.Save(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	restored, err := Load(context.Background(), db, s.ID, StaticKey("test"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	before, _ := s.ContextStatus()
	after, _ := restored.ContextStatus()
	t.Logf("before save input=%d compact=%v; after cold resume input=%d compact=%v", before.LastInputTokens, s.needsCompaction(), after.LastInputTokens, restored.needsCompaction())
	if after.LastInputTokens != before.LastInputTokens {
		t.Errorf("cold resume lost provider usage")
	}
	child, err := s.ForkAt(context.Background(), "review-fork", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	forked, _ := child.ContextStatus()
	if forked.LastInputTokens != 0 || child.needsCompaction() {
		t.Fatal("fork reused provider usage for a different history")
	}
}

func TestLegacyContextAndForkUseEstimatesWithoutInventingProviderUsage(t *testing.T) {
	s := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	s.config.Model.ContextWindow = 10000
	s.messages = append(s.messages, llm.Message{Role: llm.RoleUser, Content: strings.Repeat("long history ", 6000)})
	db := newTestStore(t)
	s.SetPersistence(db)
	if err := s.Save(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	record, err := db.Load(context.Background(), s.ID)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]json.RawMessage
	if err = json.Unmarshal(record.Config, &config); err != nil {
		t.Fatal(err)
	}
	delete(config, "contextUsage")
	record.Config, _ = json.Marshal(config)
	if err = db.Save(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	restored, err := Load(context.Background(), db, s.ID, StaticKey("test"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if restored.lastInputTokens != 0 || !restored.needsCompaction() {
		t.Fatal("legacy history should use a local estimate, not fabricated provider input")
	}
	child, err := s.ForkAt(context.Background(), "long-fork", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	if child.lastInputTokens != 0 || !child.needsCompaction() {
		t.Fatal("large fork did not evaluate copied context")
	}
}
