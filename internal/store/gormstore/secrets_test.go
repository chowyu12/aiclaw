package gormstore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chowyu12/aiclaw/internal/config"
	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/secrets"
)

func encryptedStore(t *testing.T) *GormStore {
	t.Helper()
	store, err := New(config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "app.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	cipher, err := secrets.New([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	store.UseCipher(cipher)
	return store
}

// rawColumn 绕过 store 直接读一列，看落库的到底是什么。
func rawColumn(t *testing.T, store *GormStore, table, column string, id int64) string {
	t.Helper()
	var value string
	if err := store.db.Raw("SELECT "+column+" FROM "+table+" WHERE id = ?", id).Scan(&value).Error; err != nil {
		t.Fatal(err)
	}
	return value
}

func TestProviderKeyIsEncryptedAtRestAndPlainOnRead(t *testing.T) {
	store := encryptedStore(t)
	ctx := context.Background()
	item := &model.Provider{Name: "p", Type: model.ProviderOpenAICompat, BaseURL: "https://x/v1", APIKey: "sk-plain", Enabled: true}
	if err := store.CreateProvider(ctx, item); err != nil {
		t.Fatal(err)
	}
	if item.APIKey != "sk-plain" {
		t.Errorf("调用方手里的结构体不该被改成密文：%q", item.APIKey)
	}
	raw := rawColumn(t, store, "providers", "api_key", item.ID)
	if !secrets.Sealed(raw) || strings.Contains(raw, "sk-plain") {
		t.Fatalf("库里应是密文：%q", raw)
	}
	got, err := store.GetProvider(ctx, item.ID)
	if err != nil || got.APIKey != "sk-plain" {
		t.Errorf("Get 应解出明文：%q %v", got.APIKey, err)
	}
	listed, _, err := store.ListProviders(ctx, model.ListQuery{})
	if err != nil || listed[0].APIKey != "sk-plain" {
		t.Errorf("List 应解出明文：%v", err)
	}

	next := "sk-rotated"
	if err := store.UpdateProvider(ctx, item.ID, model.UpdateProviderReq{APIKey: &next}); err != nil {
		t.Fatal(err)
	}
	if raw := rawColumn(t, store, "providers", "api_key", item.ID); !secrets.Sealed(raw) {
		t.Errorf("Update 后库里应是密文：%q", raw)
	}
	got, _ = store.GetProvider(ctx, item.ID)
	if got.APIKey != "sk-rotated" {
		t.Errorf("Update 后应读到新 Key：%q", got.APIKey)
	}
	// 清掉 Key：存空串，不存一段「空的密文」。
	empty := ""
	_ = store.UpdateProvider(ctx, item.ID, model.UpdateProviderReq{APIKey: &empty})
	if raw := rawColumn(t, store, "providers", "api_key", item.ID); raw != "" {
		t.Errorf("清掉的 Key 应存成空串：%q", raw)
	}
}

func TestSearchEngineAndPluginSecretsAreEncrypted(t *testing.T) {
	store := encryptedStore(t)
	ctx := context.Background()
	engine := &model.SearchEngineConfig{Provider: "tavily", Name: "t", APIKey: "tvly-plain", Enabled: true}
	if err := store.CreateSearchEngineConfig(ctx, engine); err != nil {
		t.Fatal(err)
	}
	if raw := rawColumn(t, store, "search_engine_configs", "api_key", engine.ID); !secrets.Sealed(raw) {
		t.Errorf("搜索引擎 Key 应加密：%q", raw)
	}
	engine.Name = "renamed"
	if err := store.UpdateSearchEngineConfig(ctx, engine.ID, engine); err != nil {
		t.Fatal(err)
	}
	if raw := rawColumn(t, store, "search_engine_configs", "api_key", engine.ID); !secrets.Sealed(raw) {
		t.Errorf("Save 整个结构体也不该把明文写回去：%q", raw)
	}
	got, _ := store.GetSearchEngineConfig(ctx, engine.ID)
	if got.APIKey != "tvly-plain" || got.Name != "renamed" {
		t.Errorf("读回来不对：%+v", got)
	}

	_ = store.SetPluginConfig(ctx, &model.PluginConfig{PluginUUID: "u", Key: "secret_token", Value: "tok", Secret: true})
	_ = store.SetPluginConfig(ctx, &model.PluginConfig{PluginUUID: "u", Key: "corp_id", Value: "ww123", Secret: false})
	items, err := store.ListPluginConfig(ctx, "u")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		raw := rawColumn(t, store, "plugin_configs", "value", item.ID)
		switch item.Key {
		case "secret_token":
			if !secrets.Sealed(raw) || item.Value != "tok" {
				t.Errorf("secret 配置应加密且读出明文：raw=%q value=%q", raw, item.Value)
			}
		case "corp_id":
			if raw != "ww123" || item.Value != "ww123" {
				t.Errorf("普通配置应保持明文：raw=%q value=%q", raw, item.Value)
			}
		}
	}
}

