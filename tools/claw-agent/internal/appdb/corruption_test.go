package appdb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chowyu12/aiclaw/internal/sqlitehealth"
)

func TestOpenPreservesCorruptApplicationDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aiclaw.db")
	original := []byte("damaged application database")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := Open(path)
	if db != nil || !sqlitehealth.IsCorruption(err) || !strings.Contains(err.Error(), sqlitehealth.BackupDir(path)) {
		t.Fatalf("unexpected startup result: %v %v", db, err)
	}
	got, e := os.ReadFile(path)
	if e != nil || string(got) != string(original) {
		t.Fatalf("original was replaced: %q %v", got, e)
	}
}
