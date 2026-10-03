package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRenamePreservesMetadataAndSurvivesStaleSaves(t *testing.T) {
	db, _ := open(t)
	ctx := context.Background()
	original := Session{ID: "rename", Title: "automatic", CreatedAt: time.Now(), UpdatedAt: time.Now(), Workdir: "/work", Model: "fixture", TurnCount: 3, Config: json.RawMessage(`{"workdir":"/work"}`), Messages: json.RawMessage(`[{"role":"user","content":"original"}]`)}
	if err := db.Save(ctx, original); err != nil {
		t.Fatal(err)
	}
	if err := db.SetArchived(ctx, []string{original.ID}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if title, err := db.Rename(ctx, original.ID, "  自定义 🐾  "); err != nil || title != "自定义 🐾" {
		t.Fatalf("%q %v", title, err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := db.Save(ctx, original); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	got, err := db.Load(ctx, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "自定义 🐾" || got.Workdir != original.Workdir || got.Model != original.Model || got.TurnCount != original.TurnCount || string(got.Config) != string(original.Config) || string(got.Messages) != string(original.Messages) || !got.UpdatedAt.Equal(original.UpdatedAt.Truncate(time.Millisecond)) {
		t.Fatalf("rename changed unrelated metadata: %+v", got)
	}
	archived, err := db.ListArchived(ctx)
	if err != nil || len(archived) != 1 || archived[0].Title != got.Title {
		t.Fatalf("archive state lost: %+v %v", archived, err)
	}
}

func TestRenameValidationDoesNotMutate(t *testing.T) {
	db, _ := open(t)
	ctx := context.Background()
	if err := db.Save(ctx, Session{ID: "rename", Title: "old", CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"", "  ", "one\ntwo", "one\ttwo", "a\x00b", strings.Repeat("中", 201), string([]byte{0xff})} {
		if _, err := db.Rename(ctx, "rename", title); err == nil {
			t.Fatalf("accepted invalid title %q", title)
		}
	}
	got, _ := db.Load(ctx, "rename")
	if got.Title != "old" {
		t.Fatal(got.Title)
	}
	if _, err := db.Rename(ctx, "missing", "name"); err != ErrNotFound {
		t.Fatal(err)
	}
	if _, err := db.Rename(ctx, "rename", strings.Repeat("中", 200)); err != nil {
		t.Fatal(err)
	}
}

func TestRenameMigratesOldDatabaseAndPersistsAfterReopen(t *testing.T) {
	home := t.TempDir()
	ctx := context.Background()
	legacy, err := sql.Open("sqlite", "file:"+filepath.Join(home, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.ExecContext(ctx, schema); err != nil {
		t.Fatal(err)
	}
	legacy.Close()
	db, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Save(ctx, Session{ID: "s", Title: "old", CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Rename(ctx, "s", "new"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Save(ctx, Session{ID: "s", Title: "stale", CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	got, err := db.Load(ctx, "s")
	if err != nil || got.Title != "new" {
		t.Fatalf("%+v %v", got, err)
	}
}
