package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/chowyu12/aiclaw/internal/model"
)

func TestInvalidHistoricalBatchContinuesAcrossProtocols(t *testing.T) {
	for _, api := range []model.APIProtocol{model.APIChatCompletions, model.APIResponses, model.APIAnthropic, model.APIGemini} {
		for _, withTools := range []bool{false, true} {
			t.Run(string(api)+map[bool]string{false: "/summary", true: "/continue"}[withTools], func(t *testing.T) {
				calls := 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					raw, _ := json.Marshal(body)
					for _, evidence := range []string{"original answer", "original-result", "truncated", "continue safely"} {
						if !strings.Contains(string(raw), evidence) {
							t.Error("historical evidence lost", evidence)
						}
					}
					var inspect func(any)
					inspect = func(value any) {
						switch v := value.(type) {
						case map[string]any:
							for key, child := range v {
								switch key {
								case "tool_calls", "tool_call_id", "tool_use_id", "functionCall", "functionResponse", "call_id":
									t.Errorf("unreplayable protocol object %s", key)
								}
								inspect(child)
							}
						case []any:
							for _, child := range v {
								inspect(child)
							}
						}
					}
					inspect(body)
					w.Header().Set("Content-Type", "text/event-stream")
					switch api {
					case model.APIAnthropic:
						w.Write([]byte(claudeTextStream))
					case model.APIGemini:
						w.Write([]byte(geminiTextStream))
					case model.APIResponses:
						w.Write([]byte(responsesCompleted(responsesTextOutput)))
					default:
						w.Write([]byte(sse(`{"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}`)))
					}
				}))
				defer srv.Close()
				c, _ := NewConfigured(Config{BaseURL: srv.URL, APIKey: "fixture", Protocol: api})
				messages := []Message{{Role: RoleUser, Content: "write"}, {Role: RoleAssistant, Content: "original answer", ToolCalls: []ToolCall{{ID: "old", Name: "write_file", Arguments: `{"path":"out","text":"cut`}}}, {Role: RoleTool, ToolCallID: "old", Content: "original-result: arguments truncated"}, {Role: RoleUser, Content: "continue safely"}}
				before, _ := json.Marshal(messages)
				req := Request{Model: "fixture", Messages: messages}
				if withTools {
					req.Tools = []Tool{{Name: "read_file"}}
				}
				response, err := c.Stream(context.Background(), req, nil)
				after, _ := json.Marshal(messages)
				if err != nil || response.Content != "done" || calls != 1 || string(before) != string(after) {
					t.Fatalf("history did not recover without mutation: %+v %v calls=%d", response, err, calls)
				}
			})
		}
	}
}

func TestHistoryNormalizationKeepsValidBatchesAndRawEvidence(t *testing.T) {
	validState := &ProviderState{Model: "fixture", Output: []json.RawMessage{json.RawMessage(`{"type":"reasoning"}`)}}
	valid := []Message{{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "reused", Name: "read_file", Arguments: `{}`}}, ProviderState: validState}, {Role: RoleTool, ToolCallID: "reused", Content: "valid result"}}
	if !reflect.DeepEqual(valid, normaliseHistory(valid)) {
		t.Fatal("valid history changed")
	}
	bad := Message{Role: RoleAssistant, Content: "before", ToolCalls: []ToolCall{{ID: "reused", Name: "write_file", Arguments: `{"cut":`}, {ID: "parallel", Name: "read_file", Arguments: `{}`}}, ProviderState: validState}
	history := append([]Message{bad, {Role: RoleTool, ToolCallID: "reused", Content: "bad result"}, {Role: RoleTool, ToolCallID: "parallel", Content: "completed parallel result"}}, valid...)
	wire := normaliseHistory(history)
	if len(wire) != 3 || wire[0].ProviderState != nil || len(wire[0].ToolCalls) != 0 || !strings.Contains(wire[0].Content, "completed parallel result") || !reflect.DeepEqual(wire[1:], valid) {
		t.Fatal("batch association or valid later history was lost", wire)
	}
	if len(history[0].ToolCalls) != 2 || history[0].ProviderState != validState {
		t.Fatal("source history changed")
	}
}
