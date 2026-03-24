package agent

import (
	"context"
	"testing"
	"time"
)

func TestToolCallContext_parentAlreadyDone_returnsParent(t *testing.T) {
	parent, cancel := context.WithCancel(t.Context())
	cancel()

	ctx, done := toolCallContext(parent)
	defer done()
	if ctx != parent {
		t.Fatal("expected same ctx as parent when parent already done")
	}
	if ctx.Err() == nil {
		t.Fatal("expected canceled ctx")
	}
}

func TestToolCallContext_parentDeadlineExceeded_returnsParent(t *testing.T) {
	parent, cancel := context.WithTimeout(t.Context(), time.Nanosecond)
	defer cancel()
	time.Sleep(2 * time.Millisecond)
	if parent.Err() == nil {
		t.Fatal("expected parent deadline exceeded")
	}

	ctx, done := toolCallContext(parent)
	defer done()
	if ctx != parent {
		t.Fatal("expected same ctx as parent for deadline exceeded")
	}
	if ctx.Err() == nil {
		t.Fatal("expected returned ctx to be done")
	}
}

func TestToolCallContext_parentCancel_propagatesToTool(t *testing.T) {
	parent, cancel := context.WithCancel(t.Context())

	ctx, done := toolCallContext(parent)
	defer done()
	if ctx.Err() != nil {
		t.Fatalf("unexpected initial tool ctx err: %v", ctx.Err())
	}

	cancel()
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("tool ctx should cancel when parent cancels")
	}
}
