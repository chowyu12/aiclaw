package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chowyu12/aiclaw/internal/config"
)

func TestRunCreatesLocalSQLiteApp(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	workspacePath := filepath.Join(dir, "state")
	cfg := &config.Config{Workspace: workspacePath}
	if err := cfg.Save(configPath); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := Run(Options{ConfigFlag: configPath, Input: strings.NewReader("/help\n/exit\n"), Output: &output}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "AIClaw local app") || !strings.Contains(output.String(), "/provider list") {
		t.Fatalf("unexpected local app output: %s", output.String())
	}
	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Database.Driver != "sqlite" {
		t.Fatalf("database driver = %q, want sqlite", reloaded.Database.Driver)
	}
	if reloaded.Database.DSN != filepath.Join(workspacePath, "aiclaw.db") {
		t.Fatalf("database path = %q", reloaded.Database.DSN)
	}
	if _, err := os.Stat(reloaded.Database.DSN); err != nil {
		t.Fatalf("local database was not created: %v", err)
	}
}
