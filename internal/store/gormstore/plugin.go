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

// UpsertPlugin writes a plugin record by UUID, used by the bundled plugin sync
// so an upgrade refreshes name, version and manifest in place.
func (s *GormStore) UpsertPlugin(ctx context.Context, plugin *model.Plugin) error {
	if plugin.UUID == "" {
		plugin.UUID = uuid.NewString()
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "uuid"}}, UpdateAll: true}).Create(plugin).Error
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

func (s *GormStore) ListPluginConfig(ctx context.Context, pluginUUID string) ([]model.PluginConfig, error) {
	var items []model.PluginConfig
	if err := s.db.WithContext(ctx).Where("plugin_uuid = ?", pluginUUID).Order("key ASC").Find(&items).Error; err != nil {
		return nil, err
	}
	// 只有 secret 的值加密：普通配置（比如企业微信的 corp id）明文存着，
	// 出问题时用 sqlite3 还能看一眼。
	for index := range items {
		if items[index].Secret {
			items[index].Value = s.open(items[index].Value, "plugin_config "+items[index].Key)
		}
	}
	return items, nil
}

func (s *GormStore) SetPluginConfig(ctx context.Context, item *model.PluginConfig) error {
	stored := *item
	if stored.Secret {
		stored.Value = s.seal(item.Value)
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "plugin_uuid"}, {Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "secret", "updated_at"}),
	}).Create(&stored).Error
}

func (s *GormStore) DeletePluginConfig(ctx context.Context, pluginUUID, key string) error {
	return s.db.WithContext(ctx).Where("plugin_uuid = ? AND key = ?", pluginUUID, key).Delete(&model.PluginConfig{}).Error
}

// DeletePluginConfigs removes every stored value of a plugin. Secrets must not
// outlive the bundle they belong to.
func (s *GormStore) DeletePluginConfigs(ctx context.Context, pluginUUID string) error {
	return s.db.WithContext(ctx).Where("plugin_uuid = ?", pluginUUID).Delete(&model.PluginConfig{}).Error
}
