package core

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/chowyu12/aiclaw/internal/config"
	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/store/gormstore"
	"github.com/chowyu12/aiclaw/internal/tools"
)

type subAgentTestSampler struct{}

func (subAgentTestSampler) Sample(_ context.Context, request SamplingRequest, _ func(string) error) (SamplingResult, error) {
	return SamplingResult{Text: "sub-agent completed: " + request.Messages[len(request.Messages)-1].Content}, nil
}

func newToolTestRuntime(t *testing.T) (context.Context, *gormstore.GormStore, *LocalToolDispatcher, model.Thread) {
	t.Helper()
	ctx := context.Background()
	database, err := gormstore.New(config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "tools.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	thread := model.Thread{UserID: "local", AgentUUID: "desktop", ProviderID: 1, ModelName: "test-model", SearchEngineID: 1, Title: "Contract thread"}
	if err := database.CreateThread(ctx, &thread); err != nil {
		t.Fatal(err)
	}
	dispatcher := NewLocalToolDispatcher(database, WithDispatcherRoot(t.TempDir()), WithSubAgentSampler(subAgentTestSampler{}))
	ctx, err = dispatcher.PrepareTurn(ctx, thread, Turn{ID: "turn-contract", Input: "test tools"})
	if err != nil {
		t.Fatal(err)
	}
	return ctx, database, dispatcher, thread
}

func TestAdvertisedBuiltinToolsAlwaysHaveExecutors(t *testing.T) {
	ctx, _, dispatcher, thread := newToolTestRuntime(t)
	registry, err := dispatcher.registry(ctx, thread)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, definition := range tools.DefaultBuiltinDefs() {
		registered, ok := registry.Lookup(definition.Name)
		if !ok {
			t.Fatalf("advertised builtin %q is not registered", definition.Name)
		}
		if registered.Handler == nil {
			t.Fatalf("advertised builtin %q has no executor", definition.Name)
		}
	}
}

func TestRegistryRejectsDuplicateAndMissingHandlers(t *testing.T) {
	registry := NewToolRegistry()
	if err := registry.Register(RegisteredTool{Definition: ToolDefinition{Name: "broken"}}); err == nil {
		t.Fatal("tool without handler was accepted")
	}
	handler := func(context.Context, model.Thread, ToolCall) (ToolResult, error) { return ToolResult{}, nil }
	if err := registry.Register(RegisteredTool{Definition: ToolDefinition{Name: "same"}, Handler: handler, Source: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(RegisteredTool{Definition: ToolDefinition{Name: "same"}, Handler: handler, Source: "second"}); err == nil {
		t.Fatal("duplicate tool name was accepted")
	}
}

func TestRestoredCoreToolsExecute(t *testing.T) {
	ctx, database, dispatcher, thread := newToolTestRuntime(t)

	plan, err := dispatcher.Execute(ctx, thread, ToolCall{ID: "plan-1", Name: "plan", Arguments: `{"action":"set","goal":"ship","items":[{"id":"one","title":"First","status":"running"},{"id":"two","title":"Second"}]}`})
	if err != nil || plan.Content == "" {
		t.Fatalf("plan failed: result=%+v err=%v", plan, err)
	}
	rollout, err := database.LoadRollout(ctx, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundPlan := false
	for _, item := range rollout {
		foundPlan = foundPlan || item.Kind == model.RolloutPlanUpdated
	}
	if !foundPlan {
		t.Fatal("plan update was not persisted to the rollout")
	}

	search, err := dispatcher.Execute(ctx, thread, ToolCall{ID: "search-1", Name: "session_search", Arguments: `{"query":"contract"}`})
	if err != nil || search.Content == "" {
		t.Fatalf("session_search failed: result=%+v err=%v", search, err)
	}

	skill, err := dispatcher.Execute(ctx, thread, ToolCall{ID: "skill-1", Name: "skill", Arguments: `{"action":"list_active"}`})
	if err != nil || skill.Content == "" {
		t.Fatalf("skill failed: result=%+v err=%v", skill, err)
	}

	subAgent, err := dispatcher.Execute(ctx, thread, ToolCall{ID: "sub-1", Name: "sub_agent", Arguments: `{"tasks":[{"goal":"inspect contract"}]}`})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Success bool `json:"success"`
		Results []struct {
			Status string `json:"status"`
		} `json:"results"`
	}
	if json.Unmarshal([]byte(subAgent.Content), &payload) != nil || !payload.Success || len(payload.Results) != 1 || payload.Results[0].Status != "completed" {
		t.Fatalf("sub_agent result = %s", subAgent.Content)
	}
}
