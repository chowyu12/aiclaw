package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// 用量记录：每次打模型、每次调工具记一行，设置页的「用量」按时间段汇总。
//
// 为什么单独记而不是从会话存档里现算：存档里存的是给模型看的消息，没有 token 数
// （那只在当轮的事件里出现一次），也没有耗时；会话删了、压缩过，记录也就跟着没了，
// 而「这个月花了多少」不该因为删了一个会话就变少。
//
// 写入走后台队列：记录发生在 Agent 循环里，一次 SQLite 写入虽然只有毫秒级，也不该
// 让模型调用、工具调用去等它。队列满了就丢（只记一句），宁可少一行统计，不能卡住对话。

const usageSchema = `
CREATE TABLE IF NOT EXISTS usage_events (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  at          INTEGER NOT NULL,
  session_id  TEXT NOT NULL DEFAULT '',
  kind        TEXT NOT NULL,
  model       TEXT NOT NULL DEFAULT '',
  input       INTEGER NOT NULL DEFAULT 0,
  output      INTEGER NOT NULL DEFAULT 0,
  total       INTEGER NOT NULL DEFAULT 0,
  tool        TEXT NOT NULL DEFAULT '',
  source      TEXT NOT NULL DEFAULT '',
  detail      TEXT NOT NULL DEFAULT '',
  failed      INTEGER NOT NULL DEFAULT 0,
  duration_ms INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS usage_events_at ON usage_events (at);
`

// UsageKind 是一条用量记录的类型。
type UsageKind string

const (
	// UsageModel 是一次模型调用（一次采样）。
	UsageModel UsageKind = "model"
	// UsageTool 是一次工具调用。
	UsageTool UsageKind = "tool"
)

// UsageEvent 是一条用量记录。
type UsageEvent struct {
	At        time.Time
	SessionID string
	Kind      UsageKind
	Model     string
	Input     int
	Output    int
	Total     int
	// Tool 是工具名（MCP 工具带 server 前缀）。
	Tool string
	// Source 是工具来自哪儿：builtin、mcp:<server>、exec（代码模式脚本里调的）。
	Source string
	// Detail 是附加信息：load_skill 的技能名。
	Detail     string
	Failed     bool
	DurationMS int64
}

type usageWriter struct {
	queue chan UsageEvent
	done  chan struct{}
	once  sync.Once
}

const usageQueueSize = 4096

func (s *Store) startUsageWriter() {
	s.usage = &usageWriter{queue: make(chan UsageEvent, usageQueueSize), done: make(chan struct{})}
	go s.drainUsage()
}

// RecordUsage 记一条用量。不阻塞：队列满了就丢。
func (s *Store) RecordUsage(event UsageEvent) {
	if s.usage == nil {
		return
	}
	if event.At.IsZero() {
		event.At = time.Now()
	}
	select {
	case s.usage.queue <- event:
	default:
	}
}

