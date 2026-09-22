// Package store 把会话存进 data home 下的一个 SQLite 库。
//
// 驱动用 github.com/glebarez/go-sqlite——modernc.org/sqlite 的一个分支，同样是把
// SQLite 翻译成 Go 的纯 Go 实现，**不需要 cgo**。选它而不是 modernc 本尊，是因为
// 同一个二进制里应用库（gorm + glebarez/sqlite）已经带着它，而两个包都注册名为
// "sqlite" 的 database/sql 驱动，同时链进来进程一启动就 panic：
// "sql: Register called twice for driver sqlite"。
// 这一点是硬要求：开了 cgo 就没法交叉编译（macOS 上出不了 Windows 包），
// 还要求每台构建机装好 C 工具链。换驱动前先确认新的那个也不需要 cgo。
//
// 为什么不是一个会话一个 JSON 文件（最早的做法）：
//   - 列表要读全部文件再逐个解析，会话一多就慢，而列表每轮都要刷；
//   - 写到一半被杀会留下坏文件，得靠「临时文件 + 改名」自己保证原子性；
//   - 跨会话查询（按更新时间排序、按工作目录筛）只能在内存里做。
//
// SQLite 把这三件事都变成别人的问题。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/glebarez/go-sqlite"
)

// Session 是一个会话的完整存档。Config 与 Messages 由调用方给出原始 JSON——
// store 不关心它们的内部结构，避免每加一个字段就要改这里。
type Session struct {
	ID        string
	Title     string
	CreatedAt time.Time
	UpdatedAt time.Time
	Workdir   string
	Model     string
	TurnCount int
	Config    json.RawMessage
	Messages  json.RawMessage
}

// Summary 是会话列表里的一行，不带消息正文。
type Summary struct {
	ID        string
	Title     string
	CreatedAt time.Time
	UpdatedAt time.Time
	Workdir   string
	Model     string
	TurnCount int
	/** 搜索时命中的那一小段正文。只有 Search 会填。 */
	Snippet string
}

type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
  id          TEXT PRIMARY KEY,
  title       TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL,
  workdir     TEXT NOT NULL DEFAULT '',
  model       TEXT NOT NULL DEFAULT '',
  turn_count  INTEGER NOT NULL DEFAULT 0,
  config      TEXT NOT NULL DEFAULT '{}',
  messages    TEXT NOT NULL DEFAULT '[]'
);
CREATE INDEX IF NOT EXISTS sessions_updated_at ON sessions (updated_at DESC);
`

// Open 打开（必要时创建）data home 下的会话库。
func Open(dataHome string) (*Store, error) {
	if err := os.MkdirAll(dataHome, 0o755); err != nil {
		return nil, fmt.Errorf("创建数据目录失败：%w", err)
	}
	path := filepath.Join(dataHome, "sessions.db")

	// WAL：读不挡写。列表查询和轮次结束后的写入会撞在一起，默认的
	// 回滚日志模式下后者会把前者堵住。
	// busy_timeout：撞上了就等，而不是立刻返回 SQLITE_BUSY——那会让
	// 「保存会话失败」变成一个随机出现的错误。
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开会话库失败：%w", err)
	}
	// 单连接。modernc 的驱动本身是并发安全的，但把写串起来能彻底避开
	// SQLITE_BUSY；这个库的写入量是「每轮一次」，不值得为并发写调优。
	db.SetMaxOpenConns(1)

	if _, err := db.ExecContext(context.Background(), schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化会话库失败：%w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Save 写入或覆盖一个会话。
func (s *Store) Save(ctx context.Context, session Session) error {
	if session.Config == nil {
		session.Config = json.RawMessage("{}")
	}
	if session.Messages == nil {
		session.Messages = json.RawMessage("[]")
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO sessions (id, title, created_at, updated_at, workdir, model, turn_count, config, messages)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  title = excluded.title,
  updated_at = excluded.updated_at,
  workdir = excluded.workdir,
  model = excluded.model,
  turn_count = excluded.turn_count,
  config = excluded.config,
  messages = excluded.messages`,
		session.ID, session.Title,
		session.CreatedAt.UnixMilli(), session.UpdatedAt.UnixMilli(),
		session.Workdir, session.Model, session.TurnCount,
		string(session.Config), string(session.Messages),
	)
	if err != nil {
		return fmt.Errorf("保存会话失败：%w", err)
	}
	return nil
}

// ErrNotFound 表示库里没有这个会话。
var ErrNotFound = fmt.Errorf("会话不存在")

