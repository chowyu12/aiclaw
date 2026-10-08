package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chowyu12/aiclaw/internal/sqlitehealth"
)

func TestOpenPreservesCorruptSessionDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.db")
	original := []byte("damaged session database")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := Open(dir)
	if db != nil || !sqlitehealth.IsCorruption(err) || !strings.Contains(err.Error(), sqlitehealth.BackupDir(path)) {
		t.Fatalf("unexpected startup result: %v %v", db, err)
	}
	got, e := os.ReadFile(path)
	if e != nil || string(got) != string(original) {
		t.Fatalf("original was replaced: %q %v", got, e)
	}
}
