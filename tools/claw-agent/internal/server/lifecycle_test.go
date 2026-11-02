package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestRetiredSessionCannotReturnAfterColdMount(t *testing.T) {
	for _, action := range []string{"archive", "delete", "shutdown"} {
		t.Run(action, func(t *testing.T) {
			s, root := collabServer(t, &scriptedModel{})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := root.Save(ctx, s.db); err != nil {
				t.Fatal(err)
			}
			s.unload(root.ID)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			mcp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					ID     *int64 `json:"id"`
					Method string `json:"method"`
				}
				_ = json.NewDecoder(r.Body).Decode(&req)
				if req.Method == "initialize" {
					once.Do(func() { close(entered) })
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
				if req.ID == nil {
					w.WriteHeader(202)
					return
				}
				result := `{"tools":[]}`
				if req.Method == "initialize" {
					result = `{"protocolVersion":"2025-03-26","capabilities":{},"serverInfo":{"name":"fixture","version":"1"}}`
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":%s}`, *req.ID, result)
			}))
			defer mcp.Close()
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			s.out = &receiptWriter{}
			params, _ := json.Marshal(protocol.SessionResumeParams{SessionID: root.ID, Refresh: &protocol.SessionRefresh{MCPServers: map[string]protocol.MCPServerConfig{"slow": {URL: mcp.URL}}}})
			done := make(chan struct{})
			go func() { s.handleSessionResume(ctx, frame{ID: json.RawMessage(`1`), Params: params}); close(done) }()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			switch action {
			case "archive":
				if _, err := s.archiveSessionTree(ctx, root.ID, true); err != nil {
					t.Fatal(err)
				}
			case "delete":
				if _, err := s.deleteSessionTree(ctx, root.ID); err != nil {
					t.Fatal(err)
				}
			case "shutdown":
				s.closeAll()
			}
			close(release)
			select {
			case <-done:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}

			if s.session(root.ID) != nil {
				t.Error("cold resume remounted a thread after archive completed")
			}
		})
	}
}
