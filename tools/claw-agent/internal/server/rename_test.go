package server

import (
	"context"
	"encoding/json"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"testing"
)

func renameCall(t *testing.T, s *Server, id, title string) frame {
	t.Helper()
	w, ok := s.out.(*receiptWriter)
	if !ok {
		w = &receiptWriter{}
		s.out = w
	}
	w.mu.Lock()
	offset := len(w.frames)
	w.mu.Unlock()
	raw, _ := json.Marshal(protocol.SessionRenameParams{SessionID: id, Title: title})
	s.dispatch(context.Background(), frame{Method: protocol.MethodSessionRename, ID: json.RawMessage(`99`), Params: raw})
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, f := range w.frames[offset:] {
		if string(f.ID) == "99" {
			return f
		}
	}
	t.Fatal("missing rename response")
	return frame{}
}

func TestRenameRPCHandlesLiveAndUnloadedSessions(t *testing.T) {
	s, root := collabServer(t, &scriptedModel{root: func(int, wireRequest) string { return sseText("answer") }})
	ctx := context.Background()
	if f := renameCall(t, s, root.ID, "  Custom chat  "); f.Error != nil {
		t.Fatal(f.Error)
	}
	if err := root.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	got, err := s.db.Load(ctx, root.ID)
	if err != nil || got.Title != "Custom chat" {
		t.Fatalf("%+v %v", got, err)
	}
	s.unload(root.ID)
	if f := renameCall(t, s, root.ID, "Dormant chat"); f.Error != nil {
		t.Fatal(f.Error)
	}
	raw, _ := json.Marshal(protocol.SessionResumeParams{SessionID: root.ID})
	s.handleSessionResume(ctx, frame{ID: json.RawMessage(`100`), Params: raw})
	resumed := s.session(root.ID)
	if resumed == nil {
		t.Fatal("resume failed")
	}
	if resumed.Title != "Dormant chat" {
		t.Fatal(resumed.Title)
	}
	if err := resumed.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	for _, input := range []struct{ id, title string }{{root.ID, " "}, {root.ID, "line\nbreak"}, {"missing", "valid"}, {"", "valid"}} {
		if f := renameCall(t, s, input.id, input.title); f.Error == nil || f.Error.Code != codeInvalidParams {
			t.Fatalf("%+v: %+v", input, f)
		}
	}
	got, _ = s.db.Load(ctx, root.ID)
	if got.Title != "Dormant chat" {
		t.Fatal(got.Title)
	}
}
