package llm

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/chowyu12/aiclaw/internal/model"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

const toolsNeedResponses = "Function tools with reasoning_effort are not supported for gpt-6-astra in /v1/chat/completions. To use function tools, use /v1/responses or set reasoning_effort to 'none'."
const responsesToolOutput = `[{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"opaque-fixture"},{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read_file","arguments":"{\"path\":\"sample.txt\"}","status":"completed"}]`
const responsesTextOutput = `[{"type":"message","id":"msg_1","role":"assistant","status":"completed","phase":"final_answer","content":[{"type":"output_text","text":"done","annotations":[]}]}]`

func responsesCompleted(output string) string {
	return `data: {"type":"response.completed","response":{"status":"completed","output":` + output + `,"usage":{"input_tokens":100,"output_tokens":20,"total_tokens":120}}}` + "\n\n"
}

func TestResponsesPreservesReasoningAndReplaysAfterRestore(t *testing.T) {
	var paths []string
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("authorization missing")
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		paths = append(paths, r.URL.Path)
		bodies = append(bodies, body)
		if r.URL.Path != "/v1/responses" {
			t.Error("wrong configured protocol", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if len(paths) == 1 {
			_, _ = w.Write([]byte(responsesCompleted(responsesToolOutput)))
		} else {
			_, _ = w.Write([]byte(`data: {"type":"response.output_text.delta","delta":"done"}` + "\n\n" + responsesCompleted(responsesTextOutput)))
		}
	}))
	defer srv.Close()
	client, _ := NewConfigured(Config{BaseURL: srv.URL + "/v1", APIKey: "fixture", Protocol: model.APIResponses, Timeout: time.Second})
	temperature := 0.7
	req := Request{Model: "gpt-6-astra", ReasoningEffort: "high", MaxTokens: 4096, Temperature: &temperature,
		Messages: []Message{{Role: RoleSystem, Content: "system"}, {Role: RoleUser, Content: "read", Images: [][]byte{{1, 2, 3}}}},
		Tools:    []Tool{{Name: "read_file", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)}},
	}
	first, err := client.Stream(context.Background(), req, nil)
	if err != nil || len(first.ToolCalls) != 1 || first.ToolCalls[0].ID != "call_1" {
		t.Fatalf("fallback must return the original function call: %+v %v", first, err)
	}
	// Simulate persisted history and a fresh client after a cold restore.
	message := Message{Role: RoleAssistant, ToolCalls: first.ToolCalls, ProviderState: first.ProviderState}
	raw, _ := json.Marshal(message)
	var restored Message
	if err = json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	req.Messages = append(req.Messages, restored, Message{Role: RoleTool, ToolCallID: "call_1", Content: "file contents"})
	client, _ = NewConfigured(Config{BaseURL: srv.URL + "/v1", APIKey: "fixture", Protocol: model.APIResponses, Timeout: time.Second})
	var text strings.Builder
	second, err := client.Stream(context.Background(), req, func(d Delta) { text.WriteString(d.Content) })
	if err != nil || second.Content != "done" || text.String() != "done" || second.Usage.TotalTokens != 120 {
		t.Fatalf("bad final answer: %+v %v, streamed %q", second, err, text.String())
	}
	if !reflect.DeepEqual(paths, []string{"/v1/responses", "/v1/responses"}) {
		t.Fatal("restored session must use Responses directly", paths)
	}
	wire := bodies[0]
	if wire["reasoning"].(map[string]any)["effort"] != "high" || wire["store"] != false || wire["max_output_tokens"] != float64(4096) || wire["reasoning_effort"] != nil || wire["temperature"] != 0.7 {
		t.Fatal("reasoning or request contract was changed", wire)
	}
	tool := wire["tools"].([]any)[0].(map[string]any)
	if tool["strict"] != false || tool["function"] != nil || tool["name"] != "read_file" {
		t.Fatal("tool schema not converted", tool)
	}
	image := wire["input"].([]any)[1].(map[string]any)["content"].([]any)[1].(map[string]any)
	if image["type"] != "input_image" || image["image_url"] != "data:image/png;base64,AQID" {
		t.Fatal("image not converted", image)
	}
	input := bodies[1]["input"].([]any)
	if len(input) != 5 || input[2].(map[string]any)["encrypted_content"] != "opaque-fixture" || input[3].(map[string]any)["call_id"] != "call_1" || input[4].(map[string]any)["output"] != "file contents" {
		t.Fatal("reasoning/tool history lost or duplicated", input)
	}
	other, _ := New("https://different.invalid/v1", "fixture", time.Second)
	for _, c := range []*Client{client, other} {
		model := "another-model"
		if c == other {
			model = "gpt-6-astra"
		}
		req.Model = model
		wire, _ := json.Marshal(c.responsesRequest(req))
		if strings.Contains(string(wire), "opaque-fixture") {
			t.Fatal("opaque reasoning leaked to another model or endpoint")
		}
	}
}

