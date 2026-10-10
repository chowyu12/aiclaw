package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

const searchFixture = `{"query":"AIClaw","provider":"fixture","results":[{"title":"AIClaw","url":"https://example.com/docs#one","snippet":"Official docs"},{"title":"Duplicate","url":"https://example.com/docs#two"},{"title":"Unsafe","url":"file:///tmp/private"}]}`

func searchServer(t *testing.T, failed bool) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var message struct {
			ID     *int64 `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&message)
		if message.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any = map[string]any{}
		switch message.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "serverInfo": map[string]string{"name": "search", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "web_search", "inputSchema": map[string]string{"type": "object"}}}}
		case "tools/call":
			result = map[string]any{"content": []any{map[string]string{"type": "text", "text": searchFixture}}, "isError": failed}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": *message.ID, "result": result})
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func TestSearchSourcesSurviveMCPCodeModeAndSaveLoad(t *testing.T) {
	for _, codeMode := range []bool{false, true} {
		for _, failed := range []bool{false, true} {
			t.Run(fmt.Sprintf("code=%v/failure=%v", codeMode, failed), func(t *testing.T) {
				name, args := "search__web_search", `{"query":"AIClaw"}`
				if codeMode {
					name, args = "exec", `{"code":"await tools.search__web_search({query:'AIClaw'}); text('finished')"}`
				}
				model := &fakeModel{script: []string{sseToolCall("search", name, args), sseText("answer")}}
				upstream := httptest.NewServer(http.HandlerFunc(model.handler))
				t.Cleanup(upstream.Close)
				s, err := New(context.Background(), "test", protocol.SessionStartParams{Model: protocol.ModelConfig{BaseURL: upstream.URL, Model: "fake"}, Workdir: t.TempDir(), ApprovalPolicy: protocol.ApprovalOnWrite, CodeMode: codeMode, MCPServers: map[string]protocol.MCPServerConfig{"search": {URL: searchServer(t, failed), Trusted: true}}}, StaticKey("sk-test"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(s.Close)
				s.RunTurn(context.Background(), "turn", "search", nil, nil, &recordingEmitter{approve: true})
				db := newTestStore(t)
				if err := s.Save(context.Background(), db); err != nil {
					t.Fatal(err)
				}
				loaded, err := Load(context.Background(), db, "test", StaticKey("sk-test"), nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(loaded.Close)
				found := false
				for _, item := range loaded.History() {
					if item.Kind != protocol.ItemToolCall || item.ToolName != name {
						continue
					}
					found = true
					var expected []protocol.SearchSource
					if !failed {
						expected = []protocol.SearchSource{{Title: "AIClaw", URL: "https://example.com/docs", Snippet: "Official docs"}}
					}
					if !reflect.DeepEqual(item.Sources, expected) {
						t.Fatalf("sources=%+v want=%+v result=%s", item.Sources, expected, item.ToolResult)
					}
				}
				if !found {
					t.Fatal("missing tool")
				}
			})
		}
	}
}

func TestHistorySourcesOnlyRecoverStructuredSearchResults(t *testing.T) {
	encoded, _ := json.Marshal(searchFixture)
	for _, output := range []string{searchFixture, string(encoded), "finished\n" + string(encoded)} {
		if got := historySources("exec", output, nil); len(got) != 1 || got[0].URL != "https://example.com/docs" {
			t.Fatalf("legacy sources=%v", got)
		}
	}
	for _, tool := range []string{"read_file", "agentMessage"} {
		if got := historySources(tool, searchFixture, nil); len(got) != 0 {
			t.Fatalf("unrelated tool sources=%v", got)
		}
	}
	for _, output := range []string{"https://example.com", `{"results":[{"url":"https://example.com"}]}`, "错误：" + searchFixture} {
		if got := historySources("search__web_search", output, nil); len(got) != 0 {
			t.Fatalf("invalid output sources=%v", got)
		}
	}
}
