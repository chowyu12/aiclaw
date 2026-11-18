package server

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/store"
	"testing"
	"time"
)

func TestSearchRPCStreamsCorrelatedResultsAndCleansUp(t *testing.T) {
	var output bytes.Buffer
	s, err := New(Options{DataHome: t.TempDir()}, &output)
	if err != nil {
		t.Fatal(err)
	}
	defer s.closeAll()
	defer s.db.Close()
	if err = s.db.Save(context.Background(), store.Session{ID: "match", Title: "needle", CreatedAt: time.Now(), UpdatedAt: time.Now(), Config: json.RawMessage(`{}`), Messages: json.RawMessage(`[]`)}); err != nil {
		t.Fatal(err)
	}
	s.handleSessionSearch(context.Background(), frame{ID: json.RawMessage(`1`), Params: json.RawMessage(`{"keyword":"needle","requestId":"unique"}`)})
	lines := bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("partial and final required: %s", output.String())
	}
	var partial struct {
		Method string
		Params struct {
			RequestID, Keyword string
			Sessions           []struct{ ID string }
		}
	}
	if err = json.Unmarshal(lines[0], &partial); err != nil || partial.Method != "session/searchPartial" || partial.Params.RequestID != "unique" || len(partial.Params.Sessions) != 1 || partial.Params.Sessions[0].ID != "match" {
		t.Fatalf("bad correlation: %s %v", lines[0], err)
	}
	if len(s.searches) != 0 {
		t.Fatal("completed search leaked cancellation entry")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	output.Reset()
	s.handleSessionSearch(ctx, frame{ID: json.RawMessage(`2`), Params: json.RawMessage(`{"keyword":"needle","requestId":"cancelled"}`)})
	if bytes.Contains(output.Bytes(), []byte("session/searchPartial")) || len(s.searches) != 0 {
		t.Fatal("cancelled search published results")
	}
}
func TestSearchCancellationAndShutdownRejectsNewSearches(t *testing.T) {
	var output bytes.Buffer
	s, err := New(Options{DataHome: t.TempDir()}, &output)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.searches = map[string]*sessionSearch{"request:target": {cancel: cancel}}
	s.dispatch(context.Background(), frame{ID: json.RawMessage(`1`), Method: "session/search/cancel", Params: json.RawMessage(`{"requestId":"target"}`)})
	if ctx.Err() != context.Canceled {
		t.Fatal("cancellation not delivered")
	}
	s.closeAll()
	output.Reset()
	s.handleSessionSearch(context.Background(), frame{ID: json.RawMessage(`2`), Params: json.RawMessage(`{"keyword":"needle"}`)})
	if !bytes.Contains(output.Bytes(), []byte("search unavailable")) {
		t.Fatal("search accepted after shutdown", output.String())
	}
}

func TestSearchCancelBeforeRegistrationPreventsScan(t *testing.T) {
	var output bytes.Buffer
	s, err := New(Options{DataHome: t.TempDir()}, &output)
	if err != nil {
		t.Fatal(err)
	}
	defer s.closeAll()
	s.dispatch(context.Background(), frame{ID: json.RawMessage(`1`), Method: "session/search/cancel", Params: json.RawMessage(`{"requestId":"early-cancel"}`)})
	output.Reset()
	s.handleSessionSearch(context.Background(), frame{ID: json.RawMessage(`2`), Params: json.RawMessage(`{"keyword":"needle","requestId":"early-cancel"}`)})
	if !bytes.Contains(output.Bytes(), []byte("search unavailable")) || len(s.searches) != 0 {
		t.Fatal("early cancellation lost", output.String())
	}
}
