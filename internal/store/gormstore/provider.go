package gormstore

import (
	"context"

	"github.com/chowyu12/aiclaw/internal/model"
)

func (s *GormStore) CreateProvider(ctx context.Context, p *model.Provider) error {
	// 落库的是密文；调用方手里的结构体保持明文，它接着还要用。
	stored := *p
	stored.APIKey = s.seal(p.APIKey)
	if err := s.db.WithContext(ctx).Create(&stored).Error; err != nil {
		return err
	}
	p.ID, p.CreatedAt, p.UpdatedAt = stored.ID, stored.CreatedAt, stored.UpdatedAt
	return nil
}

func (s *GormStore) GetProvider(ctx context.Context, id int64) (*model.Provider, error) {
	var p model.Provider
	if err := s.db.WithContext(ctx).First(&p, id).Error; err != nil {
		return nil, notFound(err)
	}
	p.APIKey = s.open(p.APIKey, "provider api_key")
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
	for _, item := range items {
		item.APIKey = s.open(item.APIKey, "provider api_key")
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
		updates["api_key"] = s.seal(*req.APIKey)
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
