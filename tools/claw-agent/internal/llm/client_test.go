package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// sse 把若干 chunk 拼成一段 SSE 响应体。
func sse(chunks ...string) string {
	var builder strings.Builder
	for _, chunk := range chunks {
		builder.WriteString("data: ")
		builder.WriteString(chunk)
		builder.WriteString("\n\n")
	}
	builder.WriteString("data: [DONE]\n\n")
	return builder.String()
}

func TestStreamAccumulatesContentAndUsage(t *testing.T) {
	body := sse(
		`{"choices":[{"delta":{"content":"你"}}]}`,
		`{"choices":[{"delta":{"content":"好"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`,
	)
	var deltas []string
	resp, err := consumeStream(strings.NewReader(body), func(d Delta) {
		deltas = append(deltas, d.Content)
	})
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if resp.Content != "你好" {
		t.Errorf("content = %q", resp.Content)
	}
	if strings.Join(deltas, "|") != "你|好" {
		t.Errorf("deltas = %v", deltas)
	}
	if resp.Usage.TotalTokens != 12 {
		t.Errorf("usage = %+v", resp.Usage)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("finish = %q", resp.FinishReason)
	}
}

func TestStreamReassemblesFragmentedToolCalls(t *testing.T) {
	// 真实上游的形态：id/name 只在第一片，arguments 分多片，两个并行调用靠 index 区分。
	body := sse(
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"read_file","arguments":""}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"pa"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"list_dir","arguments":"{}"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"th\":\"a.txt\"}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	resp, err := consumeStream(strings.NewReader(body), nil)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if len(resp.ToolCalls) != 2 {
		t.Fatalf("want 2 tool calls, got %d: %+v", len(resp.ToolCalls), resp.ToolCalls)
	}
	first, second := resp.ToolCalls[0], resp.ToolCalls[1]
	if first.ID != "call_a" || first.Name != "read_file" {
		t.Errorf("first = %+v", first)
	}
	// 分片必须按 index 拼回，而不是按 id——第二、四片根本没带 id。
	if first.Arguments != `{"path":"a.txt"}` {
		t.Errorf("first args = %q", first.Arguments)
	}
	if second.ID != "call_b" || second.Name != "list_dir" || second.Arguments != "{}" {
		t.Errorf("second = %+v", second)
	}
}

