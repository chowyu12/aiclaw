package gormstore

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/chowyu12/aiclaw/internal/config"
	"github.com/chowyu12/aiclaw/internal/model"
)

func TestDeleteMessagesFromDeletesRetryArtifacts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := New(config.DatabaseConfig{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "retry.db"),
	})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	conversation := &model.Conversation{UserID: "user-1", AgentUUID: "agent-1"}
	if err := store.CreateConversation(ctx, conversation); err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	previousMessage := &model.Message{ConversationID: conversation.ID, Role: "user", Content: "keep this turn"}
	retryMessage := &model.Message{ConversationID: conversation.ID, Role: "user", Content: "retry this turn"}
	oldResponse := &model.Message{ConversationID: conversation.ID, Role: "assistant", Content: "old response"}
	for _, message := range []*model.Message{previousMessage, retryMessage, oldResponse} {
		if err := store.CreateMessage(ctx, message); err != nil {
			t.Fatalf("create message: %v", err)
		}
	}

	oldRun := &model.AgentRun{
		UUID:             "00000000-0000-0000-0000-000000000001",
		AgentID:          1,
		AgentUUID:        conversation.AgentUUID,
		ConversationID:   conversation.ID,
		ConversationUUID: conversation.UUID,
		MessageID:        0,
		UserID:           conversation.UserID,
		Input:            retryMessage.Content,
		Status:           model.AgentRunSucceeded,
	}
	if err := store.CreateAgentRun(ctx, oldRun); err != nil {
		t.Fatalf("create old run: %v", err)
	}
	if err := store.CreateExecutionStep(ctx, &model.ExecutionStep{
		RunUUID:        oldRun.UUID,
		ConversationID: conversation.ID,
		MessageID:      0,
		StepOrder:      1,
		StepType:       model.StepLLMCall,
		Name:           "llm",
		Status:         model.StepSuccess,
	}); err != nil {
		t.Fatalf("create execution step: %v", err)
	}
	for _, file := range []*model.File{
		{
			ConversationID: conversation.ID,
			MessageID:      oldResponse.ID,
			Filename:       "old-output.txt",
			FileType:       model.FileTypeText,
		},
		{
			ConversationID: conversation.ID,
			Filename:       "unfinished-output.txt",
			FileType:       model.FileTypeText,
		},
	} {
		if err := store.CreateFile(ctx, file); err != nil {
			t.Fatalf("create file: %v", err)
		}
	}
	oldPlan := &model.PlanRun{
		UUID:           "00000000-0000-0000-0000-000000000002",
		ConversationID: conversation.ID,
		MessageID:      0,
		Goal:           "answer the request",
		Source:         model.PlanSourceModel,
		Status:         model.PlanStatusCompleted,
	}
	if err := store.CreatePlanRun(ctx, oldPlan); err != nil {
		t.Fatalf("create plan: %v", err)
	}
	if err := store.ReplacePlanItems(ctx, oldPlan.ID, []model.PlanItem{{
		ItemKey: "answer",
		Title:   "Answer the request",
		Status:  model.PlanItemCompleted,
	}}); err != nil {
		t.Fatalf("create plan item: %v", err)
	}

	if err := store.DeleteMessagesFrom(ctx, conversation.ID, retryMessage.ID); err != nil {
		t.Fatalf("delete messages from retry turn: %v", err)
	}

	assertModelCount(t, store, &model.Message{}, 1, "conversation_id = ?", conversation.ID)
	assertModelCount(t, store, &model.AgentRun{}, 0, "conversation_id = ?", conversation.ID)
	assertModelCount(t, store, &model.ExecutionStep{}, 0, "conversation_id = ?", conversation.ID)
	assertModelCount(t, store, &model.File{}, 0, "conversation_id = ?", conversation.ID)
	assertModelCount(t, store, &model.PlanRun{}, 0, "conversation_id = ?", conversation.ID)
	assertModelCount(t, store, &model.PlanItem{}, 0, "plan_run_id = ?", oldPlan.ID)

	remaining, err := store.GetMessage(ctx, previousMessage.ID)
	if err != nil {
		t.Fatalf("get retained message: %v", err)
	}
	if remaining.Content != previousMessage.Content {
		t.Fatalf("retained message content = %q, want %q", remaining.Content, previousMessage.Content)
	}
}

func assertModelCount(t *testing.T, store *GormStore, value any, want int64, where string, args ...any) {
	t.Helper()
	var count int64
	if err := store.db.Model(value).Where(where, args...).Count(&count).Error; err != nil {
		t.Fatalf("count %T: %v", value, err)
	}
	if count != want {
		t.Fatalf("count %T = %d, want %d", value, count, want)
	}
}
