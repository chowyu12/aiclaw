package gormstore

import (
	"context"
	"database/sql"

	"github.com/chowyu12/aiclaw/internal/model"
)

func (s *GormStore) ListSearchEngineConfigs(ctx context.Context, q model.ListQuery) ([]*model.SearchEngineConfig, int64, error) {
	var items []*model.SearchEngineConfig
	var total int64
	db := s.db.WithContext(ctx).Model(&model.SearchEngineConfig{})
	if q.Keyword != "" {
		kw := "%" + q.Keyword + "%"
		db = db.Where("name LIKE ? OR provider LIKE ?", kw, kw)
	}
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(q)
	if err := db.Order("id ASC").Offset(offset).Limit(limit).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (s *GormStore) GetSearchEngineConfig(ctx context.Context, id int64) (*model.SearchEngineConfig, error) {
	var cfg model.SearchEngineConfig
	if err := s.db.WithContext(ctx).First(&cfg, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &cfg, nil
}

func (s *GormStore) CreateSearchEngineConfig(ctx context.Context, cfg *model.SearchEngineConfig) error {
	return s.db.WithContext(ctx).Create(cfg).Error
}

func (s *GormStore) UpdateSearchEngineConfig(ctx context.Context, id int64, cfg *model.SearchEngineConfig) error {
	if _, err := s.GetSearchEngineConfig(ctx, id); err != nil {
		return err
	}
	cfg.ID = id
	return s.db.WithContext(ctx).Save(cfg).Error
}

func (s *GormStore) DeleteSearchEngineConfig(ctx context.Context, id int64) error {
	res := s.db.WithContext(ctx).Delete(&model.SearchEngineConfig{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return sql.ErrNoRows
	}
	return nil
}
