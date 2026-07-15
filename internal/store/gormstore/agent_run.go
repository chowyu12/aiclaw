package gormstore

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/chowyu12/aiclaw/internal/model"
)

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
