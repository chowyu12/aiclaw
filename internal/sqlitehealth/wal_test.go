package sqlitehealth

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func openWAL(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0; CREATE TABLE IF NOT EXISTS entries(id INTEGER PRIMARY KEY,value TEXT)"); err != nil {
		t.Fatal(err)
	}
	return db
}
func TestApplicationSQLiteVersionAndConcurrentWALReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.db")
	db := openWAL(t, path)
	var version string
	if err := db.QueryRow("SELECT sqlite_version()").Scan(&version); err != nil {
		t.Fatal(err)
	}
	var major, minor, patch int
	if _, err := fmt.Sscanf(version, "%d.%d.%d", &major, &minor, &patch); err != nil || major < 3 || major == 3 && minor < 54 {
		t.Fatal("app driver lacks WAL fixes", version, err)
	}
	if _, err := db.Exec("INSERT INTO entries(value) VALUES('initial')"); err != nil {
		t.Fatal(err)
	}
	reader, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err = reader.QueryRow("SELECT count(*) FROM entries").Scan(&count); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 30; n++ {
				if _, err := db.Exec("INSERT INTO entries(value) VALUES(?)", strings.Repeat("x", 5000)); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if err = reader.QueryRow("SELECT count(*) FROM entries").Scan(&count); err != nil || count != 1 {
		t.Fatal("reader snapshot changed", count, err)
	}
	if err = reader.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db = openWAL(t, path)
	defer db.Close()
	if err = Check(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT count(*) FROM entries").Scan(&count); err != nil || count != 121 {
		t.Fatal(count, err)
	}
}
func TestWALSurvivesAbruptProcessExit(t *testing.T) {
	if path := os.Getenv("AICLAW_WAL_CRASH_FIXTURE"); path != "" {
		db := openWAL(t, path)
		for i := 0; i < 25; i++ {
			if _, err := db.Exec("INSERT INTO entries(value) VALUES(?)", strings.Repeat("wal", 2000)); err != nil {
				t.Fatal(err)
			}
		}
		os.Exit(0) // Deliberately skip Close and checkpoint.
	}
	path := filepath.Join(t.TempDir(), "crash.db")
	cmd := exec.Command(os.Args[0], "-test.run=^TestWALSurvivesAbruptProcessExit$")
	cmd.Env = append(os.Environ(), "AICLAW_WAL_CRASH_FIXTURE="+path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, output)
	}
	if stat, err := os.Stat(path + "-wal"); err != nil || stat.Size() == 0 {
		t.Fatal("fixture did not leave WAL", err)
	}
	db := openWAL(t, path)
	defer db.Close()
	if err := Check(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM entries").Scan(&count); err != nil || count != 25 {
		t.Fatal("committed WAL lost", count, err)
	}
}
