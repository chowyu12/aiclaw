package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/chowyu12/aiclaw/internal/i18n"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unicode/utf8"
)

var fileChangeMu sync.Mutex

const maxSnapshotBytes = 4 * 1024 * 1024

type FileChange struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	TurnID    string `json:"turnId"`
	Before    string `json:"before"`
	After     string `json:"after"`
	Existed   bool   `json:"existed"`
	Mode      uint32 `json:"mode"`
	State     string `json:"state"`
	CreatedAt int64  `json:"createdAt"`
	Conflict  bool   `json:"conflict"`
}

func fileHash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func canonicalWritePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", err
	}
	path = filepath.Join(parent, filepath.Base(abs))
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return path, nil
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", i18n.E("只能查看或恢复普通文件")
	}
	return path, nil
}
func (s *Store) WriteTracked(ctx context.Context, id, turn, path string, data, expected []byte) error {
	fileChangeMu.Lock()
	defer fileChangeMu.Unlock()
	if len(data) > maxSnapshotBytes || !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return i18n.E("可记录的文本写入不能超过 4 MiB")
	}
	var err error
	path, err = canonicalWritePath(path)
	if err != nil {
		return err
	}
	if info, e := os.Stat(path); e == nil && info.Size() > maxSnapshotBytes {
		return i18n.E("原文件过大，无法保存快照")
	}
	before, err := os.ReadFile(path)
	existed := err == nil
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	mode := os.FileMode(0o644)
	if existed {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		mode = info.Mode().Perm()
	}
	if len(before) > maxSnapshotBytes || !utf8.Valid(before) || bytes.IndexByte(before, 0) >= 0 {
		return i18n.E("原文件不是可安全保存快照的文本")
	}
	if expected != nil && !bytes.Equal(before, expected) {
		return i18n.E("文件在读取后已变化，请重新读取后再编辑")
	}
	change := FileChange{ID: fmt.Sprintf("change_%d", time.Now().UnixNano()), Path: path, TurnID: turn, Before: string(before), After: string(data), Existed: existed, Mode: uint32(mode), State: "pending", CreatedAt: time.Now().UnixMilli()}
	if err = s.PutState(ctx, id, "change:"+change.ID, change); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = os.WriteFile(path, data, mode); err != nil {
		return err
	}
	change.State = "applied"
	return s.PutState(context.Background(), id, "change:"+change.ID, change)
}
func (s *Store) Changes(ctx context.Context, id string) ([]FileChange, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT body FROM work_state WHERE session_id=? AND kind LIKE 'change:%' ORDER BY kind DESC`, id)
	if err != nil {
		return nil, err
	}
	out := []FileChange{}
	for rows.Next() {
		var body string
		var c FileChange
		if err = rows.Scan(&body); err != nil {
			rows.Close()
			return nil, err
		}
		if err = json.Unmarshal([]byte(body), &c); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, c)
	}
	err = rows.Err()
	rows.Close()
	for i := range out {
		c := &out[i]
		canonical, ce := canonicalWritePath(c.Path)
		c.Conflict = true
		if ce != nil || canonical != c.Path {
			continue
		}
		info, e := os.Stat(c.Path)
		if e != nil {
			if c.State == "undoing" && !c.Existed && os.IsNotExist(e) {
				c.Conflict = false
			}
			continue
		}
		if info.Size() > maxSnapshotBytes {
			continue
		}
		data, e := os.ReadFile(c.Path)
		c.Conflict = e != nil || fileHash(data) != fileHash([]byte(c.After))
		if c.State == "undoing" && c.Existed && e == nil && bytes.Equal(data, []byte(c.Before)) {
			c.Conflict = false
		}
	}
	return out, err
}
func (s *Store) UndoChange(ctx context.Context, id, changeID string, validate func(string) error) error {
	fileChangeMu.Lock()
	defer fileChangeMu.Unlock()
	var c FileChange
	if err := s.State(ctx, id, "change:"+changeID, &c); err != nil {
		return err
	}
	if c.State == "undone" {
		return nil
	}
	if err := validate(c.Path); err != nil {
		return err
	}
	path, err := canonicalWritePath(c.Path)
	if err != nil {
		return err
	}
	if path != c.Path {
		return i18n.E("文件路径在保存快照后已变化")
	}
	data, err := os.ReadFile(path)
	if c.State == "undoing" && ((c.Existed && err == nil && bytes.Equal(data, []byte(c.Before))) || (!c.Existed && os.IsNotExist(err))) {
		c.State = "undone"
		return s.PutState(ctx, id, "change:"+changeID, c)
	}
	if err != nil {
		return err
	}
	if fileHash(data) != fileHash([]byte(c.After)) {
		return i18n.E("文件已有其他改动，已拒绝覆盖撤销")
	}
	c.State = "undoing"
	if err = s.PutState(ctx, id, "change:"+changeID, c); err != nil {
		return err
	}
	if c.Existed {
		err = os.WriteFile(path, []byte(c.Before), os.FileMode(c.Mode))
	} else {
		err = os.Remove(path)
	}
	if err != nil {
		return err
	}
	c.State = "undone"
	return s.PutState(context.Background(), id, "change:"+changeID, c)
}
