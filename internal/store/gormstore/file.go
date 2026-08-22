package gormstore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

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

func (s *GormStore) ListFilesByThread(ctx context.Context, threadID int64) ([]*model.File, error) {
	var files []*model.File
	if err := s.db.WithContext(ctx).
		Where("thread_id = ?", threadID).
		Order("id").
		Find(&files).Error; err != nil {
		return nil, err
	}
	return files, nil
}

// LinkFilesToThread claims staged desktop attachments for one durable thread.
// An attachment can be linked only once, except when retrying the same thread.
func (s *GormStore) LinkFilesToThread(ctx context.Context, threadID int64, fileUUIDs []string) error {
	unique := make([]string, 0, len(fileUUIDs))
	seen := make(map[string]struct{}, len(fileUUIDs))
	for _, fileUUID := range fileUUIDs {
		fileUUID = strings.TrimSpace(fileUUID)
		if fileUUID == "" {
			continue
		}
		if _, ok := seen[fileUUID]; ok {
			continue
		}
		seen[fileUUID] = struct{}{}
		unique = append(unique, fileUUID)
	}
	if len(unique) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var files []model.File
		if err := tx.Where("uuid IN ?", unique).Find(&files).Error; err != nil {
			return err
		}
		if len(files) != len(unique) {
			return fmt.Errorf("one or more attachments are missing")
		}
		for _, file := range files {
			if file.ThreadID != 0 && file.ThreadID != threadID {
				return fmt.Errorf("attachment %q belongs to another conversation", file.Filename)
			}
		}
		return tx.Model(&model.File{}).
			Where("uuid IN ?", unique).
			Update("thread_id", threadID).Error
	})
}

func (s *GormStore) ListPendingFilesBefore(ctx context.Context, before time.Time) ([]*model.File, error) {
	var files []*model.File
	if err := s.db.WithContext(ctx).
		Where("thread_id = 0 AND conversation_id = 0 AND created_at < ?", before).
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