// drainUsage 把队列里的记录攒成一批写进去：一批一个事务，比一条一个快两个数量级。
func (s *Store) drainUsage() {
	defer close(s.usage.done)
	batch := make([]UsageEvent, 0, 64)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		_ = s.insertUsage(context.Background(), batch)
		batch = batch[:0]
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case event, ok := <-s.usage.queue:
			if !ok {
				flush()
				return
			}
			batch = append(batch, event)
			if len(batch) >= 64 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// flushUsage 停下写入并等队列写完。Close 之前调。
func (s *Store) flushUsage() {
	if s.usage == nil {
		return
	}
	s.usage.once.Do(func() { close(s.usage.queue) })
	<-s.usage.done
}

func (s *Store) insertUsage(ctx context.Context, events []UsageEvent) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	statement, err := tx.PrepareContext(ctx, `INSERT INTO usage_events
		(at, session_id, kind, model, input, output, total, tool, source, detail, failed, duration_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer statement.Close()
	for _, event := range events {
		failed := 0
		if event.Failed {
			failed = 1
		}
		if _, err := statement.ExecContext(ctx,
			event.At.UnixMilli(), event.SessionID, string(event.Kind), event.Model,
			event.Input, event.Output, event.Total, event.Tool, event.Source, event.Detail,
			failed, event.DurationMS,
		); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// ---------- 汇总 ----------

// UsageTotals 是一段时间里的总量。
type UsageTotals struct {
	Input       int64 `json:"input"`
	Output      int64 `json:"output"`
	Total       int64 `json:"total"`
	ModelCalls  int64 `json:"modelCalls"`
	ModelFailed int64 `json:"modelFailed"`
	ToolCalls   int64 `json:"toolCalls"`
	ToolFailed  int64 `json:"toolFailed"`
	Sessions    int64 `json:"sessions"`
}

// UsageDay 是一天的量。Day 是本地日期 YYYY-MM-DD。
type UsageDay struct {
	Day        string `json:"day"`
	Input      int64  `json:"input"`
	Output     int64  `json:"output"`
	ModelCalls int64  `json:"modelCalls"`
	ToolCalls  int64  `json:"toolCalls"`
}

// UsageModelRow 是一个模型的量。
type UsageModelRow struct {
	Model  string `json:"model"`
	Input  int64  `json:"input"`
	Output int64  `json:"output"`
	Calls  int64  `json:"calls"`
	Failed int64  `json:"failed"`
}

// UsageToolRow 是一个工具的量。
type UsageToolRow struct {
	Tool   string `json:"tool"`
	Source string `json:"source"`
	Calls  int64  `json:"calls"`
	Failed int64  `json:"failed"`
	AvgMS  int64  `json:"avgMs"`
}

// UsageSkillRow 是一个技能被取用的次数。
type UsageSkillRow struct {
	Skill string `json:"skill"`
	Uses  int64  `json:"uses"`
}

// UsageSessionRow 是一个会话的量。Title 由调用方从会话表补。
type UsageSessionRow struct {
	SessionID string `json:"sessionId"`
	Title     string `json:"title"`
	Total     int64  `json:"total"`
	Calls     int64  `json:"calls"`
}

// UsageSummary 是设置页「用量」要的全部数字。
type UsageSummary struct {
	Since    int64             `json:"since"`
	Totals   UsageTotals       `json:"totals"`
	Days     []UsageDay        `json:"days"`
	Models   []UsageModelRow   `json:"models"`
	Tools    []UsageToolRow    `json:"tools"`
	Skills   []UsageSkillRow   `json:"skills"`
	Sessions []UsageSessionRow `json:"sessions"`
	// FirstAt 是有记录以来最早的一条（毫秒）。0 表示还没有任何记录。
	FirstAt int64 `json:"firstAt"`
}

// Usage 汇总 since 之后的用量。按天分组用 loc 的日期（用户看到的「今天」）。
func (s *Store) Usage(ctx context.Context, since time.Time, loc *time.Location) (UsageSummary, error) {
	summary := UsageSummary{Since: since.UnixMilli()}
	from := since.UnixMilli()

	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MIN(at), 0) FROM usage_events`).Scan(&summary.FirstAt); err != nil {
		return summary, err
	}

	row := s.db.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN kind = 'model' THEN input END), 0),
		COALESCE(SUM(CASE WHEN kind = 'model' THEN output END), 0),
		COALESCE(SUM(CASE WHEN kind = 'model' THEN total END), 0),
		COALESCE(SUM(CASE WHEN kind = 'model' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN kind = 'model' THEN failed ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN kind = 'tool' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN kind = 'tool' THEN failed ELSE 0 END), 0),
		COUNT(DISTINCT session_id)
		FROM usage_events WHERE at >= ?`, from)
	totals := &summary.Totals
	if err := row.Scan(&totals.Input, &totals.Output, &totals.Total, &totals.ModelCalls, &totals.ModelFailed,
		&totals.ToolCalls, &totals.ToolFailed, &totals.Sessions); err != nil {
		return summary, err
	}

	days, err := s.usageDays(ctx, from, loc)
	if err != nil {
		return summary, err
	}
	summary.Days = fillDays(days, since, time.Now(), loc)

	if summary.Models, err = queryRows(ctx, s.db, `SELECT model, SUM(input), SUM(output), COUNT(*), SUM(failed)
		FROM usage_events WHERE kind = 'model' AND at >= ? GROUP BY model ORDER BY SUM(total) DESC, COUNT(*) DESC LIMIT 20`,
		from, func(rows *sql.Rows) (UsageModelRow, error) {
			var r UsageModelRow
			return r, rows.Scan(&r.Model, &r.Input, &r.Output, &r.Calls, &r.Failed)
		}); err != nil {
		return summary, err
	}
	if summary.Tools, err = queryRows(ctx, s.db, `SELECT tool, source, COUNT(*), SUM(failed), CAST(AVG(duration_ms) AS INTEGER)
		FROM usage_events WHERE kind = 'tool' AND at >= ? GROUP BY tool, source ORDER BY COUNT(*) DESC LIMIT 40`,
		from, func(rows *sql.Rows) (UsageToolRow, error) {
			var r UsageToolRow
			return r, rows.Scan(&r.Tool, &r.Source, &r.Calls, &r.Failed, &r.AvgMS)
		}); err != nil {
		return summary, err
	}
	if summary.Skills, err = queryRows(ctx, s.db, `SELECT detail, COUNT(*)
		FROM usage_events WHERE kind = 'tool' AND tool = 'load_skill' AND detail != '' AND failed = 0 AND at >= ?
		GROUP BY detail ORDER BY COUNT(*) DESC LIMIT 30`,
		from, func(rows *sql.Rows) (UsageSkillRow, error) {
			var r UsageSkillRow
			return r, rows.Scan(&r.Skill, &r.Uses)
		}); err != nil {
		return summary, err
	}
	if summary.Sessions, err = queryRows(ctx, s.db, `SELECT u.session_id, COALESCE(s.title, ''), SUM(u.total), COUNT(*)
		FROM usage_events u LEFT JOIN sessions s ON s.id = u.session_id
		WHERE u.kind = 'model' AND u.at >= ? AND u.session_id != ''
		GROUP BY u.session_id ORDER BY SUM(u.total) DESC LIMIT 10`,
		from, func(rows *sql.Rows) (UsageSessionRow, error) {
			var r UsageSessionRow
			return r, rows.Scan(&r.SessionID, &r.Title, &r.Total, &r.Calls)
		}); err != nil {
		return summary, err
	}
	return summary, nil
}

func queryRows[T any](ctx context.Context, db *sql.DB, query string, from int64, scan func(*sql.Rows) (T, error)) ([]T, error) {
	rows, err := db.QueryContext(ctx, query, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		value, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

// usageDays 按本地日期分组。SQLite 不知道用户在哪个时区，所以取出来在 Go 里分。
// 一天的记录是「每次模型调用、每次工具调用」一行，几个月也就几万行，扫一遍无所谓。
func (s *Store) usageDays(ctx context.Context, from int64, loc *time.Location) (map[string]*UsageDay, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT at, kind, input, output FROM usage_events WHERE at >= ?`, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	days := map[string]*UsageDay{}
	for rows.Next() {
		var at, input, output int64
		var kind string
		if err := rows.Scan(&at, &kind, &input, &output); err != nil {
			return nil, err
		}
		key := time.UnixMilli(at).In(loc).Format("2006-01-02")
		day := days[key]
		if day == nil {
			day = &UsageDay{Day: key}
			days[key] = day
		}
		switch UsageKind(kind) {
		case UsageModel:
			day.Input += input
			day.Output += output
			day.ModelCalls++
		case UsageTool:
			day.ToolCalls++
		}
	}
	return days, rows.Err()
}

// fillDays 把没有记录的日子补成 0：图表上断掉的一天和「那天没用」是两回事，要看得出来。
func fillDays(days map[string]*UsageDay, since, now time.Time, loc *time.Location) []UsageDay {
	start := since.In(loc)
	start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, loc)
	end := now.In(loc)
	out := []UsageDay{}
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		key := day.Format("2006-01-02")
		if found := days[key]; found != nil {
			out = append(out, *found)
		} else {
			out = append(out, UsageDay{Day: key})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day < out[j].Day })
	return out
}

// ToolSource 判断一个工具来自哪儿：MCP 工具名是「server__tool」。
func ToolSource(name string) string {
	if server, _, ok := strings.Cut(name, "__"); ok && server != "" {
		return "mcp:" + server
	}
	return "builtin"
}

// ensureUsageSchema 在已有的库上补上用量表（Open 调）。
func ensureUsageSchema(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, usageSchema); err != nil {
		return fmt.Errorf("初始化用量表失败：%w", err)
	}
	return nil
}
