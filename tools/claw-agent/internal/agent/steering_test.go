package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

func TestSteeringPreemptsBlockedModelAndKeepsPartialText(t *testing.T) {
	entered := make(chan struct{})
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial answer\"}}]}\n\n")
			w.(http.Flusher).Flush()
			close(entered)
			<-r.Context().Done()
			return
		}
		if !strings.Contains(fmt.Sprint(request["messages"]), "new direction") {
			t.Error("steered request missed user input")
		}
		fmt.Fprint(w, sseText("new answer"))
	}))
	defer upstream.Close()
	session, err := New(context.Background(), "s", protocol.SessionStartParams{Model: protocol.ModelConfig{BaseURL: upstream.URL, Model: "test"}, Workdir: t.TempDir()}, StaticKey("test"))
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	emitter := &recordingEmitter{}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { session.RunTurn(ctx, "t", "original", nil, nil, emitter); close(done) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("model did not start")
	}
	for !emitter.find(protocol.NotifyItemDelta, "partial answer") {
		select {
		case <-ctx.Done():
			t.Fatal("partial text was not delivered")
		case <-time.After(time.Millisecond):
		}
	}
	if _, ok := session.EnqueueRequest(protocol.TurnStartParams{Text: "new direction", RequestID: "r2"}); !ok {
		t.Fatal("not queued")
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("steering did not preempt blocked stream")
	}
	if !emitter.find(protocol.NotifyItemCompleted, "new answer") || !emitter.find(protocol.NotifyItemCompleted, `"requestId":"r2"`) {
		t.Fatal(emitter.events)
	}
	if emitter.find(protocol.NotifyTurnCompleted, `"error"`) {
		t.Fatal("steering ended the turn as an error")
	}
	history := fmt.Sprint(session.snapshotMessages())
	if !strings.Contains(history, "partial answer") {
		t.Fatal("partial response lost")
	}
}

func TestQueuedAudioAndRequestIdentitySurviveEnqueue(t *testing.T) {
	session := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	session.mu.Lock()
	session.cancelTurn = func() {}
	session.currentTurn = "t"
	session.mu.Unlock()
	turn, ok := session.EnqueueRequest(protocol.TurnStartParams{RequestID: "r", Text: "listen", AudioPaths: []string{"recording.wav"}})
	if !ok || turn != "t" {
		t.Fatal("input not queued")
	}
	session.mu.Lock()
	input := session.pending[0]
	session.finishing = true
	session.mu.Unlock()
	if len(input.audioPaths) != 1 || input.audioPaths[0] != "recording.wav" || input.requestID != "r" {
		t.Fatalf("attachments lost: %#v", input)
	}
	if _, queued := session.Enqueue("late input", nil); queued {
		t.Fatal("finishing turns must not accept stranded input")
	}
}
