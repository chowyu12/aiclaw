package gormstore

import (
	"encoding/json"
	"fmt"
	"strings"

	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"

	"github.com/chowyu12/aiclaw/internal/model"
)

// dropReplacedIndexes 删掉被换掉的旧唯一索引，并先把 connection_id 列加上。
//
// 插件配置与放行记录加了「连接」这一维，唯一索引换成带 connection_id 的新索引。旧索引
// 必须在 AutoMigrate 之前删：它还在的话，第二个连接写同一个字段会撞上旧的唯一约束。
func dropReplacedIndexes(db *gorm.DB) error {
	for _, index := range []struct {
		table any
		name  string
	}{
		{&model.PluginConfig{}, "idx_plugin_config_key"},
		{&model.ChannelBinding{}, "idx_channel_binding_key"},
	} {
		if !db.Migrator().HasTable(index.table) {
			continue
		}
		if db.Migrator().HasIndex(index.table, index.name) {
			if err := db.Migrator().DropIndex(index.table, index.name); err != nil {
				return fmt.Errorf("drop index %s: %w", index.name, err)
			}
		}
		// 新列也在这里先加上：让 AutoMigrate 自己加的话，它会先去建引用这一列的新唯一
		// 索引，旧库上直接报「no such column: connection_id」（升级测试里撞上过）。
		if !db.Migrator().HasColumn(index.table, "ConnectionID") {
			if err := db.Migrator().AddColumn(index.table, "ConnectionID"); err != nil {
				return fmt.Errorf("add connection_id: %w", err)
			}
		}
	}
	return nil
}

// migrateChannelConnections 把升级前「一个渠道插件一套凭据」的数据归到它的第一个连接下。
//
// 早先的配置与放行记录没有 connection_id（是空串）。渠道插件的这些行挪到一个新建的
// 连接上，名字用渠道的显示名（「微信」「企业微信」），原来的会话、放行照常能用。
// 幂等：没有空 connection_id 的行就什么都不做。不带渠道的插件（computer use）的配置
// 本来就是插件级的，不动。
func migrateChannelConnections(db *gorm.DB) error {
	var plugins []model.Plugin
	if err := db.Find(&plugins).Error; err != nil {
		return err
	}
	for _, plugin := range plugins {
		name, isChannel := channelDisplayName(plugin)
		if !isChannel {
			continue
		}
		var configs, bindings int64
		db.Model(&model.PluginConfig{}).Where("plugin_uuid = ? AND connection_id = ?", plugin.UUID, "").Count(&configs)
		db.Model(&model.ChannelBinding{}).Where("plugin_uuid = ? AND connection_id = ?", plugin.UUID, "").Count(&bindings)
		if configs == 0 && bindings == 0 {
			continue
		}
		connection := model.ChannelConnection{UUID: firstConnectionID(plugin.UUID), PluginUUID: plugin.UUID, Name: name}
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Where("uuid = ?", connection.UUID).FirstOrCreate(&connection).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.PluginConfig{}).Where("plugin_uuid = ? AND connection_id = ?", plugin.UUID, "").
				Update("connection_id", connection.UUID).Error; err != nil {
				return err
			}
			return tx.Model(&model.ChannelBinding{}).Where("plugin_uuid = ? AND connection_id = ?", plugin.UUID, "").
				Update("connection_id", connection.UUID).Error
		})
		if err != nil {
			return fmt.Errorf("migrate channel connections of %s: %w", plugin.Name, err)
		}
		log.WithField("plugin", plugin.Name).Info("channel config moved to its first connection")
	}
	return nil
}

// firstConnectionID 是升级迁移出来的那个连接的 id：由插件 uuid 算出，重复跑迁移也是同一个。
func firstConnectionID(pluginUUID string) string {
	compact := strings.ReplaceAll(pluginUUID, "-", "")
	if len(compact) > 12 {
		compact = compact[:12]
	}
	return "k" + compact
}

// channelDisplayName 从 manifest 里读第一个渠道的显示名。不带渠道的插件返回 false。
// 这里只读 JSON 而不引用 plugin 包：plugin 包依赖 store，反过来引用会成环。
func channelDisplayName(plugin model.Plugin) (string, bool) {
	var manifest struct {
		Contributes struct {
			Channels []struct {
				DisplayName string `json:"display_name"`
			} `json:"channels"`
		} `json:"contributes"`
	}
	if len(plugin.Manifest) == 0 || json.Unmarshal(plugin.Manifest, &manifest) != nil || len(manifest.Contributes.Channels) == 0 {
		return "", false
	}
	name := strings.TrimSpace(manifest.Contributes.Channels[0].DisplayName)
	if name == "" {
		name = plugin.Name
	}
	return name, true
}
