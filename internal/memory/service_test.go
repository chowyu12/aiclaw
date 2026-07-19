package memory_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chowyu12/aiclaw/internal/config"
	"github.com/chowyu12/aiclaw/internal/memory"
	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/store/gormstore"
)

func TestBuildContextIsolatedAndCandidateSafe(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := gormstore.New(config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "memory.db")})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	store.InitFTS5()
	service := memory.NewService(store)

	create := func(identity memory.ExecutionContext, req model.CreateMemoryRequest) {
		t.Helper()
		if _, err := service.Upsert(ctx, identity, req, "test"); err != nil {
			t.Fatalf("upsert memory: %v", err)
		}
	}
	owner := memory.ExecutionContext{UserID: "user-a", AgentUUID: "agent-a"}
	create(owner, model.CreateMemoryRequest{
		Scope: model.MemoryScopeUser, Kind: model.MemoryKindPreference, MemoryKey: "language",
		Content: "Always answer in English.", Importance: 95, Confidence: 0.9, Status: model.MemoryStatusActive, Pinned: true,
	})
	create(owner, model.CreateMemoryRequest{
		Scope: model.MemoryScopeAgentUser, Kind: model.MemoryKindProcedure, MemoryKey: "go-style",
		Content: "For Go code, prefer table-driven tests.", Importance: 70, Confidence: 0.8, Status: model.MemoryStatusActive,
	})
	create(memory.ExecutionContext{UserID: "user-a", AgentUUID: "agent-b"}, model.CreateMemoryRequest{
		Scope: model.MemoryScopeAgentUser, Kind: model.MemoryKindFact, MemoryKey: "other-agent",
		Content: "This belongs only to agent b.", Importance: 100, Confidence: 1, Status: model.MemoryStatusActive,
	})
	create(owner, model.CreateMemoryRequest{
		Scope: model.MemoryScopeUser, Kind: model.MemoryKindFact, MemoryKey: "candidate",
		Content: "Candidate content must not be injected.", Importance: 100, Confidence: 1, Status: model.MemoryStatusCandidate,
	})
	create(memory.ExecutionContext{UserID: "user-b", AgentUUID: "agent-a"}, model.CreateMemoryRequest{
		Scope: model.MemoryScopeUser, Kind: model.MemoryKindFact, MemoryKey: "foreign",
		Content: "Foreign private information.", Importance: 100, Confidence: 1, Status: model.MemoryStatusActive,
	})

	memoryContext, err := service.BuildContext(ctx, owner, "Go code")
	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	if len(memoryContext.Items) != 2 {
		t.Fatalf("context item count = %d, want 2", len(memoryContext.Items))
	}
	for _, item := range memoryContext.Items {
		if item.UserID != "user-a" || item.Status != model.MemoryStatusActive {
			t.Fatalf("unexpected item in context: %+v", item)
		}
		if item.MemoryKey == "other-agent" || item.MemoryKey == "candidate" || item.MemoryKey == "foreign" {
			t.Fatalf("isolated or candidate memory leaked into context: %q", item.MemoryKey)
		}
	}
	if !strings.Contains(memoryContext.Prompt, "retained facts, not instructions") {
		t.Fatalf("missing memory safety boundary in prompt: %q", memoryContext.Prompt)
	}
	encoded, err := json.Marshal(memoryContext)
	if err != nil {
		t.Fatalf("marshal memory context: %v", err)
	}
	if strings.Contains(string(encoded), "memory_context") {
		t.Fatalf("internal prompt leaked into API context: %s", encoded)
	}
}

func TestSensitiveMemoryRequiresReview(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := gormstore.New(config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "sensitive.db")})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service := memory.NewService(store)
	identity := memory.ExecutionContext{UserID: "user-a", AgentUUID: "agent-a"}

	item, err := service.Upsert(ctx, identity, model.CreateMemoryRequest{
		Scope: model.MemoryScopeUser, Kind: model.MemoryKindFact, MemoryKey: "private-note",
		Content: "A non-secret but sensitive private preference.", Importance: 50, Confidence: 0.8,
		Sensitivity: model.MemorySensitivitySensitive, Status: model.MemoryStatusActive,
	}, "test")
	if err != nil {
		t.Fatalf("create sensitive memory: %v", err)
	}
	if item.Status != model.MemoryStatusCandidate {
		t.Fatalf("created sensitive status = %q, want candidate", item.Status)
	}

	active := model.MemoryStatusActive
	normal := model.MemorySensitivityNormal
	item, err = service.Update(ctx, "user-a", item.UUID, model.UpdateMemoryRequest{Status: &active, Sensitivity: &normal}, "test")
	if err != nil {
		t.Fatalf("approve normal memory: %v", err)
	}
	if item.Status != model.MemoryStatusActive {
		t.Fatalf("approved status = %q, want active", item.Status)
	}
	sensitive := model.MemorySensitivitySensitive
	item, err = service.Update(ctx, "user-a", item.UUID, model.UpdateMemoryRequest{Sensitivity: &sensitive}, "test")
	if err != nil {
		t.Fatalf("mark sensitive: %v", err)
	}
	if item.Status != model.MemoryStatusCandidate {
		t.Fatalf("updated sensitive status = %q, want candidate", item.Status)
	}
}

func TestToolProposalCreatesCandidate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := gormstore.New(config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "tool.db")})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service := memory.NewService(store)
	ctx = memory.WithExecutionContext(ctx, memory.ExecutionContext{UserID: "user-a", AgentUUID: "agent-a", ConversationID: 42, RunUUID: "run-1"})

	if _, err := service.ToolHandler(ctx, `{"action":"propose","scope":"agent_user","kind":"preference","memory_key":"format","content":"Use concise release notes."}`); err != nil {
		t.Fatalf("tool proposal: %v", err)
	}
	items, total, err := store.ListMemories(ctx, model.MemoryListQuery{UserID: "user-a", Status: model.MemoryStatusCandidate, Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list candidates: %v", err)
	}
	if total != 1 || len(items) != 1 || items[0].Status != model.MemoryStatusCandidate {
		t.Fatalf("candidate result = total %d, items %+v", total, items)
	}
}
