package gormstore

import (
	"context"

	"github.com/google/uuid"

	"github.com/chowyu12/aiclaw/internal/model"
)

func (s *GormStore) CreateTool(ctx context.Context, t *model.Tool) error {
	if t.UUID == "" {
		t.UUID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(t).Error
}

func (s *GormStore) GetTool(ctx context.Context, id int64) (*model.Tool, error) {
	var t model.Tool
	if err := s.db.WithContext(ctx).First(&t, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &t, nil
}

func (s *GormStore) ListTools(ctx context.Context, q model.ListQuery) ([]*model.Tool, int64, error) {
	var items []*model.Tool
	var total int64

	db := s.db.WithContext(ctx).Model(&model.Tool{})
	if q.Keyword != "" {
		db = db.Where("name LIKE ?", "%"+q.Keyword+"%")
	}
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(q)
	if err := db.Order("id DESC").Offset(offset).Limit(limit).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (s *GormStore) UpdateTool(ctx context.Context, id int64, req model.UpdateToolReq) error {
	updates := map[string]any{}
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.FunctionDef != nil {
		updates["function_def"] = req.FunctionDef
	}
	if req.HandlerType != nil {
		updates["handler_type"] = *req.HandlerType
	}
	if req.HandlerConfig != nil {
		updates["handler_config"] = req.HandlerConfig
	}
	if req.Enabled != nil {
		updates["enabled"] = *req.Enabled
	}
	if req.Timeout != nil {
		updates["timeout"] = *req.Timeout
	}
	if len(updates) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Model(&model.Tool{}).Where("id = ?", id).Updates(updates).Error
}

func (s *GormStore) DeleteTool(ctx context.Context, id int64) error {
	return s.db.WithContext(ctx).Delete(&model.Tool{}, id).Error
}
