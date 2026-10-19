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
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/tools"
)

func TestSteeringCancelsRunningToolsAndCodeMode(t *testing.T) {
	for _, codeMode := range []bool{false, true} {
		t.Run(fmt.Sprint(codeMode), func(t *testing.T) {
			entered := make(chan struct{})
			var later atomic.Int32
			call := sseToolCall("c", "blocked", `{}`)
			if codeMode {
				call = sseToolCall("c", "exec", `{"code":"text('finished before steering'); await tools.blocked({}); await tools.later({});"}`)
			}
			model := &fakeModel{script: []string{call, sseText("new direction handled")}}
			s := newTestSession(t, model, protocol.ApprovalBypass)
			register(t, s, tools.Tool{Name: "blocked", Effect: tools.EffectRead, Handler: func(ctx context.Context, _ json.RawMessage, _ *tools.Env) (string, error) {
				close(entered)
				<-ctx.Done()
				return "", ctx.Err()
			}})
			register(t, s, tools.Tool{Name: "later", Effect: tools.EffectWrite, Handler: func(context.Context, json.RawMessage, *tools.Env) (string, error) {
				later.Add(1)
				return "unexpected", nil
			}})
			if codeMode {
				s.installCodeMode()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			e := &recordingEmitter{}
			done := make(chan struct{})
			go func() { s.RunTurn(ctx, "t", "original", nil, nil, e); close(done) }()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("tool did not start")
			}
			if _, ok := s.Enqueue("new direction", nil); !ok {
				t.Fatal("not queued")
			}
			select {
			case <-done:
			case <-ctx.Done():
				t.Fatal("steering did not cancel tool")
			}
			if later.Load() != 0 || !e.find(protocol.NotifyItemCompleted, "new direction handled") || e.find(protocol.NotifyTurnCompleted, `"error"`) {
				t.Fatal(e.events)
			}
			if codeMode && !e.find(protocol.NotifyItemCompleted, "finished before steering") {
				t.Fatal("lost completed script output")
			}
		})
	}
}

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
