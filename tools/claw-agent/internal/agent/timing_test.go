package agent

import (
	"context"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"testing"
)

func TestSamplingEvidenceAndPhasesRestoreWithoutProvider(t *testing.T) {
	s := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	db := newTestStore(t)
	s.messages = append(s.messages, llm.Message{ID: "u", Role: llm.RoleUser, Content: "question"}, llm.Message{ID: "a", Role: llm.RoleAssistant, Content: "answer", Sampling: &protocol.Item{ID: "sample", Kind: protocol.ItemLLM, Text: "visible summary", StartedAt: 123, DurationMS: 456, TTFTMS: 78}})
	if err := s.Save(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	s.db = db
	if _, err := s.approvalWaiter("t", &recordingEmitter{approve: true})(context.Background(), protocol.ApprovalRequestParams{}); err != nil {
		t.Fatal(err)
	}
	s.recordPhase(protocol.Item{ID: "save", Kind: protocol.ItemNotice, Phase: "save", StartedAt: 600, DurationMS: 10})
	items, err := StoredHistory(context.Background(), db, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	var sampling, phase, approval bool
	for _, item := range items {
		if item.Phase == "approval" {
			approval = true
		}
		if item.ID == "sample" {
			sampling = item.Text == "visible summary" && item.DurationMS == 456 && item.TTFTMS == 78
		}
		if item.ID == "save" {
			phase = item.Phase == "save" && item.DurationMS == 10
		}
	}
	if !sampling || !phase || !approval {
		t.Fatalf("evidence missing %+v", items)
	}
}

func TestStreamedSamplingSummarySurvivesStorage(t *testing.T) {
	stream := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"Visible fixture summary\"}}]}\n\n" + sseText("done")
	s := newTestSession(t, &fakeModel{script: []string{stream}}, protocol.ApprovalBypass)
	s.RunTurn(context.Background(), "t", "hi", nil, nil, &recordingEmitter{approve: true})
	db := newTestStore(t)
	if err := s.Save(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	items, err := StoredHistory(context.Background(), db, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Kind == protocol.ItemLLM && item.Text == "Visible fixture summary" && item.StartedAt > 0 {
			return
		}
	}
	t.Fatalf("streamed summary lost: %+v", items)
}
