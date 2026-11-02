package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chowyu12/aiclaw/internal/model"
)

func nativeEvents(events ...string) string {
	return "data: " + strings.Join(events, "\n\ndata: ") + "\n\n"
}

var claudeToolStream = nativeEvents(
	`{"type":"message_start","message":{"usage":{"input_tokens":10,"cache_read_input_tokens":30,"cache_creation_input_tokens":20,"output_tokens":1}}}`,
	`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
	`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"summary"}}`,
	`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"claude-signature"}}`,
	`{"type":"content_block_stop","index":0}`,
	`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"c1","name":"read_file","input":{}}}`,
	`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}`,
	`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"sample.txt\"}"}}`,
	`{"type":"content_block_stop","index":1}`,
	`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"c2","name":"read_file","input":{"path":"second.txt"}}}`,
	`{"type":"content_block_stop","index":2}`,
	`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}`,
	`{"type":"message_stop"}`,
)

var claudeTextStream = nativeEvents(
	`{"type":"message_start","message":{"usage":{"input_tokens":60,"output_tokens":1}}}`,
	`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
	`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"done"}}`,
	`{"type":"content_block_stop","index":0}`,
	`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}`,
	`{"type":"message_stop"}`,
)

var geminiToolStream = nativeEvents(`{"candidates":[{"content":{"role":"model","parts":[{"thought":true,"text":"summary"},{"functionCall":{"id":"c1","name":"read_file","args":{"path":"sample.txt"}},"thoughtSignature":"gemini-signature"},{"functionCall":{"id":"c2","name":"read_file","args":{"path":"second.txt"}}}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":60,"candidatesTokenCount":3,"thoughtsTokenCount":4,"totalTokenCount":67}}`)
var geminiTextStream = nativeEvents(`{"candidates":[{"content":{"parts":[{"text":"done"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":60,"candidatesTokenCount":7,"totalTokenCount":67}}`)

func TestNativeProtocolsRoundTripParallelToolsAndSignedHistory(t *testing.T) {
	for _, tc := range []struct {
		api                          model.APIProtocol
		path, first, last, signature string
	}{
		{model.APIAnthropic, "/v1/messages", claudeToolStream, claudeTextStream, "claude-signature"},
		{model.APIGemini, "/v1/models/fixture:streamGenerateContent", geminiToolStream, geminiTextStream, "gemini-signature"},
	} {
		t.Run(string(tc.api), func(t *testing.T) {
			var requests []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.path || r.Method != "POST" || r.Header.Get("Authorization") != "" || strings.Contains(r.URL.String(), "fixture-key") {
					t.Error("wrong native request", r.URL.Path)
				}
				if tc.api == model.APIAnthropic && (r.Header.Get("x-api-key") != "fixture-key" || r.Header.Get("anthropic-version") != "2023-06-01") {
					t.Error("missing Claude auth/version")
				}
				if tc.api == model.APIGemini && (r.Header.Get("x-goog-api-key") != "fixture-key" || r.URL.Query().Get("alt") != "sse") {
					t.Error("missing Gemini auth/SSE")
				}
				var body json.RawMessage
				_ = json.NewDecoder(r.Body).Decode(&body)
				requests = append(requests, string(body))
				w.Header().Set("Content-Type", "text/event-stream")
				if len(requests) == 1 {
					fmt.Fprint(w, tc.first)
				} else {
					fmt.Fprint(w, tc.last)
				}
			}))
			defer srv.Close()
			cfg := Config{BaseURL: srv.URL + "/v1", APIKey: "fixture-key", Protocol: tc.api, Timeout: time.Second}
			client, err := NewConfigured(cfg)
			if err != nil {
				t.Fatal(err)
			}
			req := Request{Model: "fixture", ReasoningEffort: "high", Messages: []Message{{Role: RoleSystem, Content: "system"}, {Role: RoleUser, Content: "read both files", Images: [][]byte{{1, 2, 3}}}}, Tools: []Tool{{Name: "read_file", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)}}}
			var thought strings.Builder
			first, err := client.Stream(context.Background(), req, func(d Delta) { thought.WriteString(d.Reasoning) })
			if err != nil || len(first.ToolCalls) != 2 || first.ToolCalls[0].ID != "c1" || first.ToolCalls[1].ID != "c2" || first.Usage.InputTokens != 60 || first.Usage.OutputTokens != 7 || first.Usage.TotalTokens != 67 || thought.String() != "summary" {
				t.Fatalf("bad native tool response: %+v %v", first, err)
			}
			if first.ToolCalls[0].Arguments != `{"path":"sample.txt"}` {
				t.Fatal("fragmented tool args lost", first.ToolCalls)
			}
			raw, _ := json.Marshal(Message{Role: RoleAssistant, ToolCalls: first.ToolCalls, ProviderState: first.ProviderState})
			var restored Message
			_ = json.Unmarshal(raw, &restored)
			req.Messages = append(req.Messages, restored, Message{Role: RoleTool, ToolCallID: "c1", Content: "result-one"}, Message{Role: RoleTool, ToolCallID: "c2", Content: "result-two"})
			client, _ = NewConfigured(cfg)
			var text strings.Builder
			final, err := client.Stream(context.Background(), req, func(d Delta) { text.WriteString(d.Content) })
			if err != nil || final.Content != "done" || text.String() != "done" || len(final.ToolCalls) != 0 {
				t.Fatalf("bad final response %+v %v", final, err)
			}
			if len(requests) != 2 || strings.Count(requests[1], tc.signature) != 1 || !strings.Contains(requests[1], "result-one") || !strings.Contains(requests[1], "result-two") || !strings.Contains(requests[0], "AQID") {
				t.Fatal("lost or duplicated signed history/image", requests)
			}
			if tc.api == model.APIAnthropic && !strings.Contains(requests[0], `"effort":"high"`) {
				t.Fatal("Claude effort not mapped")
			}
			if tc.api == model.APIGemini && !strings.Contains(requests[0], `"thinkingLevel":"HIGH"`) {
				t.Fatal("Gemini effort not mapped")
			}
			for _, other := range []Config{{BaseURL: cfg.BaseURL, APIKey: cfg.APIKey, Protocol: model.APIResponses}, {BaseURL: "https://elsewhere.invalid", APIKey: cfg.APIKey, Protocol: tc.api}} {
				c, _ := NewConfigured(other)
				_, wire, err := c.adapter.encode(c, req)
				if err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(wire)
				if strings.Contains(string(raw), tc.signature) {
					t.Fatal("signature escaped its origin")
				}
			}
			req.Model = "different"
			_, wire, _ := client.adapter.encode(client, req)
			raw, _ = json.Marshal(wire)
			if strings.Contains(string(raw), tc.signature) {
				t.Fatal("signature escaped its model")
			}
		})
	}
}

