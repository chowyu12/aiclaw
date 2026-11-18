package agent

import (
	"context"
	"errors"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"testing"
	"time"
)

func TestSharedRetryPolicyHonorsRetryAfterAndCancellation(t *testing.T) {
	s := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	start := time.Now()
	if err := s.retryModel(context.Background(), "turn", &recordingEmitter{}, 1, &llm.Error{Status: 429, RetryAfter: 20 * time.Millisecond}); err != nil || time.Since(start) < 20*time.Millisecond {
		t.Fatal("Retry-After ignored", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	timer := time.AfterFunc(20*time.Millisecond, cancel)
	defer timer.Stop()
	start = time.Now()
	if err := s.retryModel(ctx, "turn", &recordingEmitter{}, 1, &llm.Error{Status: 503, RetryAfter: time.Minute}); !errors.Is(err, context.Canceled) || time.Since(start) > time.Second {
		t.Fatal("cancellation did not interrupt retry wait", err)
	}
	terminal := &llm.Error{Status: 503, Code: "invalid_workspace"}
	if err := s.retryModel(context.Background(), "turn", &recordingEmitter{}, 1, terminal); err != terminal {
		t.Fatal("terminal error retried", err)
	}
	exhausted := &llm.Error{Status: 503}
	if err := s.retryModel(context.Background(), "turn", &recordingEmitter{}, maxModelRetries+1, exhausted); err != exhausted {
		t.Fatal("retry limit ignored", err)
	}
}
