package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chowyu12/aiclaw/internal/model"
)

func TestReasoningPreferenceNegotiatesBeforeHTTPRequest(t *testing.T) {
	for _, tc := range []struct {
		name       string
		caps       model.ModelCapabilities
		preference bool
		want       string
		reject     bool
	}{
		{"unknown preference", model.ModelCapabilities{}, true, "", false},
		{"unsupported preference", model.ModelCapabilities{Reasoning: "unsupported"}, true, "", false},
		{"supported preference", model.ModelCapabilities{Reasoning: "supported"}, true, "medium", false},
		{"unsupported preference level", model.ModelCapabilities{Reasoning: "supported", ReasoningEfforts: "low,high"}, true, "", false},
		{"unsupported explicit", model.ModelCapabilities{Reasoning: "unsupported"}, false, "", true},
		{"unknown explicit", model.ModelCapabilities{}, false, "medium", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				if tc.want == "" && body["reasoning_effort"] != nil {
					t.Error("unsupported preference sent", body)
				}
				if tc.want != "" && body["reasoning_effort"] != tc.want {
					t.Error("explicit/supported value changed", body)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				w.Write([]byte(sse(`{"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}`)))
			}))
			defer srv.Close()
			c, _ := NewConfigured(Config{BaseURL: srv.URL, APIKey: "fixture", Capabilities: tc.caps})
			req := Request{Model: "fixture", ReasoningEffort: "medium", ReasoningPreference: tc.preference}
			_, err := c.Stream(context.Background(), req, nil)
			if (err != nil) != tc.reject || calls != map[bool]int{true: 0, false: 1}[tc.reject] || req.ReasoningEffort != "medium" {
				t.Fatal("policy mutated preference or retried", err, calls, req)
			}
		})
	}
}
