package gormstore

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	sqliteDriver "github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/chowyu12/aiclaw/internal/config"
	"github.com/chowyu12/aiclaw/internal/model"
)

// 升级：旧库里渠道插件只有一套凭据（没有 connection_id，旧唯一索引只看 plugin_uuid+key）。
// 打开之后要：旧索引删掉、那套凭据与放行记录归到新建的第一个连接下、第二个连接能写
// 同一个键；再开一次不会重复建连接。
func TestUpgradeMovesSingleCredentialSetIntoFirstConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	// 旧库按升级前的结构体建（字段与索引标签原样照抄 3.6.3 的 model），这样和用户
	// 手里真实的旧库是同一个样子：没有 connection_id，唯一索引只看旧的那几列。
	legacy, err := gorm.Open(sqliteDriver.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.AutoMigrate(&model.Plugin{}); err != nil {
		t.Fatal(err)
	}
	for _, plugin := range []model.Plugin{
		{UUID: "11111111-2222-3333-4444-555555555555", Name: "WeCom", InstallDir: "/tmp", Enabled: true,
			Manifest: model.JSON(`{"contributes":{"channels":[{"id":"wecom","display_name":"企业微信"}]}}`)},
		{UUID: "99999999-2222-3333-4444-555555555555", Name: "Computer Use", InstallDir: "/tmp", Enabled: true,
			Manifest: model.JSON(`{"contributes":{"tools":[{"provider":"builtin:computer_use"}]}}`)},
	} {
		if err := legacy.Create(&plugin).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := legacy.AutoMigrate(&legacyPluginConfig{}, &legacyChannelBinding{}); err != nil {
		t.Fatal(err)
	}
	for _, item := range []legacyPluginConfig{
		{PluginUUID: "11111111-2222-3333-4444-555555555555", Key: "bot_id", Value: "bot-1"},
		{PluginUUID: "99999999-2222-3333-4444-555555555555", Key: "scale", Value: "2"},
	} {
		if err := legacy.Create(&item).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := legacy.Create(&legacyChannelBinding{
		PluginUUID: "11111111-2222-3333-4444-555555555555", ChannelID: "wecom", ExternalKey: "zhangsan",
		DisplayName: "企业微信 张三", ThreadUUID: "c_1", Allowed: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if raw, err := legacy.DB(); err == nil {
		raw.Close()
	}

	store, err := New(config.DatabaseConfig{Driver: "sqlite", DSN: path})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const wecom = "11111111-2222-3333-4444-555555555555"

	connections, err := store.ListChannelConnections(ctx, wecom)
	if err != nil || len(connections) != 1 || connections[0].Name != "企业微信" {
		t.Fatalf("the old credential set should become one connection named after the channel: %+v %v", connections, err)
	}
	first := connections[0].UUID
	if items, _ := store.ListPluginConfig(ctx, wecom, first); len(items) != 1 || items[0].Value != "bot-1" {
		t.Errorf("the old config should now belong to the first connection: %+v", items)
	}
	if binding, _ := store.GetChannelBinding(ctx, wecom, "wecom", first, "zhangsan"); binding == nil || binding.ThreadUUID != "c_1" || !binding.Allowed {
		t.Errorf("the old binding (and its session) should carry over to the first connection: %+v", binding)
	}
	// 不带渠道的插件的配置本来就是插件级的，不动。
	if items, _ := store.ListPluginConfig(ctx, "99999999-2222-3333-4444-555555555555", ""); len(items) != 1 {
		t.Errorf("a non-channel plugin's config must stay plugin-level: %+v", items)
	}
	// 旧唯一索引删掉了：第二个连接能写同一个键。
	if err := store.SetPluginConfig(ctx, &model.PluginConfig{PluginUUID: wecom, ConnectionID: "k2", Key: "bot_id", Value: "bot-2"}); err != nil {
		t.Fatalf("a second connection could not store the same key (old unique index still there?): %v", err)
	}
	if items, _ := store.ListPluginConfig(ctx, wecom, first); len(items) != 1 || items[0].Value != "bot-1" {
		t.Errorf("writing the second connection must not touch the first: %+v", items)
	}

	// 再开一次：幂等，不重复建连接。
	if err := migrateChannelConnections(store.db); err != nil {
		t.Fatal(err)
	}
	if connections, _ := store.ListChannelConnections(ctx, wecom); len(connections) != 1 {
		t.Errorf("running the migration again must not add connections: %+v", connections)
	}
}

// legacyPluginConfig 是 3.6.3 的 model.PluginConfig（没有 ConnectionID）。
type legacyPluginConfig struct {
	ID         int64  `gorm:"primaryKey;autoIncrement"`
	PluginUUID string `gorm:"size:36;not null;uniqueIndex:idx_plugin_config_key"`
	Key        string `gorm:"size:200;not null;uniqueIndex:idx_plugin_config_key"`
	Value      string `gorm:"type:text"`
	Secret     bool   `gorm:"not null"`
	UpdatedAt  time.Time
}

func (legacyPluginConfig) TableName() string { return "plugin_configs" }

// legacyChannelBinding 是 3.6.3 的 model.ChannelBinding（没有 ConnectionID）。
type legacyChannelBinding struct {
	ID           int64  `gorm:"primaryKey;autoIncrement"`
	PluginUUID   string `gorm:"size:36;not null;uniqueIndex:idx_channel_binding_key"`
	ChannelID    string `gorm:"size:100;not null;uniqueIndex:idx_channel_binding_key"`
	ExternalKey  string `gorm:"size:200;not null;uniqueIndex:idx_channel_binding_key"`
	DisplayName  string `gorm:"size:200"`
	ThreadUUID   string `gorm:"size:36;index"`
	ProviderID   int64
	ModelName    string     `gorm:"size:200"`
	Allowed      bool       `gorm:"not null"`
	AllowedTools model.JSON `gorm:"type:text"`
	LastMessage  time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (legacyChannelBinding) TableName() string { return "channel_bindings" }
