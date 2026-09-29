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

// ListPluginConfig 列出一个插件在某个连接下的配置。connectionID 为空是插件本身的配置。
func (s *GormStore) ListPluginConfig(ctx context.Context, pluginUUID, connectionID string) ([]model.PluginConfig, error) {
	var items []model.PluginConfig
	if err := s.db.WithContext(ctx).Where("plugin_uuid = ? AND connection_id = ?", pluginUUID, connectionID).
		Order("key ASC").Find(&items).Error; err != nil {
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
		Columns:   []clause.Column{{Name: "plugin_uuid"}, {Name: "connection_id"}, {Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "secret", "updated_at"}),
	}).Create(&stored).Error
}

func (s *GormStore) DeletePluginConfig(ctx context.Context, pluginUUID, connectionID, key string) error {
	return s.db.WithContext(ctx).Where("plugin_uuid = ? AND connection_id = ? AND key = ?", pluginUUID, connectionID, key).
		Delete(&model.PluginConfig{}).Error
}

// DeleteConnectionConfig 删掉一个连接的全部配置。连接删了，它的凭据不该留下。
func (s *GormStore) DeleteConnectionConfig(ctx context.Context, pluginUUID, connectionID string) error {
	return s.db.WithContext(ctx).Where("plugin_uuid = ? AND connection_id = ?", pluginUUID, connectionID).
		Delete(&model.PluginConfig{}).Error
}

// ListChannelConnections 列出一个插件的连接，按创建先后。pluginUUID 为空列全部。
func (s *GormStore) ListChannelConnections(ctx context.Context, pluginUUID string) ([]model.ChannelConnection, error) {
	var items []model.ChannelConnection
	query := s.db.WithContext(ctx).Order("id ASC")
	if pluginUUID != "" {
		query = query.Where("plugin_uuid = ?", pluginUUID)
	}
	return items, query.Find(&items).Error
}

func (s *GormStore) CreateChannelConnection(ctx context.Context, item *model.ChannelConnection) error {
	return s.db.WithContext(ctx).Create(item).Error
}

func (s *GormStore) RenameChannelConnection(ctx context.Context, uuid, name string) error {
	return s.db.WithContext(ctx).Model(&model.ChannelConnection{}).Where("uuid = ?", uuid).Update("name", name).Error
}

func (s *GormStore) DeleteChannelConnection(ctx context.Context, uuid string) error {
	return s.db.WithContext(ctx).Where("uuid = ?", uuid).Delete(&model.ChannelConnection{}).Error
}

// DeletePluginConfigs removes every stored value of a plugin. Secrets must not
// outlive the bundle they belong to.
func (s *GormStore) DeletePluginConfigs(ctx context.Context, pluginUUID string) error {
	if err := s.db.WithContext(ctx).Where("plugin_uuid = ?", pluginUUID).Delete(&model.ChannelConnection{}).Error; err != nil {
		return err
	}
	return s.db.WithContext(ctx).Where("plugin_uuid = ?", pluginUUID).Delete(&model.PluginConfig{}).Error
}
