package gormstore

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/chowyu12/aiclaw/internal/model"
)

func (s *GormStore) ListChannelBindings(ctx context.Context) ([]model.ChannelBinding, error) {
	var items []model.ChannelBinding
	return items, s.db.WithContext(ctx).Order("updated_at DESC").Find(&items).Error
}

func (s *GormStore) GetChannelBinding(ctx context.Context, pluginUUID, channelID, connectionID, externalKey string) (*model.ChannelBinding, error) {
	var item model.ChannelBinding
	err := s.db.WithContext(ctx).
		Where("plugin_uuid = ? AND channel_id = ? AND connection_id = ? AND external_key = ?", pluginUUID, channelID, connectionID, externalKey).
		First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *GormStore) SaveChannelBinding(ctx context.Context, item *model.ChannelBinding) error {
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "plugin_uuid"}, {Name: "channel_id"}, {Name: "connection_id"}, {Name: "external_key"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"display_name", "thread_uuid", "provider_id", "model_name",
			"allowed", "allowed_tools", "last_message", "updated_at",
		}),
	}).Create(item).Error
}

// DeleteConnectionBindings 删掉一个连接的放行记录。会话本身留着（在会话库里，用户还能翻看）。
func (s *GormStore) DeleteConnectionBindings(ctx context.Context, pluginUUID, connectionID string) error {
	return s.db.WithContext(ctx).Where("plugin_uuid = ? AND connection_id = ?", pluginUUID, connectionID).
		Delete(&model.ChannelBinding{}).Error
}

func (s *GormStore) DeleteChannelBindings(ctx context.Context, pluginUUID string) error {
	return s.db.WithContext(ctx).Where("plugin_uuid = ?", pluginUUID).Delete(&model.ChannelBinding{}).Error
}
