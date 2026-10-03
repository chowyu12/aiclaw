package store

import (
	"context"
	"encoding/json"
	"github.com/chowyu12/aiclaw/internal/i18n"
	"time"
)

const workSchema = `
CREATE TABLE IF NOT EXISTS work_state (session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE, kind TEXT NOT NULL, body TEXT NOT NULL, PRIMARY KEY(session_id,kind));
CREATE TABLE IF NOT EXISTS submissions (session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE, request_id TEXT NOT NULL, fingerprint TEXT NOT NULL, turn_id TEXT NOT NULL, state TEXT NOT NULL, payload TEXT NOT NULL, created_at INTEGER NOT NULL, PRIMARY KEY(session_id,request_id));
CREATE TABLE IF NOT EXISTS transcript (session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE, message_id TEXT NOT NULL, body TEXT NOT NULL, sequence INTEGER PRIMARY KEY AUTOINCREMENT, UNIQUE(session_id,message_id));
`

func (s *Store) PutState(ctx context.Context, id, kind string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO work_state VALUES(?,?,?) ON CONFLICT(session_id,kind) DO UPDATE SET body=excluded.body`, id, kind, string(body))
	return err
}
func (s *Store) State(ctx context.Context, id, kind string, value any) error {
	var body string
	err := s.db.QueryRowContext(ctx, `SELECT body FROM work_state WHERE session_id=? AND kind=?`, id, kind).Scan(&body)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(body), value)
}

type Submission struct {
	RequestID   string          `json:"requestId"`
	TurnID      string          `json:"turnId"`
	State       string          `json:"state"`
	Payload     json.RawMessage `json:"payload"`
	CreatedAt   int64           `json:"createdAt"`
	Fingerprint string          `json:"-"`
}

func (s *Store) Submission(ctx context.Context, id, request string) (Submission, error) {
	var r Submission
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT request_id,turn_id,state,payload,created_at,fingerprint FROM submissions WHERE session_id=? AND request_id=?`, id, request).Scan(&r.RequestID, &r.TurnID, &r.State, &raw, &r.CreatedAt, &r.Fingerprint)
	r.Payload = json.RawMessage(raw)
	return r, err
}
func (s *Store) AcceptSubmission(ctx context.Context, id string, r Submission) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO submissions VALUES(?,?,?,?,?,?,?)`, id, r.RequestID, r.Fingerprint, r.TurnID, "queued", string(r.Payload), time.Now().UnixMilli())
	return err
}
func (s *Store) SetSubmission(ctx context.Context, id, request, turn, state string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE submissions SET turn_id=?,state=? WHERE session_id=? AND request_id=?`, turn, state, id, request)
	return err
}
func (s *Store) FinishSubmissions(ctx context.Context, id, turn, state string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE submissions SET state=? WHERE session_id=? AND turn_id=? AND state='running'`, state, id, turn)
	return err
}
func (s *Store) RecoverSubmissions(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE submissions SET state='uncertain' WHERE state='running'`); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE work_state SET body=json_set(body,'$.state','uncertain') WHERE kind='execution' AND json_extract(body,'$.state')='running'`); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE work_state SET body=json_set(body,'$.status','paused') WHERE kind='goal' AND json_extract(body,'$.status')='active'`)
	return err
}
func (s *Store) PendingSubmissions(ctx context.Context, id string) ([]Submission, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT request_id,turn_id,state,payload,created_at FROM submissions WHERE session_id=? AND state IN ('queued','uncertain') ORDER BY created_at`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Submission{}
	for rows.Next() {
		var r Submission
		var raw string
		if err = rows.Scan(&r.RequestID, &r.TurnID, &r.State, &raw, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.Payload = json.RawMessage(raw)
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) RecordMessage(ctx context.Context, id, messageID string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO transcript(session_id,message_id,body) VALUES(?,?,?)`, id, messageID, string(raw))
	return err
}
func (s *Store) Transcript(ctx context.Context, id string) ([]json.RawMessage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT body FROM transcript WHERE session_id=? ORDER BY sequence`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		out = append(out, json.RawMessage(raw))
	}
	return out, rows.Err()
}
func (s *Store) ResolveSubmission(ctx context.Context, id, request string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE submissions SET state='dismissed' WHERE session_id=? AND request_id=? AND state IN ('queued','uncertain')`, id, request)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return i18n.E("这条输入已不在待恢复状态")
	}
	return nil
}

func (s *Store) RelinkSubmission(ctx context.Context, id, request, turn string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE submissions SET turn_id=? WHERE session_id=? AND request_id=?`, turn, id, request)
	return err
}

type Execution struct {
	TurnID    string `json:"turnId"`
	State     string `json:"state"`
	StartedAt int64  `json:"startedAt"`
}

func (s *Store) HasRunningSubmission(ctx context.Context, id string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM submissions WHERE session_id=? AND state='running'`, id).Scan(&n)
	return n > 0, err
}
