package mcpclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStructuredCodeModeRetainsMediaAndErrorSemantics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var message struct {
			ID     *int64 `json:"id"`
			Method string `json:"method"`
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&message)
		if message.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any = map[string]any{}
		switch message.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "serverInfo": map[string]string{"name": "media", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": []any{}}
		case "tools/call":
			result = map[string]any{"content": []any{map[string]string{"type": "text", "text": "caption"}, map[string]string{"type": "image", "mimeType": "image/png", "data": "aGVsbG8="}}, "structuredContent": map[string]int{"count": 1}, "isError": message.Params.Name == "fail"}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": *message.ID, "result": result})
	}))
	defer server.Close()
	c, err := Start(context.Background(), Config{URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	plain, err := c.CallTool(context.Background(), "image", nil)
	if err != nil || plain != "caption" {
		t.Fatalf("direct compatibility: %q %v", plain, err)
	}
	structured, err := c.CallTool(WithStructuredOutput(context.Background()), "image", nil)
	if err != nil || !strings.Contains(structured, `"mimeType":"image/png"`) || !strings.Contains(structured, `"structuredContent"`) {
		t.Fatalf("media lost: %q %v", structured, err)
	}
	if _, err := c.CallTool(WithStructuredOutput(context.Background()), "fail", nil); err == nil {
		t.Fatal("isError became a successful structured result")
	}
}
