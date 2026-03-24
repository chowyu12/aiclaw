package gormstore

import (
	"context"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/chowyu12/aiclaw/internal/model"
)

func (s *GormStore) CreateConversation(ctx context.Context, c *model.Conversation) error {
	if c.UUID == "" {
		c.UUID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(c).Error
}

func (s *GormStore) GetConversation(ctx context.Context, id int64) (*model.Conversation, error) {
	var c model.Conversation
	if err := s.db.WithContext(ctx).First(&c, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &c, nil
}

func (s *GormStore) GetConversationByUUID(ctx context.Context, uid string) (*model.Conversation, error) {
	var c model.Conversation
	if err := s.db.WithContext(ctx).Where("uuid = ?", uid).First(&c).Error; err != nil {
		return nil, notFound(err)
	}
	return &c, nil
}

func (s *GormStore) ListConversations(ctx context.Context, userID string, q model.ListQuery) ([]*model.Conversation, int64, error) {
	var items []*model.Conversation
	var total int64

	db := s.db.WithContext(ctx).Model(&model.Conversation{})
	if userID != "" {
		db = db.Where("user_id = ?", userID)
	}
	if q.Keyword != "" {
		db = db.Where("title LIKE ?", "%"+q.Keyword+"%")
	}
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(q)
	if err := db.Order("updated_at DESC").Offset(offset).Limit(limit).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (s *GormStore) ListConversationsByUserPrefix(ctx context.Context, prefix string, q model.ListQuery) ([]*model.Conversation, int64, error) {
	var items []*model.Conversation
	var total int64

	db := s.db.WithContext(ctx).Model(&model.Conversation{}).Where("user_id LIKE ?", prefix+"%")
	if q.Keyword != "" {
		db = db.Where("title LIKE ?", "%"+q.Keyword+"%")
	}
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(q)
	if err := db.Order("updated_at DESC").Offset(offset).Limit(limit).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (s *GormStore) UpdateConversationTitle(ctx context.Context, id int64, title string) error {
	return s.db.WithContext(ctx).Model(&model.Conversation{}).Where("id = ?", id).Update("title", title).Error
}

func (s *GormStore) DeleteConversation(ctx context.Context, id int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tx.Where("conversation_id = ?", id).Delete(&model.File{})
		tx.Where("conversation_id = ?", id).Delete(&model.ExecutionStep{})
		tx.Where("conversation_id = ?", id).Delete(&model.Message{})
		return tx.Delete(&model.Conversation{}, id).Error
	})
}

func (s *GormStore) CreateMessage(ctx context.Context, m *model.Message) error {
	return s.db.WithContext(ctx).Create(m).Error
}

func (s *GormStore) CreateMessages(ctx context.Context, msgs []*model.Message) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, m := range msgs {
			if err := tx.Create(m).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *GormStore) CountMessages(ctx context.Context, conversationID int64) (int64, error) {
	var count int64
	if err := s.db.WithContext(ctx).
		Model(&model.Message{}).
		Where("conversation_id = ? AND role = ?", conversationID, "user").
		Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func (s *GormStore) ListMessages(ctx context.Context, conversationID int64, maxTurns int) ([]model.Message, error) {
	var userMsgIDs []int64
	if err := s.db.WithContext(ctx).
		Model(&model.Message{}).
		Where("conversation_id = ? AND role = ?", conversationID, "user").
		Order("id DESC").
		Limit(maxTurns).
		Pluck("id", &userMsgIDs).Error; err != nil {
		return nil, err
	}
	if len(userMsgIDs) == 0 {
		return nil, nil
	}

	var msgs []model.Message
	if err := s.db.WithContext(ctx).
		Where("conversation_id = ? AND id >= ?", conversationID, userMsgIDs[len(userMsgIDs)-1]).
		Order("id ASC").
		Find(&msgs).Error; err != nil {
		return nil, err
	}
	return msgs, nil
}

func (s *GormStore) CreateExecutionStep(ctx context.Context, step *model.ExecutionStep) error {
	return s.db.WithContext(ctx).Create(step).Error
}

func (s *GormStore) UpdateStepsMessageID(ctx context.Context, conversationID, messageID int64) error {
	return s.db.WithContext(ctx).
		Model(&model.ExecutionStep{}).
		Where("conversation_id = ? AND message_id = 0", conversationID).
		Update("message_id", messageID).Error
}

func (s *GormStore) ListExecutionSteps(ctx context.Context, messageID int64) ([]model.ExecutionStep, error) {
	var steps []model.ExecutionStep
	if err := s.db.WithContext(ctx).
		Where("message_id = ?", messageID).
		Order("step_order ASC").
		Find(&steps).Error; err != nil {
		return nil, err
	}
	return steps, nil
}

func (s *GormStore) ListExecutionStepsByConversation(ctx context.Context, conversationID int64) ([]model.ExecutionStep, error) {
	var steps []model.ExecutionStep
	if err := s.db.WithContext(ctx).
		Where("conversation_id = ?", conversationID).
		Order("id ASC").
		Find(&steps).Error; err != nil {
		return nil, err
	}
	return steps, nil
}
