package gormstore

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/chowyu12/aiclaw/internal/config"
	"github.com/chowyu12/aiclaw/internal/model"
)

func TestLegacyProviderProtocolMigrationPreservesConfiguration(t *testing.T) {
	cfg := config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "legacy.db")}
	db, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.db.Migrator().DropColumn(&model.Provider{}, "api_protocol"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err = db.db.Migrator().DropColumn(&model.Provider{}, "routing_mode"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err = db.db.Migrator().DropColumn(&model.Provider{}, "revision"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err = db.db.Exec(`INSERT INTO providers(id,name,type,base_url,api_key,models,enabled) VALUES(42,'legacy','gemini','https://fixture.invalid/v1beta/openai','fixture-key','["kept#vision"]',1)`).Error; err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p, err := db.GetProvider(context.Background(), 42)
	if err != nil || p.Revision != 1 || p.RoutingMode != "manual" || p.APIProtocol != model.APIChatCompletions || p.BaseURL != "https://fixture.invalid/v1beta/openai" || p.APIKey != "fixture-key" || p.Type != model.ProviderGemini || !p.Enabled || string(p.Models) != `["kept#vision"]` {
		t.Fatal("legacy provider changed during schema migration", p, err)
	}
}
