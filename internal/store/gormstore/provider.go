package gormstore

import (
	"context"

	"github.com/chowyu12/aiclaw/internal/model"
)

func (s *GormStore) CreateProvider(ctx context.Context, p *model.Provider) error {
	return s.db.WithContext(ctx).Create(p).Error
}

func (s *GormStore) GetProvider(ctx context.Context, id int64) (*model.Provider, error) {
	var p model.Provider
	if err := s.db.WithContext(ctx).First(&p, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &p, nil
}

func (s *GormStore) ListProviders(ctx context.Context, q model.ListQuery) ([]*model.Provider, int64, error) {
	var items []*model.Provider
	var total int64

	db := s.db.WithContext(ctx).Model(&model.Provider{})
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

func (s *GormStore) UpdateProvider(ctx context.Context, id int64, req model.UpdateProviderReq) error {
	updates := map[string]any{}
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.Type != nil {
		updates["type"] = *req.Type
	}
	if req.BaseURL != nil {
		updates["base_url"] = *req.BaseURL
	}
	if req.APIKey != nil {
		updates["api_key"] = *req.APIKey
	}
	if req.Models != nil {
		updates["models"] = req.Models
	}
	if req.Enabled != nil {
		updates["enabled"] = *req.Enabled
	}
	if len(updates) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Model(&model.Provider{}).Where("id = ?", id).Updates(updates).Error
}

func (s *GormStore) DeleteProvider(ctx context.Context, id int64) error {
	return s.db.WithContext(ctx).Delete(&model.Provider{}, id).Error
}
