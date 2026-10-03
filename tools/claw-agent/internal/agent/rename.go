package agent

import (
	"context"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/store"
)

// Rename updates durable metadata and the live title without stopping a turn.
func (s *Session) Rename(ctx context.Context, db *store.Store, title string) (string, error) {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	if s.deleted {
		return "", store.ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	name, err := db.Rename(ctx, s.ID, title)
	if err == nil {
		s.Title = name
	}
	return name, err
}
