package gormstore

import (
	"fmt"
	"strings"

	log "github.com/sirupsen/logrus"
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
		`CREATE VIRTUAL TABLE IF NOT EXISTS memory_items_fts USING fts5(
			summary,
			content,
			content=memory_items,
			content_rowid=id
		)`,
		`CREATE TRIGGER IF NOT EXISTS memory_items_fts_insert AFTER INSERT ON memory_items BEGIN
			INSERT INTO memory_items_fts(rowid, summary, content) VALUES (new.id, new.summary, new.content);
		END`,
		`CREATE TRIGGER IF NOT EXISTS memory_items_fts_update AFTER UPDATE OF summary, content ON memory_items BEGIN
			INSERT INTO memory_items_fts(memory_items_fts, rowid, summary, content) VALUES('delete', old.id, old.summary, old.content);
			INSERT INTO memory_items_fts(rowid, summary, content) VALUES (new.id, new.summary, new.content);
		END`,
		`CREATE TRIGGER IF NOT EXISTS memory_items_fts_delete AFTER DELETE ON memory_items BEGIN
			INSERT INTO memory_items_fts(memory_items_fts, rowid, summary, content) VALUES('delete', old.id, old.summary, old.content);
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

	var memoryCount int64
	sqlDB.QueryRow("SELECT COUNT(*) FROM memory_items_fts").Scan(&memoryCount)
	if memoryCount == 0 {
		if _, err := sqlDB.Exec(`INSERT INTO memory_items_fts(rowid, summary, content)
			SELECT id, summary, content FROM memory_items WHERE content IS NOT NULL AND content != ''`); err != nil {
			log.WithError(err).Warn("[FTS5] memory backfill failed")
		}
	}

	log.Info("[FTS5] full-text search initialized")
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
