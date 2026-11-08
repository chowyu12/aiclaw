package agent

import (
	"context"
	"encoding/json"
	"errors"
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

func TestCompactionAccountsForNewHistoryAfterReportedUsage(t *testing.T) {
	s := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	s.config.Model.ContextWindow = 10000
	s.lastInputTokens = 100
	s.messages = append(s.messages, llm.Message{Role: llm.RoleUser, Content: strings.Repeat("long history ", 6000)})
	if !s.needsCompaction() {
		t.Fatal("new history must be estimated even when older provider usage exists")
	}
	status, err := s.ContextStatus()
	if err != nil || status.LastInputTokens != 100 {
		t.Fatalf("estimate must not replace reported usage: %+v, %v", status, err)
	}
}

func TestMissingUsageFallsBackToEstimateAfterTurn(t *testing.T) {
	stream := strings.Replace(sseText("done"), "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":3,\"total_tokens\":8}}\n\n", "", 1)
	s := newTestSession(t, &fakeModel{script: []string{stream}}, protocol.ApprovalBypass)
	s.config.Model.ContextWindow = 100000
	s.RunTurn(context.Background(), "missing-usage", strings.Repeat("long history ", 6000), nil, nil, &recordingEmitter{approve: true})
	s.config.Model.ContextWindow = 10000
	if s.lastInputTokens != 0 || !s.estimateCompaction || !s.needsCompaction() {
		t.Fatalf("missing usage disabled estimation: input=%d estimate=%v", s.lastInputTokens, s.estimateCompaction)
	}
}

func TestStoredHistoryWithoutRestoringProvider(t *testing.T) {
	s := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	s.messages = append(s.messages, llm.Message{Role: llm.RoleUser, Content: "saved question"}, llm.Message{Role: llm.RoleAssistant, Content: "saved answer"})
	db := newTestStore(t)
	if err := s.Save(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	items, err := StoredHistory(context.Background(), db, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	var question, answer bool
	for _, item := range items {
		question = question || item.Text == "saved question"
		answer = answer || item.Text == "saved answer"
	}
	if !question || !answer {
		t.Fatalf("stored conversation missing from history: %+v", items)
	}
	if _, err := StoredHistory(context.Background(), db, "missing"); err == nil {
		t.Fatal("missing session must fail")
	}
}

func TestRecoverUnavailableProviderWithAnotherModel(t *testing.T) {
	ctx := context.Background()
	s := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	s.config.Model.ProviderID = 1
	s.messages = append(s.messages, llm.Message{Role: llm.RoleUser, Content: "keep this history"})
	db := newTestStore(t)
	if err := s.Save(ctx, db); err != nil {
		t.Fatal(err)
	}
	resolve := func(model *protocol.ModelConfig) (string, error) {
		if model.ProviderID != 2 {
			return "", errors.New("provider unavailable")
		}
		model.BaseURL = s.config.Model.BaseURL
		return "test", nil
	}
	if _, err := Load(ctx, db, s.ID, resolve, nil); err == nil {
		t.Fatal("unavailable provider unexpectedly loaded")
	}
	recovery := &protocol.SessionRefresh{RecoveryModel: &protocol.ModelConfig{ProviderID: 2, Model: "replacement"}}
	loaded, err := Load(ctx, db, s.ID, resolve, recovery)
	if err != nil {
		t.Fatal(err)
	}
	defer loaded.Close()
	if loaded.Model().ProviderID != 2 || loaded.Model().Model != "replacement" {
		t.Fatal("replacement model not selected")
	}
	items := loaded.History()
	found := false
	for _, item := range items {
		found = found || item.Text == "keep this history"
	}
	if !found {
		t.Fatalf("history lost during recovery: %+v", items)
	}
}
