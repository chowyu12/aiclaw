package gormstore

import (
	"context"
	"database/sql"
	"time"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func (s *GormStore) CreateProject(ctx context.Context, project *model.Project) error {
	if project.UUID == "" {
		project.UUID = uuid.NewString()
	}
	return s.db.WithContext(ctx).Create(project).Error
}

func (s *GormStore) ListProjects(ctx context.Context, userID string) ([]model.Project, error) {
	var projects []model.Project
	db := s.db.WithContext(ctx).Order("updated_at DESC")
	if userID != "" {
		db = db.Where("user_id = ?", userID)
	}
	return projects, db.Find(&projects).Error
}

func (s *GormStore) DeleteProject(ctx context.Context, projectUUID string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Detach and archive conversations before deleting their project. Besides
		// avoiding dangling project UUIDs, this order remains valid when an
		// existing database enables foreign-key enforcement.
		now := time.Now()
		if err := tx.Model(&model.Thread{}).
			Where("project_uuid = ?", projectUUID).
			Updates(map[string]any{
				"project_uuid": "",
				"status":       model.ThreadStatusArchived,
				"archived_at":  now,
			}).Error; err != nil {
			return err
		}

		result := tx.Where("uuid = ?", projectUUID).Delete(&model.Project{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return sql.ErrNoRows
		}
		return nil
	})
}
