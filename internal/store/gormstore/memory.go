package gormstore

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/chowyu12/aiclaw/internal/model"
)

func (s *GormStore) UpsertMemory(ctx context.Context, item *model.MemoryItem, evidence *model.MemoryEvidence, actor string) (*model.MemoryItem, error) {
	var saved model.MemoryItem
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing model.MemoryItem
		err := tx.Where("user_id = ? AND agent_uuid = ? AND scope = ? AND kind = ? AND memory_key = ? AND status IN ?",
			item.UserID, item.AgentUUID, item.Scope, item.Kind, item.MemoryKey,
			[]model.MemoryStatus{model.MemoryStatusActive, model.MemoryStatusCandidate}).
			Order("id DESC").First(&existing).Error

		action := model.MemoryRevisionCreated
		changed := true
		if err == nil {
			item.ID = existing.ID
			item.UUID = existing.UUID
			item.CreatedAt = existing.CreatedAt
			changed = memoryItemChanged(&existing, item)
			if changed {
				if err := tx.Model(&model.MemoryItem{}).Where("id = ?", existing.ID).Updates(map[string]any{
					"content":     item.Content,
					"summary":     item.Summary,
					"importance":  item.Importance,
					"confidence":  item.Confidence,
					"sensitivity": item.Sensitivity,
					"status":      item.Status,
					"pinned":      item.Pinned,
					"expires_at":  item.ExpiresAt,
				}).Error; err != nil {
					return err
				}
			}
			action = model.MemoryRevisionUpdated
		} else if err == gorm.ErrRecordNotFound {
			if item.UUID == "" {
				item.UUID = uuid.NewString()
			}
			if err := tx.Create(item).Error; err != nil {
				return err
			}
		} else {
			return err
		}

		if err := tx.First(&saved, item.ID).Error; err != nil {
			return err
		}
		if changed {
			if err := tx.Create(&model.MemoryRevision{
				MemoryID: saved.ID,
				Action:   action,
				Content:  saved.Content,
				Summary:  saved.Summary,
				Status:   saved.Status,
				Actor:    actor,
			}).Error; err != nil {
				return err
			}
		}
		if evidence != nil {
			evidence.ID = 0
			evidence.MemoryID = saved.ID
			if evidence.Relation == "" {
				evidence.Relation = model.MemoryEvidenceSource
			}
			if evidence.Source == "" {
				evidence.Source = actor
			}
			var count int64
			if err := tx.Model(&model.MemoryEvidence{}).Where(
				"memory_id = ? AND relation = ? AND conversation_id = ? AND message_id = ? AND run_uuid = ? AND source = ?",
				evidence.MemoryID, evidence.Relation, evidence.ConversationID, evidence.MessageID, evidence.RunUUID, evidence.Source,
			).Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				if err := tx.Create(evidence).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &saved, nil
}

func memoryItemChanged(existing, next *model.MemoryItem) bool {
	if existing == nil || next == nil {
		return true
	}
	if existing.Content != next.Content || existing.Summary != next.Summary || existing.Importance != next.Importance ||
		existing.Confidence != next.Confidence || existing.Sensitivity != next.Sensitivity || existing.Status != next.Status || existing.Pinned != next.Pinned {
		return true
	}
	if existing.ExpiresAt == nil || next.ExpiresAt == nil {
		return existing.ExpiresAt != next.ExpiresAt
	}
	return !existing.ExpiresAt.Equal(*next.ExpiresAt)
}

func (s *GormStore) GetMemoryByUUID(ctx context.Context, userID, memoryUUID string) (*model.MemoryItem, error) {
	var item model.MemoryItem
	if err := s.db.WithContext(ctx).Where("uuid = ? AND user_id = ?", memoryUUID, userID).First(&item).Error; err != nil {
		return nil, notFound(err)
	}
	return &item, nil
}

func (s *GormStore) ListMemories(ctx context.Context, q model.MemoryListQuery) ([]model.MemoryItem, int64, error) {
	db := s.db.WithContext(ctx).Model(&model.MemoryItem{}).Where("user_id = ?", q.UserID)
	if q.AgentUUID != "" {
		db = db.Where("agent_uuid = ?", q.AgentUUID)
	}
	if q.Scope != "" {
		db = db.Where("scope = ?", q.Scope)
	}
	if q.Kind != "" {
		db = db.Where("kind = ?", q.Kind)
	}
	if q.OnlyPending {
		db = db.Where("status = ?", model.MemoryStatusCandidate)
	} else if q.Status != "" {
		db = db.Where("status = ?", q.Status)
	} else if !q.IncludeAll {
		db = db.Where("status IN ?", []model.MemoryStatus{model.MemoryStatusActive, model.MemoryStatusCandidate})
	}
	if keyword := strings.TrimSpace(q.Keyword); keyword != "" {
		pattern := "%" + keyword + "%"
		db = db.Where("memory_key LIKE ? OR summary LIKE ? OR content LIKE ?", pattern, pattern, pattern)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	page, size := q.Page, q.PageSize
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 20
	}
	var items []model.MemoryItem
	if err := db.Order("pinned DESC, importance DESC, updated_at DESC").Offset((page - 1) * size).Limit(size).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (s *GormStore) RetrieveMemories(ctx context.Context, q model.MemoryRetrieveQuery) ([]model.MemoryItem, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 6
	}
	if limit > 12 {
		limit = 12
	}
	query := strings.TrimSpace(q.Query)
	if query != "" {
		if items, err := s.retrieveMemoriesFTS(ctx, q, limit); err == nil {
			return items, nil
		}
	}

	db := s.memoryVisibilityQuery(ctx, q.UserID, q.AgentUUID)
	if q.PinnedOnly {
		db = db.Where("pinned = ?", true)
	}
	if query != "" {
		pattern := "%" + query + "%"
		db = db.Where("memory_key LIKE ? OR summary LIKE ? OR content LIKE ?", pattern, pattern, pattern)
	}
	var items []model.MemoryItem
	if err := db.Order("pinned DESC, importance DESC, updated_at DESC").Limit(limit).Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

func (s *GormStore) memoryVisibilityQuery(ctx context.Context, userID, agentUUID string) *gorm.DB {
	return s.db.WithContext(ctx).Model(&model.MemoryItem{}).
		Where("user_id = ? AND status = ? AND (expires_at IS NULL OR expires_at > ?)", userID, model.MemoryStatusActive, time.Now()).
		Where("scope = ? OR (scope = ? AND agent_uuid = ?)", model.MemoryScopeUser, model.MemoryScopeAgentUser, agentUUID)
}

func (s *GormStore) retrieveMemoriesFTS(ctx context.Context, q model.MemoryRetrieveQuery, limit int) ([]model.MemoryItem, error) {
	var items []model.MemoryItem
	err := s.db.WithContext(ctx).Raw(`
		SELECT mi.*
		FROM memory_items_fts
		JOIN memory_items mi ON mi.id = memory_items_fts.rowid
		WHERE memory_items_fts MATCH ?
		  AND mi.user_id = ?
		  AND mi.status = ?
		  AND (mi.expires_at IS NULL OR mi.expires_at > ?)
		  AND (? = 0 OR mi.pinned = 1)
		  AND (mi.scope = ? OR (mi.scope = ? AND mi.agent_uuid = ?))
		ORDER BY mi.pinned DESC, bm25(memory_items_fts), mi.importance DESC, mi.updated_at DESC
		LIMIT ?`, sanitizeFTS5Query(q.Query), q.UserID, model.MemoryStatusActive, time.Now(),
		boolToInt(q.PinnedOnly), model.MemoryScopeUser, model.MemoryScopeAgentUser, q.AgentUUID, limit).Scan(&items).Error
	return items, err
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (s *GormStore) UpdateMemory(ctx context.Context, item *model.MemoryItem, action model.MemoryRevisionAction, actor string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.MemoryItem{}).Where("id = ?", item.ID).Updates(map[string]any{
			"content":     item.Content,
			"summary":     item.Summary,
			"importance":  item.Importance,
			"confidence":  item.Confidence,
			"sensitivity": item.Sensitivity,
			"status":      item.Status,
			"pinned":      item.Pinned,
			"expires_at":  item.ExpiresAt,
		}).Error; err != nil {
			return err
		}
		return tx.Create(&model.MemoryRevision{
			MemoryID: item.ID,
			Action:   action,
			Content:  item.Content,
			Summary:  item.Summary,
			Status:   item.Status,
			Actor:    actor,
		}).Error
	})
}

