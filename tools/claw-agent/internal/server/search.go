package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/agent"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/store"
	"time"
)

type sessionSearch struct{ cancel context.CancelFunc }

func (s *Server) handleSessionSearch(parent context.Context, f frame) {
	var params protocol.SessionSearchParams
	if json.Unmarshal(f.Params, &params) != nil || len(params.RequestID) > 128 || len(params.Keyword) > 1024 {
		s.writeError(f.ID, codeInvalidParams, "invalid search")
		return
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	entry := &sessionSearch{cancel: cancel}
	key := "request:" + params.RequestID
	if params.RequestID == "" {
		key = fmt.Sprintf("legacy:%p", entry)
	}
	s.searchMu.Lock()
	if s.searches == nil {
		s.searches = map[string]*sessionSearch{}
	}
	cancelled := params.RequestID != "" && time.Now().Before(s.cancelledSearches[params.RequestID])
	if cancelled {
		delete(s.cancelledSearches, params.RequestID)
	}
	if s.searchRetired || cancelled || (s.searches[key] == nil && len(s.searches) >= 128) {
		s.searchMu.Unlock()
		s.writeError(f.ID, codeInvalidParams, "search unavailable")
		return
	}
	if old := s.searches[key]; old != nil {
		old.cancel()
	}
	s.searches[key] = entry
	s.searchMu.Unlock()
	defer func() {
		s.searchMu.Lock()
		if s.searches[key] == entry {
			delete(s.searches, key)
		}
		s.searchMu.Unlock()
	}()
	var partial func([]store.Summary)
	if params.RequestID != "" {
		partial = func(items []store.Summary) {
			if ctx.Err() != nil {
				return
			}
			(&emitter{server: s}).Notify("session/searchPartial", map[string]any{"requestId": params.RequestID, "keyword": params.Keyword, "sessions": searchSummaries(items)})
		}
	}
	found, err := s.db.SearchStream(ctx, params.Keyword, 50, partial)
	if err != nil {
		s.writeError(f.ID, codeInternal, err.Error())
		return
	}
	s.writeResult(f.ID, map[string]any{"sessions": searchSummaries(found)})
}
func searchSummaries(items []store.Summary) []agent.Summary {
	out := make([]agent.Summary, 0, len(items))
	for _, item := range items {
		out = append(out, agent.Summary{ID: item.ID, Title: item.Title, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt, Workdir: item.Workdir, TurnCount: item.TurnCount, Model: item.Model, Snippet: item.Snippet, ParentID: item.ParentID})
	}
	return out
}
