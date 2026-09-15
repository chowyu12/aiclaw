package connector

import (
	"context"
	"sync"
	"testing"
	"time"
)

func waitFor(t *testing.T, condition func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(message)
}

// Two turns on the same thread would interleave their replies, so messages
// from one conversation have to run one at a time.
func TestOneConversationRunsSerially(t *testing.T) {
	dispatcher := NewDispatcher(4)
	var mu sync.Mutex
	var order []string
	overlapped := false
	running := false

	release := make(chan struct{})
	for _, name := range []string{"first", "second", "third"} {
		if !dispatcher.Submit(context.Background(), "chat-1", func(context.Context) {
			mu.Lock()
			if running {
				overlapped = true
			}
			running = true
			mu.Unlock()
			<-release
			mu.Lock()
			running = false
			order = append(order, name)
			mu.Unlock()
		}) {
			t.Fatalf("%s was rejected", name)
		}
	}
	close(release)
	dispatcher.Wait()

	if overlapped {
		t.Fatal("two messages from one conversation ran at the same time")
	}
	if len(order) != 3 || order[0] != "first" {
		t.Fatalf("order = %v", order)
	}
}

// Different conversations must not block each other.
func TestDifferentConversationsRunConcurrently(t *testing.T) {
	dispatcher := NewDispatcher(2)
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	for _, key := range []string{"chat-1", "chat-2"} {
		if !dispatcher.Submit(context.Background(), key, func(context.Context) {
			started <- struct{}{}
			<-release
		}) {
			t.Fatalf("%s was rejected", key)
		}
	}
	waitFor(t, func() bool { return len(started) == 2 }, "conversations did not run concurrently")
	close(release)
	dispatcher.Wait()
}

// An external party controls the message rate, so the bound has to hold and
// rejection has to be visible to the caller rather than a silent drop.
func TestCapacityIsBoundedAndRejectionIsReported(t *testing.T) {
	dispatcher := NewDispatcher(1)
	release := make(chan struct{})
	if !dispatcher.Submit(context.Background(), "chat-1", func(context.Context) { <-release }) {
		t.Fatal("the first message was rejected")
	}
	waitFor(t, func() bool { return true }, "")

	if dispatcher.Submit(context.Background(), "chat-2", func(context.Context) {}) {
		t.Fatal("a second conversation was accepted beyond capacity")
	}
	close(release)
	dispatcher.Wait()
}

// A panic while serving one message must not stop the conversation from
// serving the next.
func TestPanicDoesNotStopTheConversation(t *testing.T) {
	dispatcher := NewDispatcher(2)
	served := make(chan string, 2)
	if !dispatcher.Submit(context.Background(), "chat-1", func(context.Context) {
		served <- "first"
		panic("handler exploded")
	}) {
		t.Fatal("the first message was rejected")
	}
	if !dispatcher.Submit(context.Background(), "chat-1", func(context.Context) {
		served <- "second"
	}) {
		t.Fatal("the follow-up was rejected")
	}
	dispatcher.Wait()
	if len(served) != 2 {
		t.Fatalf("served = %d messages after a panic", len(served))
	}
}
