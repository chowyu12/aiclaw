package gormstore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/tools/sessionsearch"
)

// InitFTS5 创建 FTS5 虚拟表和触发器，仅 SQLite 有效。
func (s *GormStore) InitFTS5() {
	sqlDB, err := s.db.DB()
	if err != nil {
		log.WithError(err).Warn("[FTS5] get underlying db failed")
		return
	}

	// 检查是否是 SQLite
	var driverName string
	if sqlDB != nil {
		driverName = fmt.Sprintf("%T", s.db.Dialector)
	}
	if !strings.Contains(strings.ToLower(driverName), "sqlite") {
		log.Debug("[FTS5] not SQLite, skipping FTS5 setup")
		return
	}

	stmts := []string{
		`CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
			content,
			content=messages,
			content_rowid=id
		)`,
		`CREATE TRIGGER IF NOT EXISTS messages_fts_insert AFTER INSERT ON messages BEGIN
			INSERT INTO messages_fts(rowid, content) VALUES (new.id, new.content);
		END`,
		`CREATE TRIGGER IF NOT EXISTS messages_fts_delete AFTER DELETE ON messages BEGIN
			INSERT INTO messages_fts(messages_fts, rowid, content) VALUES('delete', old.id, old.content);
		END`,
	}

	for _, stmt := range stmts {
		if err := sqlDB.Ping(); err != nil {
			log.WithError(err).Warn("[FTS5] db ping failed, skipping")
			return
		}
		if _, err := sqlDB.Exec(stmt); err != nil {
			if !strings.Contains(err.Error(), "already exists") {
				log.WithError(err).Debug("[FTS5] exec statement failed (may already exist)")
			}
		}
	}

	// 回填已有数据
	var count int64
	sqlDB.QueryRow("SELECT COUNT(*) FROM messages_fts").Scan(&count)
	if count == 0 {
		if _, err := sqlDB.Exec(`INSERT INTO messages_fts(rowid, content)
			SELECT id, content FROM messages WHERE content IS NOT NULL AND content != ''`); err != nil {
			log.WithError(err).Warn("[FTS5] backfill failed")
		} else {
			var filled int64
			sqlDB.QueryRow("SELECT COUNT(*) FROM messages_fts").Scan(&filled)
			log.WithField("rows", filled).Info("[FTS5] backfill completed")
		}
	}

	log.Info("[FTS5] full-text search initialized")
}

// SearchMessages 实现 sessionsearch.MessageSearcher 接口。
func (s *GormStore) SearchMessages(ctx context.Context, query string, limit int) ([]sessionsearch.MessageSearchResult, error) {
	sqlDB, err := s.db.DB()
	if err != nil {
		return nil, fmt.Errorf("get db: %w", err)
	}

	// 尝试 FTS5
	results, err := s.searchFTS5(sqlDB, query, limit)
	if err == nil {
		return results, nil
	}

	// FTS5 不可用时用 LIKE 降级
	log.WithError(err).Debug("[FTS5] search failed, fallback to LIKE")
	return s.searchLIKE(sqlDB, query, limit)
}

func (s *GormStore) searchFTS5(sqlDB *sql.DB, query string, limit int) ([]sessionsearch.MessageSearchResult, error) {
	safeQuery := sanitizeFTS5Query(query)

	rows, err := sqlDB.Query(`
		SELECT m.id, m.conversation_id, m.role, m.content, m.created_at,
		       c.uuid AS conv_uuid, c.title
		FROM messages_fts
		JOIN messages m ON m.id = messages_fts.rowid
		JOIN conversations c ON c.id = m.conversation_id
		WHERE messages_fts MATCH ?
		ORDER BY rank
		LIMIT ?
	`, safeQuery, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanResults(rows)
}

func (s *GormStore) searchLIKE(sqlDB *sql.DB, query string, limit int) ([]sessionsearch.MessageSearchResult, error) {
	pattern := "%" + query + "%"
	rows, err := sqlDB.Query(`
		SELECT m.id, m.conversation_id, m.role, m.content, m.created_at,
		       c.uuid AS conv_uuid, c.title
		FROM messages m
		JOIN conversations c ON c.id = m.conversation_id
		WHERE m.content LIKE ?
		ORDER BY m.id DESC
		LIMIT ?
	`, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanResults(rows)
}

func scanResults(rows *sql.Rows) ([]sessionsearch.MessageSearchResult, error) {
	var results []sessionsearch.MessageSearchResult
	for rows.Next() {
		var (
			id, convID                int64
			role, content, convUUID   string
			title                     sql.NullString
			createdAt                 time.Time
		)
		if err := rows.Scan(&id, &convID, &role, &content, &createdAt, &convUUID, &title); err != nil {
			continue
		}
		t := "(无标题)"
		if title.Valid && title.String != "" {
			t = title.String
		}

		snippet := content
		if len(snippet) > 200 {
			snippet = snippet[:200] + "..."
		}

		results = append(results, sessionsearch.MessageSearchResult{
			ConversationUUID: convUUID,
			Title:            t,
			Role:             role,
			Snippet:          snippet,
			CreatedAt:        createdAt.Format(time.DateTime),
		})
	}
	return results, rows.Err()
}

func sanitizeFTS5Query(query string) string {
	// 移除 FTS5 特殊字符
	replacer := strings.NewReplacer(
		`"`, ` `,
		`(`, ` `,
		`)`, ` `,
		`+`, ` `,
		`{`, ` `,
		`}`, ` `,
		`^`, ` `,
	)
	sanitized := replacer.Replace(query)

	// 用双引号包裹每个词以精确匹配
	words := strings.Fields(sanitized)
	if len(words) == 0 {
		return query
	}

	var quoted []string
	for _, w := range words {
		w = strings.TrimSpace(w)
		if w == "" {
			continue
		}
		upper := strings.ToUpper(w)
		if upper == "AND" || upper == "OR" || upper == "NOT" {
			quoted = append(quoted, w)
			continue
		}
		quoted = append(quoted, `"`+w+`"`)
	}
	return strings.Join(quoted, " OR ")
}
