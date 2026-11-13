package agent

import (
	"context"
	"encoding/json"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"strings"
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

func TestEveryRetryRestoresUIEvidenceWithoutReplayingItToModel(t *testing.T) {
	partial := "data: {\"choices\":[{\"delta\":{\"content\":\"visible partial\",\"reasoning_content\":\"first visible summary\"}}]}\n\n"
	s := newTestSession(t, &fakeModel{script: []string{partial, sseText("final")}}, protocol.ApprovalBypass)
	emitter := &recordingEmitter{approve: true}
	s.RunTurn(context.Background(), "t", "hi", nil, nil, emitter)
	samples := map[string]protocol.Item{}
	for _, event := range emitter.events {
		if !strings.HasPrefix(event, protocol.NotifyItemCompleted+" ") {
			continue
		}
		var params protocol.ItemNotification
		if err := json.Unmarshal([]byte(strings.TrimPrefix(event, protocol.NotifyItemCompleted+" ")), &params); err != nil {
			t.Fatal(err)
		}
		if params.Item.Kind == protocol.ItemLLM {
			samples[params.Item.ID] = params.Item
		}
	}
	if len(samples) != 2 {
		t.Fatalf("expected retry fixture, got %d samples", len(samples))
	}
	db := newTestStore(t)
	if err := s.Save(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	restored, err := StoredHistory(context.Background(), db, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertHistory := func(items []protocol.Item) {
		t.Helper()
		seen := map[string]bool{}
		var answers []string
		var reasoning bool
		for _, item := range items {
			if seen[item.ID] {
				t.Fatalf("duplicate restored item %s", item.ID)
			}
			seen[item.ID] = true
			if item.Kind == protocol.ItemLLM {
				live, ok := samples[item.ID]
				if !ok || item.Seq != live.Seq || item.Text != live.Text || item.ToolFailed != live.ToolFailed {
					t.Fatalf("sampling evidence differs %+v", item)
				}
				reasoning = reasoning || item.Text == "first visible summary"
			}
			if item.Kind == protocol.ItemAgentMessage {
				answers = append(answers, item.Text)
			}
		}
		for id := range samples {
			if !seen[id] {
				t.Fatalf("missing sample %s", id)
			}
		}
		if !reasoning || strings.Join(answers, ",") != "visible partial,final" {
			t.Fatalf("visible output lost: %v", answers)
		}
	}
	assertHistory(s.History())
	assertHistory(restored)
	for _, m := range s.snapshotMessages() {
		if m.Role == llm.RoleAssistant && strings.Contains(m.Content, "visible partial") {
			t.Fatal("failed UI output entered model context")
		}
	}
}
