package sqlitehealth

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/glebarez/go-sqlite"
)

func TestCheckHealthyAndCancelled(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "healthy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE test (value TEXT)"); err != nil {
		t.Fatal(err)
	}
	if err := Check(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Check(ctx, db); err != nil {
		t.Fatalf("cancelled check reported damage: %v", err)
	}
}

func TestCorruptionPreservesOriginalAndJournals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "damaged.db")
	contents := map[string]string{"": "this is not a sqlite database", "-wal": "wal contents", "-shm": "shm contents", "-journal": "journal contents"}
	// Detect with no fake journals attached, then verify preservation separately.
	if err := os.WriteFile(path, []byte(contents[""]), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	err = Check(context.Background(), db)
	db.Close()
	if !IsCorruption(err) {
		t.Fatalf("expected corruption, got %v", err)
	}
	for suffix, content := range contents {
		if err := os.WriteFile(path+suffix, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	preserved := Preserve(path, err)
	entries, e := os.ReadDir(BackupDir(path))
	if e != nil || len(entries) != 1 {
		t.Fatalf("backup entries: %v %v", entries, e)
	}
	dir := filepath.Join(BackupDir(path), entries[0].Name())
	if !strings.Contains(preserved.Error(), dir) || !IsCorruption(preserved) {
		t.Fatalf("missing backup diagnostic: %v", preserved)
	}
	for suffix, content := range contents {
		for _, file := range []string{path + suffix, filepath.Join(dir, filepath.Base(path)+suffix)} {
			got, err := os.ReadFile(file)
			if err != nil || string(got) != content {
				t.Fatalf("%s: %q %v", file, got, err)
			}
		}
		info, err := os.Stat(filepath.Join(dir, filepath.Base(path)+suffix))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("backup permissions: %v %v", info, err)
		}
	}
}

func TestNonCorruptionDoesNotCreateBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	err := errors.New("permission denied")
	if Preserve(path, err) != err {
		t.Fatal("changed unrelated error")
	}
	if _, err := os.Stat(BackupDir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}

func TestCheckDetectsDamagedPageWithValidHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "damaged-page.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE test (value TEXT); INSERT INTO test VALUES ('preserve me')"); err != nil {
		t.Fatal(err)
	}
	var page, size int
	if err := db.QueryRow("SELECT rootpage FROM sqlite_master WHERE name = 'test'").Scan(&page); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("PRAGMA page_size").Scan(&size); err != nil {
		t.Fatal(err)
	}
	db.Close()
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteAt([]byte{0xff}, int64((page-1)*size))
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Fatalf("must open successfully: %v", err)
	}
	if err := Check(context.Background(), db); !IsCorruption(err) {
		t.Fatalf("did not detect damaged page: %v", err)
	}
}

func TestLockedDatabaseIsNotCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "locked.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE test (value TEXT); BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	defer db.Exec("ROLLBACK")
	reader, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	reader.SetMaxOpenConns(1)
	started := time.Now()
	if err := Check(context.Background(), reader); err != nil {
		t.Fatalf("locked check reported corruption: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("integrity check waited for the driver's five-second busy timeout")
	}
	var timeout int
	if err := reader.QueryRow("PRAGMA busy_timeout").Scan(&timeout); err != nil || timeout != 5000 {
		t.Fatalf("busy timeout was not restored: %d %v", timeout, err)
	}
}