func TestStreamFillsMissingToolCallID(t *testing.T) {
	body := sse(
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"list_dir","arguments":"{}"}}]}}]}`,
	)
	resp, err := consumeStream(strings.NewReader(body), nil)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	// 少数上游不给 id；不补的话后面的 tool 消息关联不上，上游会 400。
	if resp.ToolCalls[0].ID == "" {
		t.Error("缺失的 id 应当被补上")
	}
}

func TestStreamSurfacesInlineError(t *testing.T) {
	body := sse(`{"error":{"message":"额度已用完","type":"quota"}}`)
	_, err := consumeStream(strings.NewReader(body), nil)
	if err == nil || !strings.Contains(err.Error(), "额度已用完") {
		t.Errorf("流内错误应当带出原文，得到 %v", err)
	}
}

func TestStreamSkipsMalformedChunk(t *testing.T) {
	body := "data: {not json}\n\n" + sse(`{"choices":[{"delta":{"content":"ok"}}]}`)
	resp, err := consumeStream(strings.NewReader(body), nil)
	if err != nil {
		t.Fatalf("坏分片不该中断整条流：%v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("content = %q", resp.Content)
	}
}

func TestWireRequestNullsContentOnToolCallMessages(t *testing.T) {
	wire := buildWireRequest(Request{
		Model: "m",
		Messages: []Message{
			{Role: RoleAssistant, Content: "", ToolCalls: []ToolCall{{ID: "c1", Name: "x", Arguments: "{}"}}},
			{Role: RoleTool, ToolCallID: "c1", Content: "result"},
		},
	})
	// 带 tool_calls 的 assistant 消息 content 必须是 null，空串会被部分上游拒。
	if wire.Messages[0].Content != nil {
		t.Errorf("content should be nil, got %v", wire.Messages[0].Content)
	}
	if wire.Messages[1].ToolCallID != "c1" {
		t.Errorf("tool_call_id 没有透传")
	}
	if !wire.Stream || wire.StreamOptions == nil || !wire.StreamOptions.IncludeUsage {
		t.Error("必须开流式并要求 usage")
	}
}

func TestStreamAgainstHTTPServer(t *testing.T) {
	var gotAuth string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sse(`{"choices":[{"delta":{"content":"hi"}}]}`)))
	}))
	defer server.Close()

	client, err := New(server.URL+"/v1", "sk-test", time.Second*5)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	resp, err := client.Stream(context.Background(), Request{
		Model:    "test-model",
		Messages: []Message{{Role: RoleUser, Content: "hello"}},
		Tools:    []Tool{{Name: "t", Description: "d"}},
	}, nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if resp.Content != "hi" {
		t.Errorf("content = %q", resp.Content)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("auth = %q", gotAuth)
	}
	if gotBody["model"] != "test-model" {
		t.Errorf("model = %v", gotBody["model"])
	}
	tools := gotBody["tools"].([]any)
	fn := tools[0].(map[string]any)["function"].(map[string]any)
	// 没给 schema 的工具要补一个空 object，否则部分上游拒收整个请求。
	if fn["parameters"] == nil {
		t.Error("空 schema 应当被补上")
	}
}

func TestHTTPErrorIsReadable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer server.Close()

	client, _ := New(server.URL, "bad", time.Second)
	_, err := client.Stream(context.Background(), Request{Model: "m"}, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid api key") || !strings.Contains(err.Error(), "401") {
		t.Errorf("错误应当带状态码与上游文案，得到 %v", err)
	}
}

func TestImagesBecomeContentParts(t *testing.T) {
	// 带图的消息 content 从字符串变成分段数组，图用 data URL 内联——
	// 上游拿不到我们本机的文件。
	wire := buildWireRequest(Request{
		Model: "m",
		Messages: []Message{
			{Role: RoleUser, Content: "看这张图", Images: [][]byte{{0x89, 0x50, 0x4e, 0x47}}},
		},
	})
	encoded, err := json.Marshal(wire.Messages[0])
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Content []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			ImageURL *struct {
				URL string `json:"url"`
			} `json:"image_url"`
		} `json:"content"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("带图的 content 应当是数组：%v（%s）", err, encoded)
	}
	if len(decoded.Content) != 2 {
		t.Fatalf("期望文本 + 图两段，实际 %d 段：%s", len(decoded.Content), encoded)
	}
	if decoded.Content[0].Type != "text" || decoded.Content[0].Text != "看这张图" {
		t.Errorf("第一段应当是文本：%+v", decoded.Content[0])
	}
	if decoded.Content[1].Type != "image_url" || decoded.Content[1].ImageURL == nil {
		t.Fatalf("第二段应当是图：%+v", decoded.Content[1])
	}
	if !strings.HasPrefix(decoded.Content[1].ImageURL.URL, "data:image/png;base64,") {
		t.Errorf("图应当是 data URL：%q", decoded.Content[1].ImageURL.URL)
	}
}

func TestMessagesWithoutImagesStayPlainStrings(t *testing.T) {
	// 没有图的消息必须保持字符串。无条件改成数组会让不支持多模态的上游直接拒。
	wire := buildWireRequest(Request{
		Model:    "m",
		Messages: []Message{{Role: RoleUser, Content: "纯文本"}},
	})
	if _, ok := wire.Messages[0].Content.(string); !ok {
		t.Errorf("没有图时 content 应当还是字符串，实际 %T", wire.Messages[0].Content)
	}
}

func TestToolCallMessageStillNullsContentEvenWithNoImages(t *testing.T) {
	// 这条原来就有，加了多模态分支之后要确认没被绕过去。
	wire := buildWireRequest(Request{
		Model: "m",
		Messages: []Message{
			{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "c1", Name: "x", Arguments: "{}"}}},
		},
	})
	if wire.Messages[0].Content != nil {
		t.Errorf("带 tool_calls 的 assistant 消息 content 必须是 null，实际 %v", wire.Messages[0].Content)
	}
}
