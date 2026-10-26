package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMCPNewToolIsDiscoveredWithinTurnInBothModes(t *testing.T) {
	for _, codeMode := range []bool{false, true} {
		t.Run(fmt.Sprint(codeMode), func(t *testing.T) {
			var changed atomic.Bool
			var newCalls atomic.Int32
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var q struct {
					ID     *int   `json:"id"`
					Method string `json:"method"`
					Params struct {
						Name string `json:"name"`
					} `json:"params"`
				}
				_ = json.NewDecoder(r.Body).Decode(&q)
				if q.ID == nil {
					w.WriteHeader(202)
					return
				}
				result := `{}`
				if q.Method == "tools/list" {
					name := "old"
					if changed.Load() {
						name = "new"
					}
					result = fmt.Sprintf(`{"tools":[{"name":%q,"description":"fixture","inputSchema":{"type":"object"}}]}`, name)
				}
				if q.Method == "tools/call" {
					if q.Params.Name == "old" {
						changed.Store(true)
					} else if q.Params.Name == "new" {
						newCalls.Add(1)
					}
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/tools/list_changed\"}\n\ndata: {\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"ok\"}]}}\n\n", *q.ID)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":%s}`, *q.ID, result)
			}))
			defer remote.Close()
			first, second := sseToolCall("one", "remote__old", `{}`), sseToolCall("two", "remote__new", `{}`)
			if codeMode {
				first = sseToolCall("one", "exec", `{"code":"return await tools.remote__old({});"}`)
				second = sseToolCall("two", "exec", `{"code":"return await tools.remote__new({});"}`)
			}
			model := &fakeModel{script: []string{first, second, sseText("done")}}
			endpoint := httptest.NewServer(http.HandlerFunc(model.handler))
			defer endpoint.Close()
			s, err := New(context.Background(), "refresh", protocol.SessionStartParams{Model: protocol.ModelConfig{BaseURL: endpoint.URL, Model: "fake"}, Workdir: t.TempDir(), ApprovalPolicy: protocol.ApprovalBypass, CodeMode: codeMode, MCPServers: map[string]protocol.MCPServerConfig{"remote": {URL: remote.URL}}}, StaticKey("fixture-key"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			e := &recordingEmitter{}
			s.RunTurn(ctx, "turn", "use the tools", nil, nil, e)
			if newCalls.Load() != 1 {
				t.Fatalf("new tool not invoked: %v", e.events)
			}
			if len(model.requests) != 3 {
				t.Fatal("unexpected model calls", len(model.requests))
			}
			definitions, _ := json.Marshal(model.requests[1]["tools"])
			if !strings.Contains(string(definitions), "remote__new") || strings.Contains(string(definitions), "remote__old") {
				t.Fatal("stale model catalog", string(definitions))
			}
			if !e.find("session/toolsUpdated", "remote__new") {
				t.Fatal("host catalog notification missing")
			}
			if len(s.mcpKeys) != 1 {
				t.Fatal("refresh borrowed another connection")
			}
		})
	}
}
