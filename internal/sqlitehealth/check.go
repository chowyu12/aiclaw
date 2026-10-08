// Package sqlitehealth checks authoritative local databases without rebuilding them.
package sqlitehealth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/chowyu12/aiclaw/internal/i18n"
	log "github.com/sirupsen/logrus"
)

type corruptionError struct{ reason string }

func (e *corruptionError) Error() string { return e.reason }

// IsCorruption uses SQLite primary result codes, never error-text matching.
func IsCorruption(err error) bool {
	var corrupt *corruptionError
	if errors.As(err, &corrupt) {
		return true
	}
	var coded interface{ Code() int }
	return errors.As(err, &coded) && (coded.Code()&255 == 11 || coded.Code()&255 == 26)
}

// Check limits startup scanning. A locked or interrupted check is incomplete,
// not evidence of damage; the ordinary startup queries still surface failures.
func Check(parent context.Context, db *sql.DB) error {
	ctx, cancel := context.WithTimeout(parent, 250*time.Millisecond)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		return checkError(ctx, err)
	}
	defer conn.Close()
	// The driver defaults to a five-second lock wait, which outlives context
	// cancellation. Bound that wait on this connection and restore its setting.
	var busyTimeout int
	if err := conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		return checkError(ctx, err)
	}
	if busyTimeout > 100 {
		if _, err := conn.ExecContext(ctx, "PRAGMA busy_timeout(100)"); err != nil {
			return checkError(ctx, err)
		}
		defer conn.ExecContext(context.Background(), fmt.Sprintf("PRAGMA busy_timeout(%d)", busyTimeout))
	}
	var result string
	err = conn.QueryRowContext(ctx, "PRAGMA quick_check(1)").Scan(&result)
	if err != nil {
		return checkError(ctx, err)
	}
	if result != "ok" {
		return &corruptionError{reason: result}
	}
	return nil
}

func checkError(ctx context.Context, err error) error {
	if IsCorruption(err) {
		return err
	}
	var coded interface{ Code() int }
	if ctx.Err() != nil || (errors.As(err, &coded) && (coded.Code()&255 == 5 || coded.Code()&255 == 6 || coded.Code()&255 == 9)) {
		log.Warn("SQLite startup integrity check incomplete (locked or interrupted)")
		return nil
	}
	return err
}

func BackupDir(path string) string { return path + ".recovery" }

// Preserve is called after closing the database. Copy the main file and any
// journals together; keep originals untouched and do not claim a usable backup.
// Callers without an independent source of truth must fail startup, not rebuild.
func Preserve(path string, err error) error {
	if !IsCorruption(err) {
		return err
	}
	backup, backupErr := preserveFiles(path)
	if backupErr != nil {
		return fmt.Errorf("%s: %w (%s)", i18n.D("数据库损坏，原文件已保留；备份失败，请关闭应用后检查恢复"), err, backupErr)
	}
	return fmt.Errorf("%s: %w", i18n.D("数据库损坏，原文件已保留，备份位于 {path}；请关闭应用后从有效备份恢复", "path", backup), err)
}

func preserveFiles(path string) (string, error) {
	root := BackupDir(path)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(root, "corrupt-")
	if err != nil {
		return "", err
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		source, err := os.Open(path + suffix)
		if suffix != "" && errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return dir, err
		}
		info, err := source.Stat()
		if err != nil || !info.Mode().IsRegular() {
			source.Close()
			return dir, fmt.Errorf("backup source is not a regular file: %s", path+suffix)
		}
		dest, err := os.OpenFile(filepath.Join(dir, filepath.Base(path)+suffix), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			source.Close()
			return dir, err
		}
		_, copyErr := io.Copy(dest, source)
		if copyErr == nil {
			copyErr = dest.Sync()
		}
		closeErr := dest.Close()
		source.Close()
		if copyErr != nil {
			return dir, copyErr
		}
		if closeErr != nil {
			return dir, closeErr
		}
	}
	return dir, nil
}
