package server

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/store"
)

func (s *Server) handleSessionRename(ctx context.Context, f frame) {
	var p protocol.SessionRenameParams
	if err := json.Unmarshal(f.Params, &p); err != nil || p.SessionID == "" {
		s.writeError(f.ID, codeInvalidParams, "invalid params")
		return
	}
	if _, err := store.NormalizeTitle(p.Title); err != nil {
		s.writeError(f.ID, codeInvalidParams, err.Error())
		return
	}
	// Serialize with restoration so a just-loaded session also sees the new name.
	s.sessionTitleMu.Lock()
	defer s.sessionTitleMu.Unlock()
	var name string
	var err error
	if session := s.session(p.SessionID); session != nil {
		name, err = session.Rename(ctx, s.db, p.Title)
	} else {
		name, err = s.db.Rename(ctx, p.SessionID, p.Title)
	}
	if err != nil {
		code := codeInternal
		if errors.Is(err, store.ErrNotFound) {
			code = codeInvalidParams
		}
		s.writeError(f.ID, code, err.Error())
		return
	}
	s.writeResult(f.ID, map[string]string{"sessionId": p.SessionID, "title": name})
}
