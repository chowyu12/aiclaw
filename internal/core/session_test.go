package core

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chowyu12/aiclaw/internal/config"
	"github.com/chowyu12/aiclaw/internal/memory"
	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/protocol"
	"github.com/chowyu12/aiclaw/internal/store/gormstore"
)

type scriptedSampler struct {
	calls    int
	requests []SamplingRequest
}

func (s *scriptedSampler) Sample(_ context.Context, request SamplingRequest, emit func(string) error) (SamplingResult, error) {
	s.calls++
	s.requests = append(s.requests, request)
	if s.calls == 1 {
		return SamplingResult{ToolCalls: []ToolCall{{ID: "call-1", Name: "lookup", Arguments: `{"q":"codex"}`}}}, nil
	}
	if err := emit("final answer"); err != nil {
		return SamplingResult{}, err
	}
	return SamplingResult{Text: "final answer"}, nil
}

type scriptedDispatcher struct{}
type failingDispatcher struct{ scriptedDispatcher }

func (scriptedDispatcher) Definitions(context.Context, model.Thread) ([]ToolDefinition, error) {
	return []ToolDefinition{{Name: "lookup"}}, nil
}

type memoryCaptureSampler struct {
	requests []SamplingRequest
}

func (s *memoryCaptureSampler) Sample(_ context.Context, request SamplingRequest, emit func(string) error) (SamplingResult, error) {
	s.requests = append(s.requests, request)
	if err := emit("好的"); err != nil {
		return SamplingResult{}, err
	}
	return SamplingResult{Text: "好的"}, nil
}
func (scriptedDispatcher) Execute(_ context.Context, _ model.Thread, call ToolCall) (ToolResult, error) {
	if call.Name != "lookup" {
		return ToolResult{}, fmt.Errorf("unexpected tool")
	}
	return ToolResult{CallID: call.ID, Name: call.Name, Content: "found"}, nil
}
func (failingDispatcher) Execute(context.Context, model.Thread, ToolCall) (ToolResult, error) {
	return ToolResult{}, fmt.Errorf("tool unavailable")
}

