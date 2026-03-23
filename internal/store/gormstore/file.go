package gormstore

import (
	"context"

	"github.com/google/uuid"

	"github.com/chowyu12/aiclaw/internal/model"
)

func (s *GormStore) CreateFile(ctx context.Context, f *model.File) error {
	if f.UUID == "" {
		f.UUID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(f).Error
}

func (s *GormStore) GetFileByUUID(ctx context.Context, uid string) (*model.File, error) {
	var f model.File
	if err := s.db.WithContext(ctx).Where("uuid = ?", uid).First(&f).Error; err != nil {
		return nil, notFound(err)
	}
	return &f, nil
}

func (s *GormStore) ListFilesByConversation(ctx context.Context, conversationID int64) ([]*model.File, error) {
	var files []*model.File
	if err := s.db.WithContext(ctx).
		Where("conversation_id = ?", conversationID).
		Order("id").
		Find(&files).Error; err != nil {
		return nil, err
	}
	return files, nil
}

func (s *GormStore) ListFilesByMessage(ctx context.Context, messageID int64) ([]*model.File, error) {
	var files []*model.File
	if err := s.db.WithContext(ctx).
		Where("message_id = ?", messageID).
		Order("id").
		Find(&files).Error; err != nil {
		return nil, err
	}
	return files, nil
}

func (s *GormStore) UpdateFileMessageID(ctx context.Context, fileID, messageID int64) error {
	return s.db.WithContext(ctx).
		Model(&model.File{}).
		Where("id = ?", fileID).
		Update("message_id", messageID).Error
}

func (s *GormStore) LinkFileToMessage(ctx context.Context, fileID, conversationID, messageID int64) error {
	return s.db.WithContext(ctx).
		Model(&model.File{}).
		Where("id = ?", fileID).
		Updates(map[string]any{
			"conversation_id": conversationID,
			"message_id":      messageID,
		}).Error
}

func (s *GormStore) DeleteFile(ctx context.Context, id int64) error {
	return s.db.WithContext(ctx).Delete(&model.File{}, id).Error
}
