package store

import (
	"context"
	"database/sql"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/chowyu12/aiclaw/internal/i18n"
)

// The explicit title survives saves from stale checkpoints or resumed sessions.
func ensureManualTitleColumn(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(sessions)")
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid, notnull, pk int
		var name, kind string
		var def sql.NullString
		if err = rows.Scan(&cid, &name, &kind, &notnull, &def, &pk); err != nil {
			rows.Close()
			return err
		}
		found = found || name == "manual_title"
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if !found {
		_, err = db.ExecContext(ctx, "ALTER TABLE sessions ADD COLUMN manual_title TEXT NOT NULL DEFAULT ''")
	}
	return err
}

func NormalizeTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", i18n.E("会话名称不能为空")
	}
	if !utf8.ValidString(title) || utf8.RuneCountInString(title) > 200 {
		return "", i18n.E("会话名称最多 200 个字符")
	}
	if strings.ContainsFunc(title, unicode.IsControl) {
		return "", i18n.E("会话名称不能包含换行或控制字符")
	}
	return title, nil
}

func (s *Store) Rename(ctx context.Context, id, title string) (string, error) {
	title, err := NormalizeTitle(title)
	if err != nil {
		return "", err
	}
	// Do not rewrite history, configuration, or archive state, or reorder chats.
	result, err := s.db.ExecContext(ctx, "UPDATE sessions SET title = ?, manual_title = ? WHERE id = ?", title, title, id)
	if err != nil {
		return "", err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if n == 0 {
		return "", ErrNotFound
	}
	return title, nil
}
