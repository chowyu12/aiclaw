package agent

import (
	"context"
	"testing"
	"time"

	"github.com/chowyu12/aiclaw/internal/model"
)

func TestStartBackgroundRunPersistsResultAfterRequestContextCancels(t *testing.T) {
	store := newMockStore()
	_, _ = seedAgent(t, store)
	executor := newTestExecutor(store, NewToolRegistry(), &mockLLMProvider{streamContent: "background result"})

	requestCtx, cancelRequest := context.WithCancel(t.Context())
	run, err := executor.StartBackgroundRun(requestCtx, model.ChatRequest{UserID: "u1", Message: "run in background"})
	if err != nil {
		t.Fatalf("StartBackgroundRun: %v", err)
	}
	if run.UUID == "" || run.Status != model.AgentRunRunning {
		t.Fatalf("unexpected started run: %#v", run)
	}
	cancelRequest()

	finished := waitForAgentRun(t, executor, run.UUID)
	if finished.Status != model.AgentRunSucceeded {
		t.Fatalf("run status = %q, error = %q", finished.Status, finished.Error)
	}
	if finished.Content != "background result" || finished.MessageID == 0 {
		t.Fatalf("run result was not persisted: %#v", finished)
	}
	steps, err := store.ListExecutionStepsByRun(t.Context(), run.UUID)
	if err != nil {
		t.Fatalf("ListExecutionStepsByRun: %v", err)
	}
	if len(steps) == 0 || steps[0].RunUUID != run.UUID {
		t.Fatalf("steps are not linked to run %q: %#v", run.UUID, steps)
	}
}

func TestAgentRunHubReplaysAndDoesNotBlock(t *testing.T) {
	hub := newAgentRunHub()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	hub.start("run-1", cancel)
	hub.publish("run-1", model.AgentRunEvent{Type: model.AgentRunEventStarted})

	events, unsubscribe, active := hub.subscribe("run-1")
	if !active {
		t.Fatal("expected active run subscription")
	}
	defer unsubscribe()
	select {
	case event := <-events:
		if event.Type != model.AgentRunEventStarted || event.RunID != "run-1" {
			t.Fatalf("unexpected replay event: %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for replay")
	}

	for i := 0; i < agentRunSubscriberBuffer+10; i++ {
		hub.publish("run-1", model.AgentRunEvent{Type: model.AgentRunEventUpdated})
	}
	select {
	case <-ctx.Done():
		// The hub must not cancel the run because one subscriber is slow.
		t.Fatal("slow subscriber cancelled the run")
	default:
	}
}

func waitForAgentRun(t *testing.T, executor *Executor, runID string) *model.AgentRun {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		run, err := executor.GetAgentRun(t.Context(), runID)
		if err == nil && run.Status != model.AgentRunRunning {
			return run
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for run %s", runID)
	return nil
}
