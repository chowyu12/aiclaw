package server

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/store"
)

func TestQueueEditPreservesIdentityAttachmentsOrderAndExecutedText(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	model := &scriptedModel{root: func(index int, r wireRequest) string {
		if index == 0 {
			<-release
		}
		return sseText("done")
	}, child: func(wireRequest) string { return sseText("child") }}
	s, root := collabServer(t, model)
	s.out = &receiptWriter{}
	submitQueued(t, s, root.ID, "running", "hold")
	eventually(t, "first sampling", func() bool { model.mu.Lock(); defer model.mu.Unlock(); return len(model.requests) == 1 })
	submitQueued(t, s, root.ID, "edited", "original", protocol.TurnStartParams{Images: [][]byte{[]byte("image")}, AudioPaths: []string{"clip.wav"}, References: []protocol.ThreadRef{{ID: "other", Title: "reference"}}})
	submitQueued(t, s, root.ID, "later", "last")
	before, _ := s.db.Submission(context.Background(), root.ID, "edited")
	edit := func(id, text, expected string) frame {
		return workCall(t, s, "session/queueEdit", map[string]any{"sessionId": root.ID, "requestId": id, "text": text, "expectedText": expected})
	}
	if f := edit("edited", "revised", "original"); f.Error != nil {
		t.Fatal(f.Error)
	}
	after, _ := s.db.Submission(context.Background(), root.ID, "edited")
	var input protocol.TurnStartParams
	_ = json.Unmarshal(after.Payload, &input)
	if input.Text != "revised" || len(input.Images) != 1 || string(input.Images[0]) != "image" || len(input.AudioPaths) != 1 || input.AudioPaths[0] != "clip.wav" || len(input.References) != 1 {
		t.Fatalf("lost payload: %+v", input)
	}
	if before.RequestID != after.RequestID || before.TurnID != after.TurnID || before.CreatedAt != after.CreatedAt || before.Fingerprint != after.Fingerprint {
		t.Fatal("edit changed submission identity")
	}
	s.submissionMu.Lock()
	queue := s.queuedTurns[root.ID]
	if len(queue) != 2 || queue[0].input.RequestID != "edited" || queue[0].input.Text != "revised" || queue[1].input.RequestID != "later" {
		t.Fatalf("wrong scheduled order: %+v", queue)
	}
	s.submissionMu.Unlock()
	if f := edit("edited", "stale overwrite", "original"); f.Error == nil {
		t.Fatal("stale edit accepted")
	}
	if f := edit("running", "changed", "hold"); f.Error == nil {
		t.Fatal("running edit accepted")
	}
	if f := edit("later", " \n ", "last"); f.Error == nil {
		t.Fatal("empty message accepted")
	}
	if f := edit("edited", "", "revised"); f.Error != nil {
		t.Fatal("cannot clear text while preserving attachments", f.Error)
	}
	if f := edit("edited", "revised", ""); f.Error != nil {
		t.Fatal(f.Error)
	}
	// Use a plain queued message for actual execution; attachment preservation was
	// checked above without accessing the intentionally nonexistent audio fixture.
	if f := edit("later", "last revised", "last"); f.Error != nil {
		t.Fatal(f.Error)
	}
	// Dismiss the attachment fixture, then verify the edited remaining turn runs.
	if f := workCall(t, s, "session/recover", map[string]any{"sessionId": root.ID, "requestId": "edited", "action": "dismiss"}); f.Error != nil {
		t.Fatal(f.Error)
	}
	once.Do(func() { close(release) })
	eventually(t, "edited queued execution", func() bool {
		r, _ := s.db.Submission(context.Background(), root.ID, "later")
		return r.State == "completed"
	})
	model.mu.Lock()
	last := model.requests[len(model.requests)-1].text()
	model.mu.Unlock()
	if !strings.Contains(last, "last revised") {
		t.Fatalf("scheduled stale text: %s", last)
	}
	if f := edit("later", "too late", "last revised"); f.Error == nil {
		t.Fatal("completed edit accepted")
	}
}

func TestQueueEditDoesNotResumePausedOrRecoveredInput(t *testing.T) {
	model := &scriptedModel{root: func(int, wireRequest) string { return sseText("done") }, child: func(wireRequest) string { return sseText("child") }}
	s, root := collabServer(t, model)
	s.out = &receiptWriter{}
	// An idle stopped session keeps queued payloads in SQLite without scheduling them.
	submitQueued(t, s, root.ID, "seed", "seed")
	eventually(t, "seed completed", func() bool { s.submissionMu.Lock(); defer s.submissionMu.Unlock(); return s.activeTurns[root.ID] == "" })
	raw, _ := json.Marshal(protocol.TurnStartParams{SessionID: root.ID, RequestID: "paused", Text: "paused original", Mode: "queue"})
	if err := s.db.AcceptSubmission(context.Background(), root.ID, store.Submission{RequestID: "paused", TurnID: "paused-turn", Fingerprint: "identity", Payload: raw}); err != nil {
		t.Fatal(err)
	}
	root.Interrupt()
	f := workCall(t, s, "session/queueEdit", map[string]any{"sessionId": root.ID, "requestId": "paused", "text": "paused revised", "expectedText": "paused original"})
	if f.Error != nil {
		t.Fatal(f.Error)
	}
	s.submissionMu.Lock()
	ids := s.queuedRequestIDs(root.ID)
	s.submissionMu.Unlock()
	if len(ids) != 0 || root.Busy() {
		t.Fatal("editing resumed paused work")
	}
	if err := s.db.RecoverSubmissions(context.Background()); err != nil {
		t.Fatal(err)
	}
	record, _ := s.db.Submission(context.Background(), root.ID, "paused")
	var payload protocol.TurnStartParams
	_ = json.Unmarshal(record.Payload, &payload)
	if record.State != "queued" || payload.Text != "paused revised" {
		t.Fatalf("lost durable edit: %+v", record)
	}
}