func (s *GormStore) DeleteMemory(ctx context.Context, userID, memoryUUID, actor string) error {
	item, err := s.GetMemoryByUUID(ctx, userID, memoryUUID)
	if err != nil {
		return err
	}
	item.Status = model.MemoryStatusDeleted
	return s.UpdateMemory(ctx, item, model.MemoryRevisionForgotten, actor)
}

func (s *GormStore) ListMemoryRevisions(ctx context.Context, memoryID int64) ([]model.MemoryRevision, error) {
	var items []model.MemoryRevision
	if err := s.db.WithContext(ctx).Where("memory_id = ?", memoryID).Order("id DESC").Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

func (s *GormStore) ListMemoryEvidence(ctx context.Context, memoryID int64) ([]model.MemoryEvidence, error) {
	var items []model.MemoryEvidence
	if err := s.db.WithContext(ctx).Where("memory_id = ?", memoryID).Order("id DESC").Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

func (s *GormStore) ListMemoryUsageByMessage(ctx context.Context, messageID int64) ([]model.MemoryItem, error) {
	var items []model.MemoryItem
	if err := s.db.WithContext(ctx).
		Table("memory_items mi").
		Select("mi.*").
		Joins("JOIN memory_evidence me ON me.memory_id = mi.id").
		Where("me.message_id = ? AND me.relation = ?", messageID, model.MemoryEvidenceUsed).
		Order("mi.importance DESC, mi.id ASC").
		Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

func (s *GormStore) RecordMemoryUsage(ctx context.Context, memoryIDs []int64, conversationID, messageID int64, runUUID string) error {
	if len(memoryIDs) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		seen := make(map[int64]struct{}, len(memoryIDs))
		for _, memoryID := range memoryIDs {
			if memoryID == 0 {
				continue
			}
			if _, exists := seen[memoryID]; exists {
				continue
			}
			seen[memoryID] = struct{}{}
			var count int64
			if err := tx.Model(&model.MemoryEvidence{}).Where("memory_id = ? AND relation = ? AND run_uuid = ?", memoryID, model.MemoryEvidenceUsed, runUUID).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				continue
			}
			if err := tx.Create(&model.MemoryEvidence{
				MemoryID:       memoryID,
				Relation:       model.MemoryEvidenceUsed,
				ConversationID: conversationID,
				MessageID:      messageID,
				RunUUID:        runUUID,
				Source:         "retrieval",
			}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *GormStore) UpdateMemoryEvidenceMessageIDByRun(ctx context.Context, runUUID string, messageID int64) error {
	if runUUID == "" || messageID == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Model(&model.MemoryEvidence{}).
		Where("run_uuid = ? AND message_id = 0", runUUID).
		Update("message_id", messageID).Error
}
