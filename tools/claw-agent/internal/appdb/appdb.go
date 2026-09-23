// Package appdb 打开 AIClaw 沿用至今的应用库（SQLite）。
//
// 模型服务、插件、插件配置、通道授权都在这一个库里；providers 与 pluginhost
// 两个包共用同一个连接，由 server 打开与关闭。
package appdb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chowyu12/aiclaw/internal/config"
	"github.com/chowyu12/aiclaw/internal/secrets"
	"github.com/chowyu12/aiclaw/internal/store/gormstore"
)

// Open 打开（必要时建出）path 处的库并跑迁移，凭据列加密。
//
// 主密钥在库旁边的 secret.key（或环境变量 AICLAW_MASTER_KEY），没有就生成。
// 老库里的明文 Key 在这里一次性换成密文——旧版一直是明文落库，升级上来
// 第一次启动就把它们收进去。
func Open(path string) (*gormstore.GormStore, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建应用数据目录失败：%w", err)
	}
	cipher, err := secrets.Load(dir)
	if err != nil {
		return nil, fmt.Errorf("准备凭据密钥失败：%w", err)
	}
	db, err := gormstore.New(config.DatabaseConfig{Driver: "sqlite", DSN: path})
	if err != nil {
		return nil, fmt.Errorf("打开应用库失败：%w", err)
	}
	db.UseCipher(cipher)
	if _, err := db.MigrateSecrets(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("加密库里已有的凭据失败：%w", err)
	}
	return db, nil
}

// KeyFile 是 path 那个库对应的密钥文件。沙箱要把它和库一起禁读。
func KeyFile(path string) string {
	return filepath.Join(filepath.Dir(path), secrets.KeyFile)
}
