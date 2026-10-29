package agent

import (
	"context"
	"testing"
	"time"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

func TestReservedWakeCannotRestartStoppedSession(t *testing.T) {
	model := &fakeModel{script: []string{sseText("explicit follow-up")}}
	session := newTestSession(t, model, protocol.ApprovalBypass)
	session.DeliverAgentMail("/root/worker", "first result", "")
	generation, allowed := session.ReserveWake()
	if !allowed {
		t.Fatal("fresh session should allow wakes")
	}
	session.runMu.Lock()
	done := make(chan struct{})
	go func() {
		session.RunReservedTurn(context.Background(), "old-wake", "", &recordingEmitter{}, generation)
		close(done)
	}()
	session.Interrupt()
	session.runMu.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("wake did not finish")
	}
	if model.calls != 0 {
		t.Fatal("stale wake reached the model")
	}
	session.DeliverAgentMail("/root/worker", "late result", "")
	if _, allowed := session.ReserveWake(); allowed {
		t.Fatal("late messages must not reopen admission")
	}
	session.mu.Lock()
	queued := len(session.pending)
	session.mu.Unlock()
	if queued != 2 {
		t.Fatalf("messages lost: %d", queued)
	}
	session.RunTurn(context.Background(), "explicit", "continue", nil, nil, &recordingEmitter{})
	if model.calls != 1 {
		t.Fatalf("explicit continuation did not run: %d", model.calls)
	}
	if _, allowed := session.ReserveWake(); !allowed {
		t.Fatal("explicit continuation did not reopen admission")
	}
}

func TestInlineMissingModelStopsWithoutRetry(t *testing.T) {
	model := &fakeModel{script: []string{`data: {"error":{"code":"model_not_found","message":"fixture model is unavailable"}}` + "\n\n", sseText("must not run")}}
	session := newTestSession(t, model, protocol.ApprovalBypass)
	emitter := &recordingEmitter{}
	session.RunTurn(context.Background(), "t", "test", nil, nil, emitter)
	if model.calls != 1 {
		t.Fatalf("retried missing model: %d", model.calls)
	}
	if !emitter.find(protocol.NotifyTurnCompleted, "fixture model is unavailable") {
		t.Fatal("provider message lost")
	}
}

func TestReservedFollowupIsInvalidatedByLaterStop(t *testing.T) {
	model := &fakeModel{script: []string{sseText("new task")}}
	session := newTestSession(t, model, protocol.ApprovalBypass)
	generation := session.ReserveFollowup()
	session.Interrupt()
	session.RunReservedTurn(context.Background(), "old", "old queued task", &recordingEmitter{}, generation)
	if model.calls != 0 {
		t.Fatal("reserved child task restarted after stop")
	}
	generation = session.ReserveFollowup()
	session.RunReservedTurn(context.Background(), "new", "explicit new task", &recordingEmitter{}, generation)
	if model.calls != 1 {
		t.Fatal("fresh follow-up did not run")
	}
}

func TestNewFollowupPreservesAlreadyReservedTask(t *testing.T) {
	model := &fakeModel{script: []string{sseText("done")}}
	session := newTestSession(t, model, protocol.ApprovalBypass)
	generation := session.ReserveFollowup()
	session.ReserveFollowup()
	session.DeliverAgentMail("/root", "additional instruction", "")
	session.RunReservedTurn(context.Background(), "initial", "original task", &recordingEmitter{}, generation)
	if model.calls != 1 {
		t.Fatal("a new follow-up invalidated the original task before it started")
	}
}