func TestResponsesNeverExecutesUnfinishedCalls(t *testing.T) {
	prefix := `data: {"type":"response.output_text.delta","delta":"partial"}` + "\n\n" + `data: {"type":"response.output_item.done","item":{"type":"function_call","call_id":"c","name":"write_file","arguments":"{}","status":"completed"}}` + "\n\n"
	for _, ending := range []string{
		"", "data: [DONE]\n\n",
		`data: {"type":"response.incomplete","response":{"status":"incomplete"}}` + "\n\n",
		`data: {"type":"response.failed","response":{"status":"failed","error":{"message":"rejected","code":"invalid_request_error"}}}` + "\n\n",
		`data: {"type":"error","message":"rejected","code":"invalid_request_error"}` + "\n\n",
		responsesCompleted(`[{"type":"function_call","name":"write_file","arguments":"{}"}]`),
		responsesCompleted(`[{"type":"function_call","call_id":"c","name":"write_file","arguments":"{","status":"completed"}]`),
	} {
		response, err := consumeResponses(strings.NewReader(prefix+ending), nil)
		if err == nil || response.Content != "partial" || len(response.ToolCalls) != 0 || response.ProviderState != nil {
			t.Fatalf("incomplete stream must retain only text: %+v %v", response, err)
		}
	}
}

func TestConfiguredProtocolNeverFallsBackOnErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		msg    string
	}{
		{401, toolsNeedResponses}, {500, toolsNeedResponses},
		{400, "Unsupported parameter: 'reasoning_effort' is not supported with this model."},
		{400, "Invalid function arguments"},
	} {
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.WriteHeader(tc.status)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": tc.msg}})
		}))
		client, _ := New(srv.URL, "fixture", time.Second)
		_, err := client.Stream(context.Background(), Request{Tools: []Tool{{Name: "read_file"}}}, nil)
		srv.Close()
		if err == nil || calls != 1 {
			t.Fatalf("unexpected protocol retry for %d %q: calls=%d %v", tc.status, tc.msg, calls, err)
		}
	}

}

func TestResponsesCancellationAndUnavailableEndpoint(t *testing.T) {
	for _, cancelRequest := range []bool{false, true} {
		calls := 0
		ctx, cancel := context.WithCancel(context.Background())
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if cancelRequest {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				cancel()
				<-r.Context().Done()
			} else {
				w.WriteHeader(404)
			}
		}))
		client, _ := NewConfigured(Config{BaseURL: srv.URL, APIKey: "fixture", Protocol: model.APIResponses, Timeout: time.Second})
		_, err := client.Stream(ctx, Request{Tools: []Tool{{Name: "read_file"}}}, nil)
		cancel()
		srv.Close()
		var apiErr *Error
		if err == nil || calls != 1 || (!cancelRequest && (!errors.As(err, &apiErr) || apiErr.Status != 404)) {
			t.Fatalf("fallback must terminate without switching back: calls=%d %v", calls, err)
		}
	}
}
