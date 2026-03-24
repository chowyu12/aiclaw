package gormstore

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/chowyu12/aiclaw/internal/model"
)

func (s *GormStore) GetChannelThread(ctx context.Context, channelID int64, threadKey string) (*model.ChannelThread, error) {
	var row model.ChannelThread
	err := s.db.WithContext(ctx).Where("channel_id = ? AND thread_key = ?", channelID, threadKey).First(&row).Error
	if err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

func (s *GormStore) UpsertChannelThread(ctx context.Context, channelID int64, threadKey, conversationUUID string) error {
	if threadKey == "" || conversationUUID == "" {
		return errors.New("thread_key and conversation_uuid required")
	}
	var row model.ChannelThread
	err := s.db.WithContext(ctx).Where("channel_id = ? AND thread_key = ?", channelID, threadKey).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return s.db.WithContext(ctx).Create(&model.ChannelThread{
			ChannelID:        channelID,
			ThreadKey:        threadKey,
			ConversationUUID: conversationUUID,
		}).Error
	}
	if err != nil {
		return err
	}
	row.ConversationUUID = conversationUUID
	return s.db.WithContext(ctx).Save(&row).Error
}

func (s *GormStore) ListChannelThreads(ctx context.Context, channelID int64) ([]model.ChannelThread, error) {
	var rows []model.ChannelThread
	if err := s.db.WithContext(ctx).
		Where("channel_id = ?", channelID).
		Order("updated_at DESC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *GormStore) DeleteChannelThreadsByConversation(ctx context.Context, channelID int64, conversationUUID string) error {
	if conversationUUID == "" {
		return nil
	}
	return s.db.WithContext(ctx).
		Where("channel_id = ? AND conversation_uuid = ?", channelID, conversationUUID).
		Delete(&model.ChannelThread{}).Error
}
