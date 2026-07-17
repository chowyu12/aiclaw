package gormstore

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/chowyu12/aiclaw/internal/model"
)

var errAgentRunClaimRace = errors.New("agent run claim race")

func (s *GormStore) CreateAgentRun(ctx context.Context, run *model.AgentRun) error {
	if run.UUID == "" {
		run.UUID = uuid.NewString()
	}
	if run.Status == "" {
		run.Status = model.AgentRunRunning
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = time.Now()
	}
	return s.db.WithContext(ctx).Create(run).Error
}

func (s *GormStore) UpdateAgentRun(ctx context.Context, id int64, updates map[string]any) error {
	if id == 0 || len(updates) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Model(&model.AgentRun{}).Where("id = ?", id).Updates(updates).Error
}

func (s *GormStore) GetAgentRunByUUID(ctx context.Context, runUUID string) (*model.AgentRun, error) {
	var run model.AgentRun
	if err := s.db.WithContext(ctx).Where("uuid = ?", runUUID).First(&run).Error; err != nil {
		return nil, notFound(err)
	}
	return &run, nil
}

func (s *GormStore) ListAgentRuns(ctx context.Context, q model.AgentRunListQuery) ([]*model.AgentRun, int64, error) {
	var items []*model.AgentRun
	var total int64
	db := s.db.WithContext(ctx).Model(&model.AgentRun{})
	if q.AgentUUID != "" {
		db = db.Where("agent_uuid = ?", q.AgentUUID)
	}
	if q.ConversationUUID != "" {
		db = db.Where("conversation_uuid = ?", q.ConversationUUID)
	}
	if q.UserID != "" {
		db = db.Where("user_id = ?", q.UserID)
	}
	if q.Status != "" {
		db = db.Where("status = ?", q.Status)
	}
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
	if err := db.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (s *GormStore) ClaimQueuedAgentRun(ctx context.Context, runtimeID int64) (*model.AgentRun, error) {
	for attempt := 0; attempt < 3; attempt++ {
		var claimed model.AgentRun
		err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var candidate model.AgentRun
			if err := tx.Where("runtime_id = ? AND status = ?", runtimeID, model.AgentRunQueued).
				Order("id ASC").First(&candidate).Error; err != nil {
				return err
			}
			now := time.Now()
			res := tx.Model(&model.AgentRun{}).
				Where("id = ? AND status = ?", candidate.ID, model.AgentRunQueued).
				Updates(map[string]any{"status": model.AgentRunRunning, "claimed_at": now, "started_at": now})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return errAgentRunClaimRace
			}
			candidate.Status = model.AgentRunRunning
			candidate.ClaimedAt = &now
			candidate.StartedAt = now
			claimed = candidate
			return nil
		})
		if err == nil {
			return &claimed, nil
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, sql.ErrNoRows
		}
		if !errors.Is(err, errAgentRunClaimRace) {
			return nil, err
		}
	}
	return nil, sql.ErrNoRows
}
