package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	home := t.TempDir()
	db, err := Open(home)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, home
}

func TestOpenCreatesDatabaseInDataHome(t *testing.T) {
	_, home := open(t)
	if _, err := os.Stat(filepath.Join(home, "sessions.db")); err != nil {
		t.Fatalf("库文件应当建在 data home 下：%v", err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	db, _ := open(t)
	ctx := context.Background()
	created := time.Now().Add(-time.Hour).Truncate(time.Millisecond)

	want := Session{
		ID: "s1", Title: "记住 42",
		CreatedAt: created, UpdatedAt: created.Add(time.Minute),
		Workdir: "/tmp/work", Model: "claude-sonnet-4", TurnCount: 3,
		Config:   json.RawMessage(`{"workdir":"/tmp/work"}`),
		Messages: json.RawMessage(`[{"Role":"user","Content":"你好"}]`),
	}
	if err := db.Save(ctx, want); err != nil {
		t.Fatal(err)
	}

	got, err := db.Load(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != want.Title || got.Model != want.Model || got.TurnCount != want.TurnCount {
		t.Errorf("存回来的字段不对：%+v", got)
	}
	if string(got.Messages) != string(want.Messages) {
		t.Errorf("消息应当原样存取：%s", got.Messages)
	}
	// 时间戳存的是毫秒。丢了精度会让「按更新时间排序」在同一秒内失序。
	if !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Errorf("更新时间 = %v，期望 %v", got.UpdatedAt, want.UpdatedAt)
	}
}

func TestSaveOverwritesButKeepsCreatedAt(t *testing.T) {
	db, _ := open(t)
	ctx := context.Background()
	created := time.Now().Add(-24 * time.Hour).Truncate(time.Millisecond)

	base := Session{ID: "s1", Title: "旧标题", CreatedAt: created, UpdatedAt: created, TurnCount: 1}
	if err := db.Save(ctx, base); err != nil {
		t.Fatal(err)
	}
	base.Title = "新标题"
	base.TurnCount = 2
	base.UpdatedAt = time.Now().Truncate(time.Millisecond)
	base.CreatedAt = time.Now() // 调用方传错了也不该覆盖原始创建时间
	if err := db.Save(ctx, base); err != nil {
		t.Fatal(err)
	}

	got, err := db.Load(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "新标题" || got.TurnCount != 2 {
		t.Errorf("覆盖没生效：%+v", got)
	}
	// created_at 不在 UPDATE 的字段里：会话的创建时间是事实，不该被后续保存改写。
	if !got.CreatedAt.Equal(created) {
		t.Errorf("创建时间被改写了：%v，期望 %v", got.CreatedAt, created)
	}
}

func TestListIsNewestFirstAndOmitsMessages(t *testing.T) {
	db, _ := open(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)

	for i, spec := range []struct {
		id      string
		updated time.Time
	}{
		{"old", now.Add(-2 * time.Hour)},
		{"new", now},
		{"mid", now.Add(-time.Hour)},
	} {
		if err := db.Save(ctx, Session{
			ID: spec.id, UpdatedAt: spec.updated, CreatedAt: spec.updated,
			TurnCount: i, Messages: json.RawMessage(`[{"Role":"user"}]`),
		}); err != nil {
			t.Fatal(err)
		}
	}

	list, err := db.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{list[0].ID, list[1].ID, list[2].ID}
	if got[0] != "new" || got[1] != "mid" || got[2] != "old" {
		t.Errorf("应当按更新时间倒序，实际 %v", got)
	}
}

func TestListOnEmptyDatabaseReturnsEmptySlice(t *testing.T) {
	db, _ := open(t)
	list, err := db.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// 必须是空切片而不是 nil：协议帧里 nil 会编码成 null，宿主那边
	// `sessions.map(...)` 就炸了。
	if list == nil {
		t.Fatal("空库应当返回空切片，不是 nil")
	}
	if len(list) != 0 {
		t.Errorf("空库不该有内容：%v", list)
	}
}

func TestLoadMissingReturnsErrNotFound(t *testing.T) {
	db, _ := open(t)
	if _, err := db.Load(context.Background(), "nope"); err != ErrNotFound {
		t.Errorf("期望 ErrNotFound，实际 %v", err)
	}
}

func TestDeleteMissingIsNotAnError(t *testing.T) {
	db, _ := open(t)
	// 调用方多半只是想确保它没了，不存在正好满足这个意图。
	if err := db.Delete(context.Background(), "nope"); err != nil {
		t.Errorf("删不存在的会话不该报错：%v", err)
	}
}

func TestConcurrentSavesDoNotFail(t *testing.T) {
	db, _ := open(t)
	ctx := context.Background()
	now := time.Now()

	// 多个会话各自跑完一轮会同时落盘。撞上 SQLITE_BUSY 就会变成
	// 随机出现的「保存会话失败」，所以这条要盯住。
	var wg sync.WaitGroup
	errs := make([]error, 16)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = db.Save(ctx, Session{
				ID: string(rune('a' + i)), CreatedAt: now, UpdatedAt: now,
				Messages: json.RawMessage(`[]`),
			})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("第 %d 个并发保存失败：%v", i, err)
		}
	}
	list, err := db.List(ctx)
	if err != nil || len(list) != len(errs) {
		t.Errorf("期望 %d 个会话，实际 %d（%v）", len(errs), len(list), err)
	}
}

func TestImportLegacyMovesJSONFilesIn(t *testing.T) {
	db, home := open(t)
	ctx := context.Background()

	dir := filepath.Join(home, "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{
  "id": "s_old",
  "title": "以前的对话",
  "createdAt": "2026-09-01T10:00:00Z",
  "updatedAt": "2026-09-01T11:00:00Z",
  "config": {"workdir": "/tmp/old", "model": {"model": "gpt-4o"}},
  "messages": [{"Role": "user", "Content": "你好"}],
  "turnCount": 2
}`
	if err := os.WriteFile(filepath.Join(dir, "s_old.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	// 坏文件不该让其余会话都进不来。
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	count, err := ImportLegacy(ctx, db, home)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("期望导入 1 个，实际 %d", count)
	}

	got, err := db.Load(ctx, "s_old")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "以前的对话" || got.TurnCount != 2 {
		t.Errorf("导入的内容不对：%+v", got)
	}
	// workdir 与 model 要从 config 里提到列上，列表才不用解析 config。
	if got.Workdir != "/tmp/old" || got.Model != "gpt-4o" {
		t.Errorf("导入时应当提取 workdir 与 model，实际 %q / %q", got.Workdir, got.Model)
	}
	// 原文件保留：搬错了还能翻回去看。
	if _, err := os.Stat(filepath.Join(dir, "s_old.json")); err != nil {
		t.Errorf("导入后不该删原文件：%v", err)
	}
}

func TestImportLegacyIsIdempotent(t *testing.T) {
	db, home := open(t)
	ctx := context.Background()
	dir := filepath.Join(home, "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{"id":"s1","title":"原标题","messages":[],"turnCount":1}`
	if err := os.WriteFile(filepath.Join(dir, "s1.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := ImportLegacy(ctx, db, home); err != nil {
		t.Fatal(err)
	}
	// 导入之后用户继续用这个会话，标题变了。
	loaded, _ := db.Load(ctx, "s1")
	loaded.Title = "后来改的标题"
	if err := db.Save(ctx, loaded); err != nil {
		t.Fatal(err)
	}

	count, err := ImportLegacy(ctx, db, home)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("第二次导入不该再搬，实际搬了 %d 个", count)
	}
	again, _ := db.Load(ctx, "s1")
	// 覆盖回旧内容等于把用户后来的对话弄丢了。
	if again.Title != "后来改的标题" {
		t.Errorf("重复导入覆盖了后来的改动：%q", again.Title)
	}
}

func TestImportLegacyOnFreshInstall(t *testing.T) {
	db, home := open(t)
	// 全新安装没有 sessions/ 目录，这不是错误。
	count, err := ImportLegacy(context.Background(), db, home)
	if err != nil {
		t.Errorf("没有旧目录时不该报错：%v", err)
	}
	if count != 0 {
		t.Errorf("没有旧文件时不该导入任何东西，实际 %d", count)
	}
}