func TestSessionPersistsTurnBeforeAndAfterOutput(t *testing.T) {
	ctx := context.Background()
	store, err := gormstore.New(config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "session.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	thread := &model.Thread{UserID: "local", AgentUUID: "agent", ProviderID: 1, ModelName: "model"}
	if err := store.CreateThread(ctx, thread); err != nil {
		t.Fatal(err)
	}
	var events []protocol.Event
	session, err := Resume(ctx, store, thread.UUID, func(event protocol.Event) error { events = append(events, event); return nil })
	if err != nil {
		t.Fatal(err)
	}
	turn, err := session.StartTurn(ctx, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.StartTurn(ctx, "must wait"); err == nil {
		t.Fatal("concurrent turn was accepted")
	}
	if err := session.AppendAssistantDelta(ctx, turn.ID, "hi"); err != nil {
		t.Fatal(err)
	}
	if err := session.CompleteTurn(ctx, turn.ID, "hi"); err != nil {
		t.Fatal(err)
	}

	items, err := store.LoadRollout(ctx, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []model.RolloutKind{model.RolloutThreadStarted, model.RolloutTurnStarted, model.RolloutUserMessage, model.RolloutAssistantFinal, model.RolloutTurnCompleted}
	if len(items) != len(want) {
		t.Fatalf("items = %d, want %d", len(items), len(want))
	}
	for i, item := range items {
		if item.Kind != want[i] {
			t.Fatalf("item %d = %q, want %q", i, item.Kind, want[i])
		}
	}
	if len(events) != 3 || events[0].Kind != protocol.EventTurnStarted || events[1].Kind != protocol.EventAssistantDelta || events[2].Kind != protocol.EventTurnCompleted {
		t.Fatalf("events = %#v", events)
	}
	contextItems, err := session.ModelContext(ctx, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if len(contextItems) != 2 || contextItems[0].Kind != model.RolloutUserMessage || contextItems[1].Kind != model.RolloutAssistantFinal {
		t.Fatalf("model context = %#v", contextItems)
	}
}

func TestRunTurnPersistsToolRoundAndResumesContext(t *testing.T) {
	ctx := context.Background()
	store, err := gormstore.New(config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "turn.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	thread := &model.Thread{UserID: "local", AgentUUID: "agent", ProviderID: 1, ModelName: "model"}
	if err := store.CreateThread(ctx, thread); err != nil {
		t.Fatal(err)
	}
	var events []protocol.Event
	session, err := Resume(ctx, store, thread.UUID, func(event protocol.Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sampler := &scriptedSampler{}
	if err := session.RunTurn(ctx, "use a tool", sampler, scriptedDispatcher{}); err != nil {
		t.Fatal(err)
	}
	if sampler.calls != 2 {
		t.Fatalf("sampler calls = %d, want 2", sampler.calls)
	}
	var lifecycle []protocol.Event
	for _, event := range events {
		if event.Kind == protocol.EventToolLifecycle {
			lifecycle = append(lifecycle, event)
		}
	}
	if len(lifecycle) != 3 || lifecycle[0].Status != string(model.StepPending) || lifecycle[1].Status != string(model.StepRunning) || lifecycle[2].Status != string(model.StepSuccess) {
		t.Fatalf("tool lifecycle events = %#v", lifecycle)
	}
	for _, event := range lifecycle {
		if event.CallID != "call-1" || event.Name != "lookup" {
			t.Fatalf("tool lifecycle identity lost: %#v", event)
		}
	}
	if lifecycle[0].Input != `{"q":"codex"}` || lifecycle[1].Input != `{"q":"codex"}` {
		t.Fatalf("tool input missing before execution: %#v", lifecycle)
	}
	if lifecycle[1].StartedAt <= 0 {
		t.Fatalf("running event has no start timestamp: %#v", lifecycle[1])
	}
	if lifecycle[2].Input != `{"q":"codex"}` || lifecycle[2].Output != "found" || lifecycle[2].DurationMS <= 0 {
		t.Fatalf("completed event has no execution detail: %#v", lifecycle[2])
	}
	second := sampler.requests[1].Messages
	if len(second) != 3 {
		t.Fatalf("second model request has %d messages, want user + assistant tool call + tool result: %#v", len(second), second)
	}
	if second[1].Role != "assistant" || len(second[1].ToolCalls) != 1 || second[1].ToolCalls[0].ID != "call-1" {
		t.Fatalf("assistant tool request was not reconstructed: %#v", second[1])
	}
	if second[2].Role != "tool" || second[2].ToolCallID != "call-1" || second[2].Content != "found" {
		t.Fatalf("tool result was not linked to its request: %#v", second[2])
	}
	reloaded, err := Resume(ctx, store, thread.UUID, nil)
	if err != nil {
		t.Fatal(err)
	}
	contextItems, err := reloaded.ModelContext(ctx, 1024)
	if err != nil {
		t.Fatal(err)
	}
	foundToolRequest := false
	foundTool := false
	foundFinal := false
	for _, item := range contextItems {
		foundToolRequest = foundToolRequest || item.Kind == model.RolloutToolRequested
		foundTool = foundTool || item.Kind == model.RolloutToolCompleted
		foundFinal = foundFinal || item.Kind == model.RolloutAssistantFinal
	}
	if !foundToolRequest || !foundTool || !foundFinal {
		t.Fatalf("recovered model context misses tool/final: %#v", contextItems)
	}
}

func TestToolFailureKeepsLifecycleIdentity(t *testing.T) {
	ctx := context.Background()
	store, err := gormstore.New(config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "failed-tool.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	thread := &model.Thread{UserID: "local", ProviderID: 1, ModelName: "model"}
	if err := store.CreateThread(ctx, thread); err != nil {
		t.Fatal(err)
	}
	var events []protocol.Event
	session, err := Resume(ctx, store, thread.UUID, func(event protocol.Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.RunTurn(ctx, "use a failing tool", &scriptedSampler{}, failingDispatcher{}); err != nil {
		t.Fatal(err)
	}
	var failed *protocol.Event
	for index := range events {
		if events[index].Kind == protocol.EventToolLifecycle && events[index].Status == string(model.StepError) {
			failed = &events[index]
		}
	}
	if failed == nil || failed.CallID != "call-1" || failed.Name != "lookup" || failed.Error != "tool unavailable" {
		t.Fatalf("failed lifecycle identity missing: %#v", failed)
	}
}

func TestLocalDispatcherCarriesExplicitMemoryAcrossThreads(t *testing.T) {
	ctx := context.Background()
	store, err := gormstore.New(config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "desktop-memory.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	store.InitFTS5()
	dispatcher := NewLocalToolDispatcher(store)
	policyCtx := memory.WithTurnPolicy(ctx, memory.TurnPolicy{UseMemories: true, GenerateMemories: true})

	first := &model.Thread{UserID: "local", ProviderID: 1, ModelName: "model", Title: "remember"}
	if err := store.CreateThread(ctx, first); err != nil {
		t.Fatal(err)
	}
	session, err := Resume(policyCtx, store, first.UUID, nil)
	if err != nil {
		t.Fatal(err)
	}
	firstSampler := &memoryCaptureSampler{}
	if err := session.RunTurn(policyCtx, "你记住，我的位置是上海", firstSampler, dispatcher); err != nil {
		t.Fatal(err)
	}

	second := &model.Thread{UserID: "local", ProviderID: 1, ModelName: "model", Title: "recall"}
	if err := store.CreateThread(ctx, second); err != nil {
		t.Fatal(err)
	}
	session, err = Resume(policyCtx, store, second.UUID, nil)
	if err != nil {
		t.Fatal(err)
	}
	secondSampler := &memoryCaptureSampler{}
	if err := session.RunTurn(policyCtx, "我在哪里？", secondSampler, dispatcher); err != nil {
		t.Fatal(err)
	}
	if len(secondSampler.requests) != 1 || len(secondSampler.requests[0].Messages) < 2 {
		t.Fatalf("unexpected recall request: %#v", secondSampler.requests)
	}
	if secondSampler.requests[0].Messages[0].Role != "system" || !strings.Contains(secondSampler.requests[0].Messages[0].Content, "上海") {
		t.Fatalf("cross-thread memory was not injected: %#v", secondSampler.requests[0].Messages)
	}
}
