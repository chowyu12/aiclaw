package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChatReasoningStateSurvivesToolLoopWithoutLeakingToOtherProvider(t *testing.T) {
	for _, reasoning := range []string{"", "reasoning fixture"} {
		t.Run(reasoning, func(t *testing.T) {
			var requests []map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req map[string]any
				_ = json.NewDecoder(r.Body).Decode(&req)
				requests = append(requests, req)
				w.Header().Set("Content-Type", "text/event-stream")
				if len(requests) == 1 {
					fmt.Fprint(w, sse(fmt.Sprintf(`{"choices":[{"delta":{"reasoning_content":%q,"tool_calls":[{"index":0,"id":"c","function":{"name":"read_file","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`, reasoning)))
				} else {
					fmt.Fprint(w, sse(`{"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}`))
				}
			}))
			defer srv.Close()
			cfg := Config{ProviderID: 1, BaseURL: srv.URL, APIKey: "fixture"}
			client, _ := NewConfigured(cfg)
			req := Request{Model: "fixture", Messages: []Message{{Role: RoleUser, Content: "read"}}, Tools: []Tool{{Name: "read_file"}}}
			first, err := client.Stream(context.Background(), req, nil)
			if err != nil || first.ProviderState == nil || len(first.ToolCalls) != 1 {
				t.Fatal("provider reasoning not retained", first, err)
			}
			raw, _ := json.Marshal(Message{Role: RoleAssistant, ToolCalls: first.ToolCalls, ProviderState: first.ProviderState})
			var saved Message
			_ = json.Unmarshal(raw, &saved)
			req.Messages = append(req.Messages, saved, Message{Role: RoleTool, ToolCallID: "c", Content: "file"})
			client, _ = NewConfigured(cfg)
			if _, err = client.Stream(context.Background(), req, nil); err != nil {
				t.Fatal(err)
			}
			message := requests[1]["messages"].([]any)[1].(map[string]any)
			if value, ok := message["reasoning_content"]; !ok || value != reasoning {
				t.Fatal("reasoning field must survive, including explicit empty strings", message)
			}
			cfg.ProviderID = 2
			other, _ := NewConfigured(cfg)
			_, wire, _ := other.adapter.encode(other, req)
			raw, _ = json.Marshal(wire)
			var data map[string]any
			_ = json.Unmarshal(raw, &data)
			if _, ok := data["messages"].([]any)[1].(map[string]any)["reasoning_content"]; ok {
				t.Fatal("provider-private reasoning crossed provider IDs")
			}
		})
	}
}
