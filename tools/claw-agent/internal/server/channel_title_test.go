package server

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/chowyu12/aiclaw/internal/config"
	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/store/gormstore"
)

// 通道会话的标题：连接名 · 对方。好几个连接同时在用时，侧边栏里看得出是哪个连接上的谁；
// 对方的名字已经以连接名开头（升级迁移出来的「企业微信」连接）就不重复。
func TestChannelSessionTitleCarriesTheConnection(t *testing.T) {
	db, err := gormstore.New(config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "app.db")})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, connection := range []model.ChannelConnection{
		{UUID: "k1", PluginUUID: "p", Name: "企业微信"},
		{UUID: "k2", PluginUUID: "p", Name: "客服机器人"},
	} {
		connection := connection
		if err := db.CreateChannelConnection(ctx, &connection); err != nil {
			t.Fatal(err)
		}
	}
	gateway := &channelGateway{server: &Server{appDB: db}}
	cases := map[string]string{
		"k1":      "企业微信 张三",
		"k2":      "客服机器人 · 企业微信 张三",
		"missing": "企业微信 张三",
	}
	for connection, want := range cases {
		got := gateway.sessionTitle(ctx, &model.ChannelBinding{PluginUUID: "p", ConnectionID: connection, DisplayName: "企业微信 张三"})
		if got != want {
			t.Errorf("connection %s: title = %q, want %q", connection, got, want)
		}
	}
}
