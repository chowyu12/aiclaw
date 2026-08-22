package gormstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/chowyu12/aiclaw/internal/model"
)

// MigrateConversation translates the former message-oriented conversation into
// a single event-sourced thread. It is idempotent, allowing startup migration
// to be safely retried after an interrupted local upgrade.
func (s *GormStore) MigrateConversation(ctx context.Context, conversationUUID string) (*model.Thread, error) {
	var existing model.Thread
	err := s.db.WithContext(ctx).Where("legacy_conversation_uuid = ?", conversationUUID).First(&existing).Error
	if err == nil {
		return &existing, nil
	}
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}

	var created model.Thread
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var conversation model.Conversation
		if err := tx.Where("uuid = ?", conversationUUID).First(&conversation).Error; err != nil {
			return err
		}
		var agent model.Agent
		if err := tx.Where("uuid = ?", conversation.AgentUUID).First(&agent).Error; err != nil {
			return err
		}
		thread := model.Thread{
			UUID: uuid.NewString(), SessionID: uuid.NewString(), LegacyConversationUUID: conversation.UUID,
			UserID: conversation.UserID, AgentUUID: agent.UUID, ProviderID: agent.ProviderID,
			ModelName: agent.ModelName, SearchEngineID: agent.SearchEngineID, WorkingDir: agent.WorkingDir, Title: conversation.Title,
			Status: model.ThreadStatusActive,
		}
		if err := tx.Create(&thread).Error; err != nil {
			return err
		}
		items := []model.RolloutItem{{ThreadID: thread.ID, Ordinal: 1, Kind: model.RolloutThreadStarted, Payload: model.JSON(fmt.Sprintf(`{"thread_id":%q,"legacy_conversation_id":%q}`, thread.UUID, conversation.UUID))}}
		var messages []model.Message
		if err := tx.Where("conversation_id = ?", conversation.ID).Order("id ASC").Find(&messages).Error; err != nil {
			return err
		}
		activeTurn := ""
		ordinal := int64(2)
		for _, message := range messages {
			payload, err := json.Marshal(map[string]any{"content": message.Content, "legacy_message_id": message.ID, "role": message.Role})
			if err != nil {
				return err
			}
			switch message.Role {
			case "user":
				activeTurn = uuid.NewString()
				items = append(items, model.RolloutItem{ThreadID: thread.ID, Ordinal: ordinal, TurnID: activeTurn, Kind: model.RolloutTurnStarted, Payload: model.JSON(`{}`)})
				ordinal++
				items = append(items, model.RolloutItem{ThreadID: thread.ID, Ordinal: ordinal, TurnID: activeTurn, Kind: model.RolloutUserMessage, ModelVisible: true, Payload: model.JSON(payload)})
			case "assistant":
				if activeTurn == "" {
					activeTurn = uuid.NewString()
				}
				items = append(items, model.RolloutItem{ThreadID: thread.ID, Ordinal: ordinal, TurnID: activeTurn, Kind: model.RolloutAssistantFinal, ModelVisible: true, Payload: model.JSON(payload)})
				ordinal++
				items = append(items, model.RolloutItem{ThreadID: thread.ID, Ordinal: ordinal, TurnID: activeTurn, Kind: model.RolloutTurnCompleted, Payload: model.JSON(`{}`)})
				activeTurn = ""
			default:
				if activeTurn == "" {
					activeTurn = uuid.NewString()
				}
				items = append(items, model.RolloutItem{ThreadID: thread.ID, Ordinal: ordinal, TurnID: activeTurn, Kind: model.RolloutToolCompleted, ModelVisible: true, Payload: model.JSON(payload)})
			}
			ordinal++
		}
		if err := tx.Create(&items).Error; err != nil {
			return err
		}
		created = thread
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &created, nil
}

// MigrateLegacyConversations upgrades all conversations for a local user. It
// is intentionally idempotent and can therefore run on every native App
// startup until the legacy projection is removed in a later migration.
func (s *GormStore) MigrateLegacyConversations(ctx context.Context, userID string) error {
	var conversations []model.Conversation
	db := s.db.WithContext(ctx).Order("id ASC")
	if userID != "" {
		db = db.Where("user_id = ?", userID)
	}
	if err := db.Find(&conversations).Error; err != nil {
		return err
	}
	for _, conversation := range conversations {
		if _, err := s.MigrateConversation(ctx, conversation.UUID); err != nil {
			return fmt.Errorf("migrate conversation %s: %w", conversation.UUID, err)
		}
	}
	return nil
}

// CreateThread opens a local, durable thread and records its creation as the
// first rollout item. This is the persistence boundary used by the new runtime.
func (s *GormStore) CreateThread(ctx context.Context, thread *model.Thread) error {
	if thread.UUID == "" {
		thread.UUID = uuid.NewString()
	}
	if thread.SessionID == "" {
		thread.SessionID = uuid.NewString()
	}
	if thread.Status == "" {
		thread.Status = model.ThreadStatusActive
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(thread).Error; err != nil {
			return err
		}
		return tx.Create(&model.RolloutItem{
			ThreadID: thread.ID, Ordinal: 1, Kind: model.RolloutThreadStarted,
			Payload: model.JSON(fmt.Sprintf(`{"thread_id":%q}`, thread.UUID)),
		}).Error
	})
}

