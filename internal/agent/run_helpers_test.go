package agent

import (
	"testing"

	openai "github.com/chowyu12/go-openai"
)

// ── sanitizeMessages ────────────────────────────────────────

func TestSanitizeMessages_NoFixNeeded(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "hello"},
		{Role: openai.ChatMessageRoleAssistant, Content: "hi"},
	}
	got := sanitizeMessages(msgs)
	if &got[0] != &msgs[0] {
		t.Error("should return original slice when no fix needed")
	}
}

func TestSanitizeMessages_FixesEmptyToolContent(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "do it"},
		{
			Role:      openai.ChatMessageRoleAssistant,
			ToolCalls: []openai.ToolCall{{ID: "tc1", Function: openai.FunctionCall{Name: "write", Arguments: `{}`}}},
		},
		{Role: openai.ChatMessageRoleTool, ToolCallID: "tc1", Name: "write"},
	}
	got := sanitizeMessages(msgs)

	if got[1].Content != " " {
		t.Errorf("assistant with tool_calls should get space content, got %q", got[1].Content)
	}
	if got[2].Content != " " {
		t.Errorf("tool with empty content should get space content, got %q", got[2].Content)
	}
	if got[0].Content != "do it" {
		t.Error("user message should be unchanged")
	}
}

func TestSanitizeMessages_FixesBrokenJSON(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "do it"},
		{
			Role: openai.ChatMessageRoleAssistant,
			Content: "calling tool",
			ToolCalls: []openai.ToolCall{{
				ID: "tc1", Function: openai.FunctionCall{Name: "exec", Arguments: `{"path": "/tmp/f`},
			}},
		},
		{Role: openai.ChatMessageRoleTool, ToolCallID: "tc1", Name: "exec", Content: "ok"},
	}
	got := sanitizeMessages(msgs)

	if got[1].ToolCalls[0].Function.Arguments != "{}" {
		t.Errorf("broken JSON should be replaced with {}, got %q", got[1].ToolCalls[0].Function.Arguments)
	}
}

func TestSanitizeMessages_ValidJSONKept(t *testing.T) {
	args := `{"key":"value"}`
	msgs := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "do it"},
		{
			Role:    openai.ChatMessageRoleAssistant,
			Content: " ",
			ToolCalls: []openai.ToolCall{{
				ID: "tc1", Function: openai.FunctionCall{Name: "exec", Arguments: args},
			}},
		},
		{Role: openai.ChatMessageRoleTool, ToolCallID: "tc1", Name: "exec", Content: "ok"},
	}
	got := sanitizeMessages(msgs)

	if got[1].ToolCalls[0].Function.Arguments != args {
		t.Error("valid JSON arguments should not be modified")
	}
}

// ── truncateLog ─────────────────────────────────────────────

func TestTruncateLog_Comprehensive(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		maxLen int
		expect string
	}{
		{"short", "hello", 10, "hello"},
		{"exact", "hello", 5, "hello"},
		{"long", "hello world!!", 5, "hello..."},
		{"newlines", "line1\nline2\nline3", 20, "line1 line2 line3"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := truncateLog(c.input, c.maxLen)
			if got != c.expect {
				t.Errorf("expected %q, got %q", c.expect, got)
			}
		})
	}
}

// ── mergeMap ────────────────────────────────────────────────

func TestMergeMap_NilDst(t *testing.T) {
	src := map[string]any{"a": 1}
	got := mergeMap(nil, src)
	if got["a"] != 1 {
		t.Error("mergeMap with nil dst should return src")
	}
}

func TestMergeMap_Merge(t *testing.T) {
	dst := map[string]any{"a": 1}
	src := map[string]any{"b": 2, "a": 3}
	got := mergeMap(dst, src)
	if got["a"] != 3 {
		t.Error("src should overwrite dst")
	}
	if got["b"] != 2 {
		t.Error("new key from src should be added")
	}
}

// ── isTransientLLMError ─────────────────────────────────────

func TestIsTransientLLMError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"api_429", &openai.APIError{HTTPStatusCode: 429}, true},
		{"api_500", &openai.APIError{HTTPStatusCode: 500}, true},
		{"api_502", &openai.APIError{HTTPStatusCode: 502}, true},
		{"api_503", &openai.APIError{HTTPStatusCode: 503}, true},
		{"api_504", &openai.APIError{HTTPStatusCode: 504}, true},
		{"api_400", &openai.APIError{HTTPStatusCode: 400}, false},
		{"api_401", &openai.APIError{HTTPStatusCode: 401}, false},
		{"connection_reset", &testErr{msg: "read tcp: connection reset by peer"}, true},
		{"unexpected_eof", &testErr{msg: "unexpected EOF"}, true},
		{"too_many_empty", &testErr{msg: "too many empty messages in a row"}, true},
		{"not_transient", &testErr{msg: "invalid api key"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTransientLLMError(tt.err)
			if got != tt.want {
				t.Errorf("isTransientLLMError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }

// ── generateSummary empty choices ───────────────────────────

func TestContextCompressor_Compress_EmptyChoices(t *testing.T) {
	mockLLM := &mockLLMProvider{
		responses: []openai.ChatCompletionResponse{
			{Choices: []openai.ChatCompletionChoice{}},
		},
	}
	c := &ContextCompressor{
		contextWindow: 100000, threshold: 0.5, minProtectTail: 5, logger: testLogger(),
	}

	msgs := buildLargeConversation(30)
	_, err := c.Compress(t.Context(), msgs, mockLLM, "test-model")
	if err == nil {
		t.Error("should return error when LLM returns empty choices")
	}
}