func TestNativeStreamsDoNotCommitUnfinishedTools(t *testing.T) {
	for _, tc := range []struct {
		api  model.APIProtocol
		body string
	}{
		{model.APIAnthropic, strings.TrimSuffix(claudeToolStream, nativeEvents(`{"type":"message_stop"}`))},
		{model.APIAnthropic, nativeEvents(`{"type":"message_start"}`, `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`, `{"type":"content_block_stop","index":0}`, `{"type":"message_delta","delta":{"stop_reason":"max_tokens"}}`, `{"type":"message_stop"}`)},
		{model.APIGemini, nativeEvents(`{"candidates":[{"content":{"parts":[{"text":"partial"},{"functionCall":{"name":"write_file","args":{}}}]}}]}`)},
		{model.APIGemini, strings.ReplaceAll(geminiToolStream, `"STOP"`, `"MAX_TOKENS"`)},
	} {
		t.Run(string(tc.api)+tc.body[len(tc.body)/2:len(tc.body)/2+3], func(t *testing.T) {
			response, err := adapters[tc.api].decode(strings.NewReader(tc.body), nil)
			if err == nil || len(response.ToolCalls) != 0 || response.ProviderState != nil {
				t.Fatalf("unfinished tool calls committed: %+v %v", response, err)
			}
			if strings.Contains(tc.body, `"text":"partial"`) && response.Content != "partial" {
				t.Fatal("partial visible text lost")
			}
		})
	}
	var apiErr *Error
	_, err := consumeAnthropic(strings.NewReader(nativeEvents(`{"type":"error","error":{"type":"invalid_request_error","message":"bad parameter"}}`)), nil)
	if !errors.As(err, &apiErr) || apiErr.Retryable() {
		t.Fatal("native parameter error should not retry", err)
	}
}

func TestNativeCancellationAndCredentialRedirects(t *testing.T) {
	for _, api := range []model.APIProtocol{model.APIAnthropic, model.APIGemini, model.APIResponses, model.APIChatCompletions} {
		t.Run(string(api), func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.Redirect(w, r, "/elsewhere", 302) }))
			defer srv.Close()
			c, _ := NewConfigured(Config{BaseURL: srv.URL, APIKey: "secret", Protocol: api, Timeout: time.Second})
			_, err := c.Stream(context.Background(), Request{Model: "fixture"}, nil)
			var apiErr *Error
			if !errors.As(err, &apiErr) || apiErr.Status != 302 || calls != 1 {
				t.Fatal("credential redirect followed", calls, err)
			}
			blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer blocked.Close()
			c, _ = NewConfigured(Config{BaseURL: blocked.URL, APIKey: "secret", Protocol: api, Timeout: time.Second})
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			response, err := c.Stream(ctx, Request{Model: "fixture"}, nil)
			if err == nil || len(response.ToolCalls) != 0 || ctx.Err() == nil {
				t.Fatal("native stream cancellation failed", err)
			}
		})
	}
}