func (s *GormStore) GetThreadByUUID(ctx context.Context, threadUUID string, includeArchived bool) (*model.Thread, error) {
	var thread model.Thread
	db := s.db.WithContext(ctx).Where("uuid = ?", threadUUID)
	if !includeArchived {
		db = db.Where("status <> ?", model.ThreadStatusArchived)
	}
	if err := db.First(&thread).Error; err != nil {
		return nil, notFound(err)
	}
	return &thread, nil
}

func (s *GormStore) ListThreads(ctx context.Context, userID string, includeArchived bool, page, pageSize int) ([]model.Thread, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	var threads []model.Thread
	var total int64
	db := s.db.WithContext(ctx).Model(&model.Thread{})
	if userID != "" {
		db = db.Where("user_id = ?", userID)
	}
	if !includeArchived {
		db = db.Where("status <> ?", model.ThreadStatusArchived)
	}
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := db.Order("updated_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&threads).Error; err != nil {
		return nil, 0, err
	}
	return threads, total, nil
}

func (s *GormStore) UpdateThreadProfile(ctx context.Context, threadUUID string, providerID int64, modelName string, searchEngineID int64) error {
	return s.db.WithContext(ctx).Model(&model.Thread{}).Where("uuid = ? AND status <> ?", threadUUID, model.ThreadStatusArchived).Updates(map[string]any{
		"provider_id": providerID, "model_name": modelName, "search_engine_id": searchEngineID,
	}).Error
}

func (s *GormStore) UpdateThreadProject(ctx context.Context, threadUUID, projectUUID string) error {
	result := s.db.WithContext(ctx).Model(&model.Thread{}).
		Where("uuid = ? AND status <> ?", threadUUID, model.ThreadStatusArchived).
		Update("project_uuid", projectUUID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *GormStore) CountThreadsUsingProvider(ctx context.Context, providerID int64) (int64, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&model.Thread{}).Where("provider_id = ?", providerID).Count(&count).Error
	return count, err
}

func (s *GormStore) CountThreadsUsingSearchEngine(ctx context.Context, searchEngineID int64) (int64, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&model.Thread{}).Where("search_engine_id = ?", searchEngineID).Count(&count).Error
	return count, err
}

// AppendRollout atomically assigns contiguous ordinals and writes immutable
// events. Callers never choose ordinals, preventing resume replay gaps.
func (s *GormStore) AppendRollout(ctx context.Context, threadID int64, items []model.RolloutItem) ([]model.RolloutItem, error) {
	if len(items) == 0 {
		return nil, nil
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var last model.RolloutItem
		result := tx.Where("thread_id = ?", threadID).Order("ordinal DESC").Limit(1).Find(&last)
		if result.Error != nil {
			return result.Error
		}
		next := last.Ordinal + 1
		for i := range items {
			items[i].ID = 0
			items[i].ThreadID = threadID
			items[i].Ordinal = next + int64(i)
			if items[i].CreatedAt.IsZero() {
				items[i].CreatedAt = time.Now()
			}
		}
		if err := tx.Create(&items).Error; err != nil {
			return err
		}
		return tx.Model(&model.Thread{}).Where("id = ?", threadID).Update("updated_at", time.Now()).Error
	})
	return items, err
}

func (s *GormStore) LoadRollout(ctx context.Context, threadID int64) ([]model.RolloutItem, error) {
	var items []model.RolloutItem
	if err := s.db.WithContext(ctx).Where("thread_id = ?", threadID).Order("ordinal ASC").Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

// RewindLastTurn removes the latest user turn atomically and returns its input.
// The caller can then submit that input again without exposing the discarded
// assistant answer or tool results to the next model request.
func (s *GormStore) RewindLastTurn(ctx context.Context, threadID int64) (string, error) {
	input, _, err := s.RewindLastTurnWithAttachments(ctx, threadID)
	return input, err
}

func (s *GormStore) RewindLastTurnWithAttachments(ctx context.Context, threadID int64) (string, []string, error) {
	var input string
	var attachments []string
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var userItem model.RolloutItem
		if err := tx.Where("thread_id = ? AND kind = ?", threadID, model.RolloutUserMessage).
			Order("ordinal DESC").First(&userItem).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return fmt.Errorf("thread has no turn to retry")
			}
			return err
		}
		if userItem.TurnID == "" {
			return fmt.Errorf("latest turn cannot be retried")
		}
		var payload struct {
			Content     string   `json:"content"`
			Attachments []string `json:"attachments"`
		}
		if err := json.Unmarshal(userItem.Payload, &payload); err != nil {
			return fmt.Errorf("decode latest turn input: %w", err)
		}
		input = payload.Content
		attachments = payload.Attachments
		if input == "" && len(attachments) == 0 {
			return fmt.Errorf("latest turn input is empty")
		}
		if err := tx.Where("thread_id = ? AND turn_id = ?", threadID, userItem.TurnID).
			Delete(&model.RolloutItem{}).Error; err != nil {
			return err
		}
		return tx.Model(&model.Thread{}).Where("id = ?", threadID).
			Update("updated_at", time.Now()).Error
	})
	return input, attachments, err
}

func (s *GormStore) ArchiveThread(ctx context.Context, threadID int64) error {
	now := time.Now()
	return s.db.WithContext(ctx).Model(&model.Thread{}).Where("id = ?", threadID).Updates(map[string]any{"status": model.ThreadStatusArchived, "archived_at": now}).Error
}
