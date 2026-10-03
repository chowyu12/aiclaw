package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func workDB(t *testing.T) *Store {
	t.Helper()
	db, _ := open(t)
	if err := db.Save(context.Background(), Session{ID: "s"}); err != nil {
		t.Fatal(err)
	}
	return db
}
func TestSubmissionRecoveryRetainsQueuedAndNeverReplaysRunning(t *testing.T) {
	db := workDB(t)
	ctx := context.Background()
	for _, id := range []string{"q", "r"} {
		if err := db.AcceptSubmission(ctx, "s", Submission{RequestID: id, TurnID: "turn", Payload: json.RawMessage(`{"text":"input"}`), Fingerprint: "hash"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SetSubmission(ctx, "s", "r", "turn", "running"); err != nil {
		t.Fatal(err)
	}
	if err := db.RecoverSubmissions(ctx); err != nil {
		t.Fatal(err)
	}
	q, _ := db.Submission(ctx, "s", "q")
	r, _ := db.Submission(ctx, "s", "r")
	if q.State != "queued" || r.State != "uncertain" {
		t.Fatalf("%+v %+v", q, r)
	}
	if err := db.AcceptSubmission(ctx, "s", q); err == nil {
		t.Fatal("duplicate request accepted")
	}
	if err := db.Delete(ctx, "s"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Submission(ctx, "s", "r"); err == nil {
		t.Fatal("request survived deletion")
	}
}
func TestTrackedWriteUndoPreservesExternalChangesAndCreation(t *testing.T) {
	db := workDB(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := db.WriteTracked(ctx, "s", "t", path, []byte("after\n"), []byte("before\n")); err != nil {
		t.Fatal(err)
	}
	changes, err := db.Changes(ctx, "s")
	if err != nil || len(changes) != 1 {
		t.Fatalf("%v %v", changes, err)
	}
	c := changes[0]
	if c.Before != "before\n" || c.After != "after\n" || c.Conflict {
		t.Fatalf("%+v", c)
	}
	os.WriteFile(path, []byte("external"), 0600)
	if err = db.UndoChange(ctx, "s", c.ID, func(string) error { return nil }); err == nil {
		t.Fatal("overwrote external change")
	}
	if err = db.WriteTracked(ctx, "s", "t", path, []byte("wrong"), []byte("after\n")); err == nil {
		t.Fatal("stale edit accepted")
	}
	os.WriteFile(path, []byte("after\n"), 0600)
	if err = db.UndoChange(ctx, "s", c.ID, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	if string(body) != "before\n" || info.Mode().Perm() != 0600 {
		t.Fatal("restore content/mode mismatch")
	}
	created := filepath.Join(filepath.Dir(path), "new.txt")
	if err = db.WriteTracked(ctx, "s", "t", created, []byte("new"), nil); err != nil {
		t.Fatal(err)
	}
	changes, _ = db.Changes(ctx, "s")
	if err = db.UndoChange(ctx, "s", changes[0].ID, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(created); !os.IsNotExist(err) {
		t.Fatal("created file was not removed")
	}
}
func TestUndoRefusesSymlinkReplacement(t *testing.T) {
	db := workDB(t)
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	target := filepath.Join(dir, "target")
	if err := db.WriteTracked(ctx, "s", "t", path, []byte("same"), nil); err != nil {
		t.Fatal(err)
	}
	changes, _ := db.Changes(ctx, "s")
	os.WriteFile(target, []byte("same"), 0600)
	os.Remove(path)
	if err := os.Symlink(target, path); err != nil {
		t.Skip(err)
	}
	if err := db.UndoChange(ctx, "s", changes[0].ID, func(string) error { return nil }); err == nil {
		t.Fatal("followed substituted symlink")
	}
	body, _ := os.ReadFile(target)
	if string(body) != "same" {
		t.Fatal("target modified")
	}
}

func TestUndoResumesAfterRestoreBeforeReceiptCommit(t *testing.T) {
	db := workDB(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "file")
	os.WriteFile(path, []byte("before"), 0600)
	if err := db.WriteTracked(ctx, "s", "t", path, []byte("after"), nil); err != nil {
		t.Fatal(err)
	}
	changes, _ := db.Changes(ctx, "s")
	change := changes[0]
	change.State = "undoing"
	if err := db.PutState(ctx, "s", "change:"+change.ID, change); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path, []byte("before"), 0600)
	if err := db.UndoChange(ctx, "s", change.ID, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	changes, _ = db.Changes(ctx, "s")
	if changes[0].State != "undone" {
		t.Fatal("undo receipt not completed")
	}
}

func TestUndoRecoveryOfCreatedFileRemainsActionable(t *testing.T) {
	db := workDB(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "new-file")
	if err := db.WriteTracked(ctx, "s", "t", path, []byte("new"), nil); err != nil {
		t.Fatal(err)
	}
	changes, _ := db.Changes(ctx, "s")
	change := changes[0]
	change.State = "undoing"
	if err := db.PutState(ctx, "s", "change:"+change.ID, change); err != nil {
		t.Fatal(err)
	}
	os.Remove(path)
	changes, _ = db.Changes(ctx, "s")
	if changes[0].Conflict {
		t.Fatal("UI cannot finish an already applied undo")
	}
	if err := db.UndoChange(ctx, "s", change.ID, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentTrackedEditsOnlyOneCanUseOriginalSnapshot(t *testing.T) {
	db := workDB(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, text := range []string{"first", "second"} {
		wg.Add(1)
		go func(text string) {
			defer wg.Done()
			<-start
			results <- db.WriteTracked(ctx, "s", "t", path, []byte(text), []byte("original"))
		}(text)
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("accepted %d competing edits", successes)
	}
	changes, err := db.Changes(ctx, "s")
	if err != nil || len(changes) != 1 {
		t.Fatalf("changes=%+v err=%v", changes, err)
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != changes[0].After {
		t.Fatalf("snapshot disagrees with file: %q %v", body, err)
	}
	if err := db.UndoChange(ctx, "s", changes[0].ID, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(path)
	if err != nil || string(body) != "original" {
		t.Fatalf("undo lost original: %q %v", body, err)
	}
}

func TestConcurrentUndoIsIdempotentForCreatedFile(t *testing.T) {
	db := workDB(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "new")
	if err := db.WriteTracked(ctx, "s", "t", path, []byte("new"), nil); err != nil {
		t.Fatal(err)
	}
	changes, err := db.Changes(ctx, "s")
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 8)
	for i := 0; i < cap(results); i++ {
		go func() { <-start; results <- db.UndoChange(ctx, "s", changes[0].ID, func(string) error { return nil }) }()
	}
	close(start)
	for i := 0; i < cap(results); i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("created file survived undo: %v", err)
	}
	var saved FileChange
	if err := db.State(ctx, "s", "change:"+changes[0].ID, &saved); err != nil || saved.State != "undone" {
		t.Fatalf("receipt=%+v err=%v", saved, err)
	}
}

func TestTrackedWriteRejectsUnsafeSnapshotsWithoutChangingFile(t *testing.T) {
	cases := []struct {
		name          string
		before, after []byte
	}{
		{"binary output", []byte("safe"), []byte{'a', 0, 'b'}},
		{"invalid UTF8 output", []byte("safe"), []byte{0xff}},
		{"oversized output", []byte("safe"), bytes.Repeat([]byte("x"), maxSnapshotBytes+1)},
		{"binary original", []byte{'a', 0, 'b'}, []byte("safe")},
		{"invalid UTF8 original", []byte{0xff}, []byte("safe")},
		{"oversized original", bytes.Repeat([]byte("x"), maxSnapshotBytes+1), []byte("safe")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := workDB(t)
			path := filepath.Join(t.TempDir(), "file")
			if err := os.WriteFile(path, tc.before, 0600); err != nil {
				t.Fatal(err)
			}
			if err := db.WriteTracked(context.Background(), "s", "t", path, tc.after, nil); err == nil {
				t.Fatal("unsafe write accepted")
			}
			body, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(body, tc.before) {
				t.Fatal("rejected write changed original")
			}
			changes, err := db.Changes(context.Background(), "s")
			if err != nil || len(changes) != 0 {
				t.Fatalf("rejected write recorded changes: %d %v", len(changes), err)
			}
		})
	}
}

func TestUndoRevalidatesCurrentWritePermission(t *testing.T) {
	db := workDB(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "file")
	if err := db.WriteTracked(ctx, "s", "t", path, []byte("after"), nil); err != nil {
		t.Fatal(err)
	}
	changes, err := db.Changes(ctx, "s")
	if err != nil {
		t.Fatal(err)
	}
	denied := errors.New("path is now protected")
	err = db.UndoChange(ctx, "s", changes[0].ID, func(got string) error {
		if got != changes[0].Path {
			t.Errorf("validated %q, expected canonical path %q", got, changes[0].Path)
		}
		return denied
	})
	if !errors.Is(err, denied) {
		t.Fatalf("permission ignored: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != "after" {
		t.Fatal("denied undo changed file")
	}
	var saved FileChange
	if err := db.State(ctx, "s", "change:"+changes[0].ID, &saved); err != nil || saved.State != "applied" {
		t.Fatalf("denied undo changed receipt: %+v %v", saved, err)
	}
}

func TestRestartPreservesWorkReceiptsTranscriptAndUndoSnapshot(t *testing.T) {
	db, home := open(t)
	ctx := context.Background()
	if err := db.Save(ctx, Session{ID: "s"}); err != nil {
		t.Fatal(err)
	}
	goal := map[string]any{"status": "active", "tokensUsed": 17, "elapsedMs": 91, "tokenBudget": 100, "evidence": "partial verification"}
	if err := db.PutState(ctx, "s", "goal", goal); err != nil {
		t.Fatal(err)
	}
	if err := db.PutState(ctx, "s", "execution", Execution{TurnID: "turn", State: "running"}); err != nil {
		t.Fatal(err)
	}
	payload := json.RawMessage(`{"text":"input","images":["image.png"],"audioPaths":["audio.wav"]}`)
	for _, id := range []string{"queued", "running"} {
		if err := db.AcceptSubmission(ctx, "s", Submission{RequestID: id, TurnID: "turn", Fingerprint: "identity", Payload: payload}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SetSubmission(ctx, "s", "running", "turn", "running"); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"original result", "duplicate must not replace original"} {
		if err := db.RecordMessage(ctx, "s", "message", map[string]string{"content": text}); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := db.WriteTracked(ctx, "s", "turn", path, []byte("after"), nil); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	resumed, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	if err := resumed.RecoverSubmissions(ctx); err != nil {
		t.Fatal(err)
	}
	if err := resumed.RecoverSubmissions(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"queued", "running"} {
		receipt, err := resumed.Submission(ctx, "s", id)
		expected := "queued"
		if id == "running" {
			expected = "uncertain"
		}
		if err != nil || receipt.State != expected || receipt.Fingerprint != "identity" || !bytes.Equal(receipt.Payload, payload) {
			t.Fatalf("lost request identity/attachments: %+v %v", receipt, err)
		}
	}
	var saved struct {
		Status                             string
		TokensUsed, ElapsedMS, TokenBudget int64
		Evidence                           string
	}
	if err := resumed.State(ctx, "s", "goal", &saved); err != nil || saved.Status != "paused" || saved.TokensUsed != 17 || saved.ElapsedMS != 91 || saved.TokenBudget != 100 || saved.Evidence != "partial verification" {
		t.Fatalf("lost goal progress: %+v %v", saved, err)
	}
	var execution Execution
	if err := resumed.State(ctx, "s", "execution", &execution); err != nil || execution.State != "uncertain" {
		t.Fatalf("execution=%+v err=%v", execution, err)
	}
	messages, err := resumed.Transcript(ctx, "s")
	if err != nil || len(messages) != 1 || string(messages[0]) != `{"content":"original result"}` {
		t.Fatalf("immutable transcript=%s err=%v", messages, err)
	}
	changes, err := resumed.Changes(ctx, "s")
	if err != nil || len(changes) != 1 || changes[0].Conflict {
		t.Fatalf("lost undo snapshot: %+v %v", changes, err)
	}
	if err := resumed.UndoChange(ctx, "s", changes[0].ID, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != "before" {
		t.Fatalf("restart undo=%q err=%v", body, err)
	}
}
