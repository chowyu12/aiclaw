// Package appdb 打开 AIClaw 沿用至今的应用库（SQLite）。
//
// 模型服务、插件、插件配置、通道授权都在这一个库里；providers 与 pluginhost
// 两个包共用同一个连接，由 server 打开与关闭。
package appdb

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/chowyu12/aiclaw/internal/config"
	"github.com/chowyu12/aiclaw/internal/store/gormstore"
)

// Open 打开（必要时建出）path 处的库并跑迁移。
func Open(path string) (*gormstore.GormStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("创建应用数据目录失败：%w", err)
	}
	db, err := gormstore.New(config.DatabaseConfig{Driver: "sqlite", DSN: path})
	if err != nil {
		return nil, fmt.Errorf("打开应用库失败：%w", err)
	}
	return db, nil
}
