package appserver

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/chowyu12/aiclaw/internal/config"
	"github.com/chowyu12/aiclaw/internal/protocol"
	"github.com/chowyu12/aiclaw/internal/store/gormstore"
)

func TestCreateThreadUsesDirectModelProfile(t *testing.T) {
	ctx := context.Background()
	store, err := gormstore.New(config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "appserver.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	service := New(store, nil, nil)
	var threadID string
	err = service.Handle(ctx, protocol.Command{
		Kind: protocol.CommandCreateThread, UserID: "local", ProjectUUID: "project-1",
		ProviderID: 7, ModelName: "model-7", SearchEngineID: 3, Input: "hello",
	}, func(event protocol.Event) error {
		threadID = event.ThreadID
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	thread, err := store.GetThreadByUUID(ctx, threadID, false)
	if err != nil {
		t.Fatal(err)
	}
	if thread.AgentUUID != "" || thread.ProviderID != 7 || thread.ModelName != "model-7" || thread.SearchEngineID != 3 || thread.ProjectUUID != "project-1" {
		t.Fatalf("thread did not preserve direct model profile: %+v", thread)
	}
}