func TestMigrateSecretsSealsLegacyPlaintextOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	store, err := New(config.DatabaseConfig{Driver: "sqlite", DSN: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	cipher, _ := secrets.New([]byte(strings.Repeat("k", 32)))
	store.UseCipher(cipher)
	ctx := context.Background()
	// 老版本写进去的明文：绕过 store 直接插。
	if err := store.db.Exec(`INSERT INTO providers (name, type, base_url, api_key, enabled, created_at, updated_at)
		VALUES ('old', 'qwen', 'https://x', 'sk-legacy', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Exec(`INSERT INTO plugin_configs (plugin_uuid, key, value, secret, updated_at)
		VALUES ('u', 'secret', 'old-secret', 1, CURRENT_TIMESTAMP), ('u', 'plain', 'keep', 0, CURRENT_TIMESTAMP)`).Error; err != nil {
		t.Fatal(err)
	}
	changed, err := store.MigrateSecrets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if changed != 2 {
		t.Errorf("应改 2 条（一个 Key、一个 secret 配置），实际 %d", changed)
	}
	if raw := rawColumn(t, store, "providers", "api_key", 1); !secrets.Sealed(raw) {
		t.Errorf("迁移后应是密文：%q", raw)
	}
	got, _ := store.GetProvider(ctx, 1)
	if got.APIKey != "sk-legacy" {
		t.Errorf("迁移后仍应读到原 Key：%q", got.APIKey)
	}
	var plain string
	_ = store.db.Raw("SELECT value FROM plugin_configs WHERE key = 'plain'").Scan(&plain).Error
	if plain != "keep" {
		t.Errorf("非 secret 配置不该动：%q", plain)
	}
	// 再跑一遍什么都不改。
	if changed, _ := store.MigrateSecrets(ctx); changed != 0 {
		t.Errorf("第二遍不该再改：%d", changed)
	}
	// 旧明文不能还躺在文件的空闲页里——那样 strings 一翻就出来，等于没加密。
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"sk-legacy", "old-secret"} {
		if strings.Contains(string(raw), leaked) {
			t.Errorf("迁移后文件里仍有明文 %q", leaked)
		}
	}
}

func TestUnreadableSecretReadsAsUnsetInsteadOfFailing(t *testing.T) {
	store := encryptedStore(t)
	ctx := context.Background()
	if err := store.db.Exec(`INSERT INTO providers (name, type, base_url, api_key, enabled, created_at, updated_at)
		VALUES ('p', 'qwen', 'https://x', 'enc:v1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`).Error; err != nil {
		t.Fatal(err)
	}
	got, err := store.GetProvider(ctx, 1)
	if err != nil {
		t.Fatalf("解不开不该让整条记录读不出来：%v", err)
	}
	if got.APIKey != "" {
		t.Errorf("解不开应按未配置处理：%q", got.APIKey)
	}
}
