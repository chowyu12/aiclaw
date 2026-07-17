package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/chowyu12/aiclaw/internal/model"
)

func TestLocalAgentRunLifecycle(t *testing.T) {
	ctx := t.Context()
	store := newMockStore()
	now := time.Now()
	runtime := &model.Runtime{
		Name: "Local Codex", Command: "codex", Args: model.StringSlice{"exec", "-"},
		Status: model.RuntimeStatusOnline, LastSeenAt: &now,
	}
	if err := store.CreateRuntime(ctx, runtime); err != nil {
		t.Fatal(err)
	}
	agent := &model.Agent{
		UUID: "local-agent", Name: "Local Agent", IsDefault: true,
		ExecutionMode: model.AgentExecutionLocal, RuntimeID: runtime.ID,
		WorkingDir: "/tmp/project", SystemPrompt: "Answer concisely.", MaxHistory: 10,
	}
	if err := store.CreateAgent(ctx, agent); err != nil {
		t.Fatal(err)
	}
	executor := NewExecutor(store, NewToolRegistry(), nil)

	run, err := executor.StartBackgroundRun(ctx, model.ChatRequest{AgentUUID: agent.UUID, UserID: "user", Message: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != model.AgentRunQueued || run.RuntimeID != runtime.ID {
		t.Fatalf("unexpected queued run: %#v", run)
	}
	// A local run can remain queued across a server restart. A fresh executor
	// must recreate the live stream entry when the browser reconnects.
	executor = NewExecutor(store, NewToolRegistry(), nil)
	_, _, unsubscribe, active, err := executor.SubscribeAgentRun(ctx, run.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if !active {
		t.Fatal("queued local run should restore an active subscription")
	}
	defer unsubscribe()

	task, err := executor.ClaimLocalAgentRun(ctx, runtime.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Command != "codex" || strings.Join(task.Args, " ") != "exec -" || task.WorkingDir != "/tmp/project" {
		t.Fatalf("unexpected runtime task: %#v", task)
	}
	if task.PromptMode != model.RuntimePromptStdin {
		t.Fatalf("unexpected prompt mode: %s", task.PromptMode)
	}
	if len(task.Messages) != 1 || task.Messages[0].Role != "user" || task.Messages[0].Content != "hello" {
		t.Fatalf("unexpected task history: %#v", task.Messages)
	}

	if err := executor.PublishLocalAgentRun(ctx, runtime.ID, run.UUID, "hi"); err != nil {
		t.Fatal(err)
	}
	completed, err := executor.CompleteLocalAgentRun(ctx, runtime.ID, run.UUID, "hi there", "")
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != model.AgentRunSucceeded || completed.Content != "hi there" || completed.MessageID == 0 {
		t.Fatalf("unexpected completed run: %#v", completed)
	}
	messages, err := store.ListMessages(ctx, completed.ConversationID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0].Role != "user" || messages[1].Role != "assistant" || messages[1].Content != "hi there" {
		t.Fatalf("unexpected persisted messages: %#v", messages)
	}
}

func TestLocalAgentRunRejectsOfflineRuntime(t *testing.T) {
	ctx := t.Context()
	store := newMockStore()
	runtime := &model.Runtime{Name: "Offline", Command: "agent", Status: model.RuntimeStatusOffline}
	if err := store.CreateRuntime(ctx, runtime); err != nil {
		t.Fatal(err)
	}
	agent := &model.Agent{
		UUID: "local-agent", Name: "Local Agent", IsDefault: true,
		ExecutionMode: model.AgentExecutionLocal, RuntimeID: runtime.ID,
	}
	if err := store.CreateAgent(ctx, agent); err != nil {
		t.Fatal(err)
	}
	executor := NewExecutor(store, NewToolRegistry(), nil)
	_, err := executor.StartBackgroundRun(ctx, model.ChatRequest{AgentUUID: agent.UUID, UserID: "user", Message: "hello"})
	if err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("expected offline error, got %v", err)
	}
}

func TestCancelQueuedLocalAgentRun(t *testing.T) {
	ctx := t.Context()
	store := newMockStore()
	now := time.Now()
	runtime := &model.Runtime{Name: "Local", Command: "agent", Status: model.RuntimeStatusOnline, LastSeenAt: &now}
	if err := store.CreateRuntime(ctx, runtime); err != nil {
		t.Fatal(err)
	}
	agent := &model.Agent{UUID: "local-agent", Name: "Local Agent", ExecutionMode: model.AgentExecutionLocal, RuntimeID: runtime.ID}
	if err := store.CreateAgent(ctx, agent); err != nil {
		t.Fatal(err)
	}
	executor := NewExecutor(store, NewToolRegistry(), nil)
	run, err := executor.StartBackgroundRun(ctx, model.ChatRequest{AgentUUID: agent.UUID, UserID: "user", Message: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := executor.CancelAgentRun(ctx, run.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != model.AgentRunCancelled {
		t.Fatalf("expected queued run to be cancelled, got %s", cancelled.Status)
	}
	if _, err := executor.ClaimLocalAgentRun(ctx, runtime.ID); !IsNoRuntimeTask(err) {
		t.Fatalf("cancelled run must not be claimable, got %v", err)
	}
}

func TestClaimFailsRunWhenAgentRuntimeChanged(t *testing.T) {
	ctx := t.Context()
	store := newMockStore()
	now := time.Now()
	runtime := &model.Runtime{Name: "Local", Command: "agent", Status: model.RuntimeStatusOnline, LastSeenAt: &now}
	if err := store.CreateRuntime(ctx, runtime); err != nil {
		t.Fatal(err)
	}
	agent := &model.Agent{UUID: "local-agent", Name: "Local Agent", ExecutionMode: model.AgentExecutionLocal, RuntimeID: runtime.ID}
	if err := store.CreateAgent(ctx, agent); err != nil {
		t.Fatal(err)
	}
	executor := NewExecutor(store, NewToolRegistry(), nil)
	run, err := executor.StartBackgroundRun(ctx, model.ChatRequest{AgentUUID: agent.UUID, UserID: "user", Message: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	managed := model.AgentExecutionManaged
	if err := store.UpdateAgent(ctx, agent.ID, &model.UpdateAgentReq{ExecutionMode: &managed}); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.ClaimLocalAgentRun(ctx, runtime.ID); err == nil {
		t.Fatal("expected claim to fail after agent runtime changed")
	}
	failed, err := store.GetAgentRunByUUID(ctx, run.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != model.AgentRunFailed {
		t.Fatalf("expected claimed run to be failed, got %s", failed.Status)
	}
}
