package connector

import (
	"fmt"
	"testing"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/protocol"
)

func TestReplyPrefersTheFinalContentOverAccumulatedDeltas(t *testing.T) {
	reply := NewReply(nil)
	for _, event := range []protocol.Event{
		{Kind: protocol.EventAssistantDelta, Delta: "par"},
		{Kind: protocol.EventAssistantDelta, Delta: "tial"},
		{Kind: protocol.EventTurnCompleted, Item: &model.RolloutItem{Payload: model.JSON(`{"content":"complete answer"}`)}},
	} {
		if err := reply.Observe(event); err != nil {
			t.Fatal(err)
		}
	}
	if reply.Text() != "complete answer" {
		t.Fatalf("text = %q", reply.Text())
	}
	if reply.Failed() {
		t.Fatal("a completed turn was reported as failed")
	}
}

// A turn that never produced a final message still has its deltas, which is
// better than replying with nothing.
func TestReplyFallsBackToDeltas(t *testing.T) {
	reply := NewReply(nil)
	_ = reply.Observe(protocol.Event{Kind: protocol.EventAssistantDelta, Delta: "streamed only"})
	if reply.Text() != "streamed only" {
		t.Fatalf("text = %q", reply.Text())
	}
}

// A failed turn must be distinguishable from an empty successful one.
func TestReplyReportsFailure(t *testing.T) {
	reply := NewReply(nil)
	_ = reply.Observe(protocol.Event{Kind: protocol.EventTurnFailed, Error: "model unavailable"})
	if !reply.Failed() {
		t.Fatal("a failed turn was not reported as failed")
	}
}

// Streaming is best effort: a transport hiccup must not lose the final answer.
func TestStreamingErrorDoesNotDiscardTheAnswer(t *testing.T) {
	reply := NewReply(func(string) error { return fmt.Errorf("frame dropped") })
	_ = reply.Observe(protocol.Event{Kind: protocol.EventAssistantDelta, Delta: "hello"})
	_ = reply.Observe(protocol.Event{Kind: protocol.EventTurnCompleted, Output: "hello there"})
	if reply.StreamError() == nil {
		t.Fatal("the streaming error was swallowed")
	}
	if reply.Text() != "hello there" {
		t.Fatalf("text = %q", reply.Text())
	}
}
