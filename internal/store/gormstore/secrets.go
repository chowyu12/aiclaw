package gormstore

import (
	"context"
	"fmt"

	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/secrets"
)

// UseCipher 让这个库之后读写的凭据都经过加密：写进去的是密文，读出来的是明文。
//
// 放在存储层而不是各个调用方：模型服务、搜索引擎、插件配置三处都存凭据，
// 读它们的地方更多（会话取 Key、搜索 MCP、插件启动）。在这一层做，
// 上面每一处拿到的都还是明文，一行不用改，也不可能漏掉一处。
//
// 没设 Cipher 时行为与从前一样（明文），给测试与旧路径用。
func (s *GormStore) UseCipher(c *secrets.Cipher) { s.cipher = c }

// seal 把一个明文换成要落库的形式。
func (s *GormStore) seal(plain string) string {
	if s.cipher == nil {
		return plain
	}
	return s.cipher.Seal(plain)
}

// open 把库里的值换成明文。
//
// 解不开时返回空串而不是报错：报错会让整个模型服务列表都打不开，用户连重填 Key
// 的入口都没有；返回空串的话界面显示「未配置」，用户重填一次就好。
// 原因记进日志——这只在主密钥换了或库被改过时发生，得留个线索。
func (s *GormStore) open(stored, what string) string {
	if s.cipher == nil {
		return stored
	}
	plain, err := s.cipher.Open(stored)
	if err != nil {
		log.WithField("what", what).Warn("凭据解不开，按未配置处理：" + err.Error())
		return ""
	}
	return plain
}

// MigrateSecrets 把库里还是明文的凭据统一换成密文，返回改了几条。
//
// 老版本一直是明文落库，升级上来第一次启动时跑这一遍；之后每次启动再跑也只是
// 扫一遍、一条不改。没设 Cipher 时什么都不做。
func (s *GormStore) MigrateSecrets(ctx context.Context) (int, error) {
	if s.cipher == nil {
		return 0, nil
	}
	changed := 0

	var providers []model.Provider
	if err := s.db.WithContext(ctx).Find(&providers).Error; err != nil {
		return changed, fmt.Errorf("读模型服务失败：%w", err)
	}
	for _, item := range providers {
		if item.APIKey == "" || secrets.Sealed(item.APIKey) {
			continue
		}
		if err := s.db.WithContext(ctx).Model(&model.Provider{}).Where("id = ?", item.ID).
			Update("api_key", s.cipher.Seal(item.APIKey)).Error; err != nil {
			return changed, fmt.Errorf("加密模型服务 %d 的 Key 失败：%w", item.ID, err)
		}
		changed++
	}

	var engines []model.SearchEngineConfig
	if err := s.db.WithContext(ctx).Find(&engines).Error; err != nil {
		return changed, fmt.Errorf("读搜索引擎失败：%w", err)
	}
	for _, item := range engines {
		if item.APIKey == "" || secrets.Sealed(item.APIKey) {
			continue
		}
		if err := s.db.WithContext(ctx).Model(&model.SearchEngineConfig{}).Where("id = ?", item.ID).
			Update("api_key", s.cipher.Seal(item.APIKey)).Error; err != nil {
			return changed, fmt.Errorf("加密搜索引擎 %d 的 Key 失败：%w", item.ID, err)
		}
		changed++
	}

	var configs []model.PluginConfig
	if err := s.db.WithContext(ctx).Where("secret = ?", true).Find(&configs).Error; err != nil {
		return changed, fmt.Errorf("读插件配置失败：%w", err)
	}
	for _, item := range configs {
		if item.Value == "" || secrets.Sealed(item.Value) {
			continue
		}
		if err := s.db.WithContext(ctx).Model(&model.PluginConfig{}).Where("id = ?", item.ID).
			Update("value", s.cipher.Seal(item.Value)).Error; err != nil {
			return changed, fmt.Errorf("加密插件配置 %d 失败：%w", item.ID, err)
		}
		changed++
	}
	if changed > 0 {
		s.scrubFreePages(ctx)
	}
	return changed, nil
}

// scrubFreePages 让被换掉的明文真正从文件里消失。
//
// SQLite 的 UPDATE 只是把旧页标成空闲，字节还在文件里，用 strings 就能翻出来——
// 加密迁移完了原样留着旧明文，等于没加密。VACUUM 重写整个文件，只带上活着的页；
// 先 checkpoint 把 WAL 里的也收进主文件。只对 sqlite 做，别的库没有这个问题的
// 这种形态，也没有这条语句。失败不算错：数据已经是密文了，这一步只是打扫。
func (s *GormStore) scrubFreePages(ctx context.Context) {
	if s.db.Dialector.Name() != "sqlite" {
		return
	}
	if err := s.db.WithContext(ctx).Exec("PRAGMA wal_checkpoint(TRUNCATE)").Error; err != nil {
		log.Warn("迁移后 checkpoint 失败：" + err.Error())
	}
	if err := s.db.WithContext(ctx).Exec("VACUUM").Error; err != nil {
		log.Warn("迁移后 VACUUM 失败，旧明文可能仍留在文件的空闲页里：" + err.Error())
	}
}