func (s *Store) Load(ctx context.Context, id string) (Session, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, title, created_at, updated_at, workdir, model, turn_count, config, messages
FROM sessions WHERE id = ?`, id)

	var session Session
	var created, updated int64
	var config, messages string
	err := row.Scan(&session.ID, &session.Title, &created, &updated,
		&session.Workdir, &session.Model, &session.TurnCount, &config, &messages)
	if err == sql.ErrNoRows {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("读取会话失败：%w", err)
	}
	session.CreatedAt = time.UnixMilli(created)
	session.UpdatedAt = time.UnixMilli(updated)
	session.Config = json.RawMessage(config)
	session.Messages = json.RawMessage(messages)
	return session, nil
}

// List 按更新时间倒序列出全部会话，不带消息正文。
func (s *Store) List(ctx context.Context) ([]Summary, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, title, created_at, updated_at, workdir, model, turn_count
FROM sessions ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("列出会话失败：%w", err)
	}
	defer rows.Close()

	summaries := []Summary{}
	for rows.Next() {
		var summary Summary
		var created, updated int64
		if err := rows.Scan(&summary.ID, &summary.Title, &created, &updated,
			&summary.Workdir, &summary.Model, &summary.TurnCount); err != nil {
			return nil, fmt.Errorf("列出会话失败：%w", err)
		}
		summary.CreatedAt = time.UnixMilli(created)
		summary.UpdatedAt = time.UnixMilli(updated)
		summaries = append(summaries, summary)
	}
	return summaries, rows.Err()
}

/**
 * Search 按关键词找会话：标题与消息正文都找，返回命中的片段。
 *
 * 只按标题找是不够的——标题是第一句话自动截的，而人回来找的往往是中间提过的
 * 某个表名、某个报错。消息正文是一整块 JSON，用 LIKE 扫它足够了：这是本机
 * 单用户的库，会话是几百条量级，一次全表扫描比引入 FTS5 划算得多
 * （FTS5 要建虚表、维护触发器，还要考虑中文分词——而 LIKE 对中文天然可用）。
 *
 * 命中片段在 Go 这边从 JSON 里取，不在 SQL 里做：SQL 只知道整块文本的偏移，
 * 取出来的片段大概率跨在 JSON 转义中间，显示出来是乱的。
 */
func (s *Store) Search(ctx context.Context, keyword string, limit int) ([]Summary, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return s.List(ctx)
	}
	if limit <= 0 {
		limit = 50
	}
	pattern := "%" + escapeLike(keyword) + "%"
	rows, err := s.db.QueryContext(ctx, `
SELECT id, title, created_at, updated_at, workdir, model, turn_count, messages
FROM sessions
WHERE title LIKE ? ESCAPE '\' OR messages LIKE ? ESCAPE '\'
ORDER BY updated_at DESC LIMIT ?`, pattern, pattern, limit)
	if err != nil {
		return nil, fmt.Errorf("搜索会话失败：%w", err)
	}
	defer rows.Close()

	summaries := []Summary{}
	for rows.Next() {
		var summary Summary
		var created, updated int64
		var messages string
		if err := rows.Scan(&summary.ID, &summary.Title, &created, &updated,
			&summary.Workdir, &summary.Model, &summary.TurnCount, &messages); err != nil {
			return nil, fmt.Errorf("搜索会话失败：%w", err)
		}
		summary.CreatedAt = time.UnixMilli(created)
		summary.UpdatedAt = time.UnixMilli(updated)
		summary.Snippet = snippet(messages, keyword)
		summaries = append(summaries, summary)
	}
	return summaries, rows.Err()
}

// escapeLike 转义 LIKE 的通配符。用户搜「100%」时不该变成「100 + 任意」。
func escapeLike(raw string) string {
	replacer := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)
	return replacer.Replace(raw)
}

// snippet 从消息正文里取一段含关键词的上下文，给搜索结果显示。
//
// 解析成消息数组再找，而不是在原始 JSON 上找：后者会把转义序列、字段名、
// base64 的图片一起搜进去，显示出来是一串乱码。
func snippet(messages, keyword string) string {
	var parsed []struct {
		Role    string `json:"Role"`
		Content string `json:"Content"`
	}
	if json.Unmarshal([]byte(messages), &parsed) != nil {
		return ""
	}
	lower := strings.ToLower(keyword)
	for _, message := range parsed {
		// 系统提示词里有工作目录、工具名这些，命中了对用户没有意义。
		if strings.EqualFold(message.Role, "system") {
			continue
		}
		at := strings.Index(strings.ToLower(message.Content), lower)
		if at < 0 {
			continue
		}
		runes := []rune(message.Content)
		// 按 rune 切，别把一个汉字劈成两半。
		start := len([]rune(message.Content[:at]))
		from := max(0, start-30)
		to := min(len(runes), start+len([]rune(keyword))+40)
		text := strings.TrimSpace(string(runes[from:to]))
		if from > 0 {
			text = "…" + text
		}
		if to < len(runes) {
			text += "…"
		}
		return text
	}
	return ""
}

// Delete 删除一个会话。不存在时不算错误——调用方多半只是想确保它没了。
func (s *Store) Delete(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id); err != nil {
		return fmt.Errorf("删除会话失败：%w", err)
	}
	return nil
}
