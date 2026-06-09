package gormstore

import (
	"context"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/chowyu12/aiclaw/internal/model"
)

func (s *GormStore) CreatePlanRun(ctx context.Context, run *model.PlanRun) error {
	if run.UUID == "" {
		run.UUID = uuid.New().String()
	}
	if run.Status == "" {
		run.Status = model.PlanStatusActive
	}
	return s.db.WithContext(ctx).Create(run).Error
}

func (s *GormStore) UpdatePlanRun(ctx context.Context, run *model.PlanRun) error {
	if run == nil || run.ID == 0 {
		return nil
	}
	return s.db.WithContext(ctx).
		Model(&model.PlanRun{}).
		Where("id = ?", run.ID).
		Updates(map[string]any{
			"message_id":      run.MessageID,
			"goal":            run.Goal,
			"status":          run.Status,
			"revision_reason": run.RevisionReason,
		}).Error
}

func (s *GormStore) GetActivePlanRun(ctx context.Context, conversationID int64) (*model.PlanRun, error) {
	var run model.PlanRun
	if err := s.db.WithContext(ctx).
		Where("conversation_id = ? AND message_id = 0 AND status = ?", conversationID, model.PlanStatusActive).
		Order("id DESC").
		First(&run).Error; err != nil {
		return nil, notFound(err)
	}
	return &run, nil
}

func (s *GormStore) GetPlanRunByMessage(ctx context.Context, messageID int64) (*model.PlanRun, error) {
	var run model.PlanRun
	if err := s.db.WithContext(ctx).Where("message_id = ?", messageID).Order("id DESC").First(&run).Error; err != nil {
		return nil, notFound(err)
	}
	return &run, nil
}

func (s *GormStore) ListPlanItems(ctx context.Context, planRunID int64) ([]model.PlanItem, error) {
	var items []model.PlanItem
	if err := s.db.WithContext(ctx).
		Where("plan_run_id = ?", planRunID).
		Order("sort_order ASC, id ASC").
		Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

func (s *GormStore) ReplacePlanItems(ctx context.Context, planRunID int64, items []model.PlanItem) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing []model.PlanItem
		if err := tx.Where("plan_run_id = ?", planRunID).Find(&existing).Error; err != nil {
			return err
		}
		byKey := make(map[string]model.PlanItem, len(existing))
		for _, item := range existing {
			byKey[item.ItemKey] = item
		}
		keptIDs := make(map[int64]bool, len(items))
		for i := range items {
			items[i].PlanRunID = planRunID
			if items[i].SortOrder == 0 {
				items[i].SortOrder = i + 1
			}
			if old, ok := byKey[items[i].ItemKey]; ok {
				items[i].ID = old.ID
				items[i].CreatedAt = old.CreatedAt
				keptIDs[old.ID] = true
				if err := tx.Model(&model.PlanItem{}).
					Where("id = ?", old.ID).
					Updates(map[string]any{
						"plan_run_id": items[i].PlanRunID,
						"item_key":    items[i].ItemKey,
						"title":       items[i].Title,
						"detail":      items[i].Detail,
						"status":      items[i].Status,
						"reason":      items[i].Reason,
						"step_id":     items[i].StepID,
						"sort_order":  items[i].SortOrder,
					}).Error; err != nil {
					return err
				}
				continue
			}
			if err := tx.Create(&items[i]).Error; err != nil {
				return err
			}
			keptIDs[items[i].ID] = true
		}
		var deleteIDs []int64
		for _, item := range existing {
			if !keptIDs[item.ID] {
				deleteIDs = append(deleteIDs, item.ID)
			}
		}
		if len(deleteIDs) > 0 {
			if err := tx.Where("id IN ?", deleteIDs).Delete(&model.PlanItem{}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *GormStore) UpdatePlanItemsMessageID(ctx context.Context, conversationID, messageID int64) error {
	return s.db.WithContext(ctx).
		Model(&model.PlanRun{}).
		Where("conversation_id = ? AND message_id = 0", conversationID).
		Update("message_id", messageID).Error
}

func (s *GormStore) DeletePlansByConversation(ctx context.Context, conversationID int64) error {
	var runIDs []int64
	if err := s.db.WithContext(ctx).Model(&model.PlanRun{}).Where("conversation_id = ?", conversationID).Pluck("id", &runIDs).Error; err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(runIDs) > 0 {
			if err := tx.Where("plan_run_id IN ?", runIDs).Delete(&model.PlanItem{}).Error; err != nil {
				return err
			}
		}
		return tx.Where("conversation_id = ?", conversationID).Delete(&model.PlanRun{}).Error
	})
}

func (s *GormStore) DeletePlansFromMessage(ctx context.Context, conversationID, fromMessageID int64) error {
	var runIDs []int64
	if err := s.db.WithContext(ctx).
		Model(&model.PlanRun{}).
		Where("conversation_id = ? AND (message_id = 0 OR message_id >= ?)", conversationID, fromMessageID).
		Pluck("id", &runIDs).Error; err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(runIDs) > 0 {
			if err := tx.Where("plan_run_id IN ?", runIDs).Delete(&model.PlanItem{}).Error; err != nil {
				return err
			}
		}
		return tx.Where("conversation_id = ? AND (message_id = 0 OR message_id >= ?)", conversationID, fromMessageID).Delete(&model.PlanRun{}).Error
	})
}
