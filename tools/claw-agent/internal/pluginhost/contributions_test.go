package pluginhost

import (
	"path/filepath"
	"testing"

	"github.com/chowyu12/aiclaw/internal/config"
	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/store/gormstore"
)

func TestContributionsReportDuplicateMCPNames(t *testing.T) {
	ctx := t.Context()
	db, err := gormstore.New(config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "app.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, id := range []string{"first", "second"} {
		if err := db.UpsertPlugin(ctx, &model.Plugin{UUID: id, Name: id, Enabled: true}); err != nil {
			t.Fatal(err)
		}
		if err := db.UpsertMCPServer(ctx, &model.MCPServer{
			UUID: id + "-mcp", PluginUUID: id, Name: "shared", Enabled: true,
			Transport: model.MCPTransportStdio, Endpoint: id,
		}); err != nil {
			t.Fatal(err)
		}
	}
	service := &Service{db: db}
	got, err := service.Contributions(ctx)
	if err != nil || len(got.MCPConflicts) != 1 || got.MCPConflicts[0] != "shared" || got.MCPServers["shared"].Command != "first" {
		t.Fatalf("contributions = %+v, %v", got, err)
	}
	if err := db.SetPluginEnabled(ctx, "first", false); err != nil {
		t.Fatal(err)
	}
	got, err = service.Contributions(ctx)
	if err != nil || len(got.MCPConflicts) != 0 || got.MCPServers["shared"].Command != "second" {
		t.Fatalf("contributions after disabling the conflict = %+v, %v", got, err)
	}
}
