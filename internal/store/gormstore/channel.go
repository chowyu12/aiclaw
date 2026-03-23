package gormstore

import (
	"context"
	"database/sql"

	"github.com/google/uuid"

	"github.com/chowyu12/aiclaw/internal/model"
)

func (s *GormStore) CreateChannel(ctx context.Context, c *model.Channel) error {
	if c.UUID == "" {
		c.UUID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(c).Error
}

func (s *GormStore) GetChannel(ctx context.Context, id int64) (*model.Channel, error) {
	var c model.Channel
	if err := s.db.WithContext(ctx).First(&c, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &c, nil
}

func (s *GormStore) GetChannelByUUID(ctx context.Context, u string) (*model.Channel, error) {
	var c model.Channel
	if err := s.db.WithContext(ctx).Where("uuid = ?", u).First(&c).Error; err != nil {
		return nil, notFound(err)
	}
	return &c, nil
}

func (s *GormStore) ListChannels(ctx context.Context, q model.ListQuery) ([]*model.Channel, int64, error) {
	var items []*model.Channel
	var total int64
	db := s.db.WithContext(ctx).Model(&model.Channel{})
	if q.Keyword != "" {
		kw := "%" + q.Keyword + "%"
		db = db.Where("name LIKE ? OR description LIKE ?", kw, kw)
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

func (s *GormStore) UpdateChannel(ctx context.Context, id int64, req model.UpdateChannelReq) error {
	updates := map[string]any{}
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.Enabled != nil {
		updates["enabled"] = *req.Enabled
	}
	if req.WebhookToken != nil {
		updates["webhook_token"] = *req.WebhookToken
	}
	if req.Config != nil {
		updates["config"] = req.Config
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if len(updates) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Model(&model.Channel{}).Where("id = ?", id).Updates(updates).Error
}

func (s *GormStore) DeleteChannel(ctx context.Context, id int64) error {
	res := s.db.WithContext(ctx).Delete(&model.Channel{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return sql.ErrNoRows
	}
	return nil
}
