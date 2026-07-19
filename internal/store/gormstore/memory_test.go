package gormstore

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/chowyu12/aiclaw/internal/config"
	"github.com/chowyu12/aiclaw/internal/model"
)

func TestSearchMessagesRespectsConversationOwner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := New(config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "messages.db")})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	store.InitFTS5()

	first := &model.Conversation{UserID: "user-a", AgentUUID: "agent-a", Title: "private a"}
	second := &model.Conversation{UserID: "user-b", AgentUUID: "agent-b", Title: "private b"}
	for _, conversation := range []*model.Conversation{first, second} {
		if err := store.CreateConversation(ctx, conversation); err != nil {
			t.Fatalf("create conversation: %v", err)
		}
	}
	for _, message := range []*model.Message{
		{ConversationID: first.ID, Role: "user", Content: "needle only belongs to user a"},
		{ConversationID: second.ID, Role: "user", Content: "needle must not leak from user b"},
	} {
		if err := store.CreateMessage(ctx, message); err != nil {
			t.Fatalf("create message: %v", err)
		}
	}

	results, err := store.SearchMessages(ctx, "user-a", "needle", 10)
	if err != nil {
		t.Fatalf("search messages: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].ConversationUUID != first.UUID {
		t.Fatalf("conversation UUID = %q, want %q", results[0].ConversationUUID, first.UUID)
	}
}
