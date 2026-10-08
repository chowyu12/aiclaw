package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/store"
)

func TestResumeDoesNotUnmountAfterFailedSave(t *testing.T) {
	s, root := collabServer(t, &scriptedModel{})
	w := &receiptWriter{}
	s.out = w
	// MarkDeleted makes Save fail while reads of the existing record still work.
	root.MarkDeleted()
	raw, _ := json.Marshal(protocol.SessionResumeParams{SessionID: root.ID, Refresh: &protocol.SessionRefresh{ApprovalPolicy: protocol.ApprovalAlways}})
	s.handleSessionResume(context.Background(), frame{ID: json.RawMessage(`1`), Params: raw})
	if s.session(root.ID) != root {
		t.Fatal("unmounted despite failed save")
	}
	if len(w.frames) != 1 || w.frames[0].Error == nil {
		t.Fatalf("save failure not returned: %+v", w.frames)
	}
}

func TestResumeKeepsSessionWithSubmittedTurnNotYetRunning(t *testing.T) {
	s, root := collabServer(t, &scriptedModel{})
	ctx := context.Background()
	if err := s.db.AcceptSubmission(ctx, root.ID, store.Submission{RequestID: "scheduled", TurnID: "turn", Fingerprint: "input", Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err := s.db.SetSubmission(ctx, root.ID, "scheduled", "turn", "running"); err != nil {
		t.Fatal(err)
	}
	if root.Busy() {
		t.Fatal("test must cover gap before RunTurn")
	}
	w := &receiptWriter{}
	s.out = w
	raw, _ := json.Marshal(protocol.SessionResumeParams{SessionID: root.ID, Refresh: &protocol.SessionRefresh{ApprovalPolicy: protocol.ApprovalAlways}})
	s.handleSessionResume(ctx, frame{ID: json.RawMessage(`1`), Params: raw})
	if s.session(root.ID) != root {
		t.Fatal("retired the scheduled turn's session")
	}
	if len(w.frames) != 1 || w.frames[0].Error != nil {
		t.Fatalf("resume failed: %+v", w.frames)
	}
}
