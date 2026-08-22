package gormstore

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/chowyu12/aiclaw/internal/model"
)

func (s *GormStore) GetAppSetting(ctx context.Context, key, fallback string) (string, error) {
	var item model.AppSetting
	err := s.db.WithContext(ctx).Where("key = ?", key).First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return fallback, nil
	}
	if err != nil {
		return "", err
	}
	return item.Value, nil
}

func (s *GormStore) SetAppSetting(ctx context.Context, key, value string) error {
	item := model.AppSetting{Key: key, Value: value}
	return s.db.WithContext(ctx).Save(&item).Error
}
