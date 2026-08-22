package gormstore

import (
	"context"
	"database/sql"

	"github.com/google/uuid"
	"gorm.io/gorm/clause"

	"github.com/chowyu12/aiclaw/internal/model"
)

func (s *GormStore) ListSkills(ctx context.Context) ([]model.Skill, error) {
	var items []model.Skill
	return items, s.db.WithContext(ctx).Order("name ASC").Find(&items).Error
}

func (s *GormStore) UpsertSkill(ctx context.Context, skill *model.Skill) error {
	if skill.UUID == "" {
		skill.UUID = uuid.NewString()
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "uuid"}}, UpdateAll: true}).Create(skill).Error
}

func (s *GormStore) SetSkillEnabled(ctx context.Context, skillUUID string, enabled bool) error {
	return s.db.WithContext(ctx).Model(&model.Skill{}).Where("uuid = ?", skillUUID).Update("enabled", enabled).Error
}

func (s *GormStore) SetPluginSkillsEnabled(ctx context.Context, pluginUUID string, enabled bool) error {
	return s.db.WithContext(ctx).Model(&model.Skill{}).Where("plugin_uuid = ?", pluginUUID).Update("enabled", enabled).Error
}

func (s *GormStore) DeletePluginSkills(ctx context.Context, pluginUUID string) error {
	return s.db.WithContext(ctx).Where("plugin_uuid = ?", pluginUUID).Delete(&model.Skill{}).Error
}

func (s *GormStore) ListPlugins(ctx context.Context) ([]model.Plugin, error) {
	var items []model.Plugin
	return items, s.db.WithContext(ctx).Order("name ASC").Find(&items).Error
}

func (s *GormStore) CreatePlugin(ctx context.Context, plugin *model.Plugin) error {
	if plugin.UUID == "" {
		plugin.UUID = uuid.NewString()
	}
	return s.db.WithContext(ctx).Create(plugin).Error
}

func (s *GormStore) SetPluginEnabled(ctx context.Context, pluginUUID string, enabled bool) error {
	return s.db.WithContext(ctx).Model(&model.Plugin{}).Where("uuid = ?", pluginUUID).Update("enabled", enabled).Error
}

func (s *GormStore) DeletePlugin(ctx context.Context, pluginUUID string) error {
	result := s.db.WithContext(ctx).Where("uuid = ?", pluginUUID).Delete(&model.Plugin{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return sql.ErrNoRows
	}
	return nil
}
