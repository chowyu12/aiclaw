package gormstore

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/chowyu12/aiclaw/internal/config"
	"github.com/chowyu12/aiclaw/internal/model"
)

func TestThreadRolloutIsOrderedAndRecoverable(t *testing.T) {
	store, err := New(config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "threads.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	thread := &model.Thread{UserID: "local", AgentUUID: "agent-1", ProviderID: 1, ModelName: "model-1", Title: "first thread"}
	if err := store.CreateThread(context.Background(), thread); err != nil {
		t.Fatal(err)
	}
	items, err := store.AppendRollout(context.Background(), thread.ID, []model.RolloutItem{
		{TurnID: "turn-1", Kind: model.RolloutTurnStarted, Payload: model.JSON(`{"input":"hello"}`)},
		{TurnID: "turn-1", Kind: model.RolloutUserMessage, ModelVisible: true, Payload: model.JSON(`{"content":"hello"}`)},
		{TurnID: "turn-1", Kind: model.RolloutAssistantFinal, ModelVisible: true, Payload: model.JSON(`{"content":"hi"}`)},
		{TurnID: "turn-1", Kind: model.RolloutTurnCompleted, Payload: model.JSON(`{}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Ordinal != 2 || items[3].Ordinal != 5 {
		t.Fatalf("ordinals = %d..%d, want 2..5", items[0].Ordinal, items[3].Ordinal)
	}

	replayed, err := store.LoadRollout(context.Background(), thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []model.RolloutKind{
		model.RolloutThreadStarted,
		model.RolloutTurnStarted,
		model.RolloutUserMessage,
		model.RolloutAssistantFinal,
		model.RolloutTurnCompleted,
	}
	if len(replayed) != len(want) {
		t.Fatalf("replayed = %d items, want %d", len(replayed), len(want))
	}
	for i, item := range replayed {
		if item.Kind != want[i] {
			t.Fatalf("item %d kind = %q, want %q", i, item.Kind, want[i])
		}
	}

	if err := store.ArchiveThread(context.Background(), thread.ID); err != nil {
		t.Fatal(err)
	}
	_, err = store.GetThreadByUUID(context.Background(), thread.UUID, false)
	if err == nil {
		t.Fatal("active lookup unexpectedly found archived thread")
	}
	archived, err := store.GetThreadByUUID(context.Background(), thread.UUID, true)
	if err != nil {
		t.Fatal(err)
	}
	if archived.Status != model.ThreadStatusArchived {
		t.Fatalf("status = %q", archived.Status)
	}
}

func TestMigrateConversationIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store, err := New(config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "migrate.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	agent := &model.Agent{Name: "agent", ModelName: "model", ProviderID: 7}
	if err := store.CreateAgent(ctx, agent); err != nil {
		t.Fatal(err)
	}
	conversation := &model.Conversation{UserID: "local", AgentUUID: agent.UUID, Title: "old chat"}
	if err := store.CreateConversation(ctx, conversation); err != nil {
		t.Fatal(err)
	}
	for _, message := range []*model.Message{{ConversationID: conversation.ID, Role: "user", Content: "hello"}, {ConversationID: conversation.ID, Role: "assistant", Content: "hi"}} {
		if err := store.CreateMessage(ctx, message); err != nil {
			t.Fatal(err)
		}
	}
	first, err := store.MigrateConversation(ctx, conversation.UUID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.MigrateConversation(ctx, conversation.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if first.UUID != second.UUID {
		t.Fatalf("migration created two threads: %s and %s", first.UUID, second.UUID)
	}
	items, err := store.LoadRollout(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []model.RolloutKind{model.RolloutThreadStarted, model.RolloutTurnStarted, model.RolloutUserMessage, model.RolloutAssistantFinal, model.RolloutTurnCompleted}
	if len(items) != len(want) {
		t.Fatalf("items = %d, want %d", len(items), len(want))
	}
	for i, item := range items {
		if item.Kind != want[i] {
			t.Fatalf("item %d = %s, want %s", i, item.Kind, want[i])
		}
	}
}

func TestMigrateLegacyConversationsScopesUser(t *testing.T) {
	ctx := context.Background()
	store, err := New(config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "all-migrate.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	agent := &model.Agent{Name: "agent", ModelName: "model", ProviderID: 1}
	if err := store.CreateAgent(ctx, agent); err != nil {
		t.Fatal(err)
	}
	for _, userID := range []string{"local", "other"} {
		conversation := &model.Conversation{UserID: userID, AgentUUID: agent.UUID}
		if err := store.CreateConversation(ctx, conversation); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.MigrateLegacyConversations(ctx, "local"); err != nil {
		t.Fatal(err)
	}
	threads, count, err := store.ListThreads(ctx, "local", false, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || len(threads) != 1 {
		t.Fatalf("local threads = %d/%d, want 1", count, len(threads))
	}
}

func TestRewindLastTurnKeepsEarlierHistory(t *testing.T) {
	ctx := context.Background()
	store, err := New(config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "retry.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	thread := &model.Thread{UserID: "local", ProviderID: 1, ModelName: "model", Title: "retry"}
	if err := store.CreateThread(ctx, thread); err != nil {
		t.Fatal(err)
	}
	_, err = store.AppendRollout(ctx, thread.ID, []model.RolloutItem{
		{TurnID: "turn-1", Kind: model.RolloutTurnStarted, Payload: model.JSON(`{}`)},
		{TurnID: "turn-1", Kind: model.RolloutUserMessage, ModelVisible: true, Payload: model.JSON(`{"content":"first"}`)},
		{TurnID: "turn-1", Kind: model.RolloutAssistantFinal, ModelVisible: true, Payload: model.JSON(`{"content":"kept"}`)},
		{TurnID: "turn-1", Kind: model.RolloutTurnCompleted, Payload: model.JSON(`{}`)},
		{TurnID: "turn-2", Kind: model.RolloutTurnStarted, Payload: model.JSON(`{}`)},
		{TurnID: "turn-2", Kind: model.RolloutUserMessage, ModelVisible: true, Payload: model.JSON(`{"content":"retry me"}`)},
		{TurnID: "turn-2", Kind: model.RolloutToolRequested, ModelVisible: true, Payload: model.JSON(`{"content":"old tool call"}`)},
		{TurnID: "turn-2", Kind: model.RolloutAssistantFinal, ModelVisible: true, Payload: model.JSON(`{"content":"discarded"}`)},
		{TurnID: "turn-2", Kind: model.RolloutTurnCompleted, Payload: model.JSON(`{}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	input, err := store.RewindLastTurn(ctx, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if input != "retry me" {
		t.Fatalf("input = %q, want retry me", input)
	}
	items, err := store.LoadRollout(ctx, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 5 {
		t.Fatalf("items = %d, want thread start plus first turn", len(items))
	}
	for _, item := range items {
		if item.TurnID == "turn-2" {
			t.Fatalf("discarded turn remains: %+v", item)
		}
	}
}

func TestRewindLastTurnRestoresAttachments(t *testing.T) {
	ctx := context.Background()
	store, err := New(config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "retry-attachments.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	thread := &model.Thread{UserID: "local", ProviderID: 1, ModelName: "vision-model", Title: "attachment retry"}
	if err := store.CreateThread(ctx, thread); err != nil {
		t.Fatal(err)
	}
	_, err = store.AppendRollout(ctx, thread.ID, []model.RolloutItem{
		{TurnID: "turn-files", Kind: model.RolloutTurnStarted, Payload: model.JSON(`{}`)},
		{TurnID: "turn-files", Kind: model.RolloutUserMessage, ModelVisible: true, Payload: model.JSON(`{"content":"inspect","attachments":["file-a","file-b"]}`)},
		{TurnID: "turn-files", Kind: model.RolloutAssistantFinal, ModelVisible: true, Payload: model.JSON(`{"content":"failed"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	input, attachments, err := store.RewindLastTurnWithAttachments(ctx, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if input != "inspect" || len(attachments) != 2 || attachments[0] != "file-a" || attachments[1] != "file-b" {
		t.Fatalf("rewind lost attachments: input=%q attachments=%#v", input, attachments)
	}
}
