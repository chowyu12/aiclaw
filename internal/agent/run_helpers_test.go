package agent

import (
	"strings"
	"testing"

	openai "github.com/chowyu12/go-openai"

	"github.com/chowyu12/aiclaw/internal/model"
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
			Role:    openai.ChatMessageRoleAssistant,
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

func TestWebSearchEffectiveModes(t *testing.T) {
	builtin := &model.Agent{
		ModelName:       "qwen-plus-latest",
		EnableWebSearch: true,
		WebSearchMode:   model.WebSearchModeBuiltin,
	}
	if !webSearchEffective(builtin) {
		t.Fatal("built-in web search should be effective for supported model")
	}

	external := &model.Agent{
		ModelName:       "qwen-plus-latest",
		EnableWebSearch: true,
		WebSearchMode:   model.WebSearchModeExternal,
	}
	if webSearchEffective(external) {
		t.Fatal("external mode should not enable built-in model web search")
	}
	if !externalWebSearchEffective(external) {
		t.Fatal("external mode should enable external web search")
	}
}

func TestApplyModelCapsBuiltInWebSearchUsesExtraBody(t *testing.T) {
	ag := &model.Agent{
		ModelName:       "qwen-plus-latest",
		EnableWebSearch: true,
		WebSearchMode:   model.WebSearchModeBuiltin,
	}
	req := openai.ChatCompletionRequest{}

	applyModelCaps(&req, ag, model.ProviderQwen, testLogger())

	if req.ExtraBody["enable_search"] != true {
		t.Fatalf("enable_search extra body = %#v, want true", req.ExtraBody["enable_search"])
	}
	if _, ok := req.ExtraBody["web_search_options"]; ok {
		t.Fatalf("unexpected web_search_options in extra body: %#v", req.ExtraBody)
	}
	if req.ChatTemplateKwargs["enable_search"] != true {
		t.Fatalf("enable_search chat template kwarg = %#v, want true", req.ChatTemplateKwargs["enable_search"])
	}
}

func TestRecordBuiltInWebSearchStep(t *testing.T) {
	store := newMockStore()
	tracker := NewStepTracker(store, 42)
	ec := &execContext{
		ag:      &model.Agent{ModelName: "qwen-plus-latest"},
		prov:    &model.Provider{Name: "Qwen"},
		tracker: tracker,
		userMsg: "今天的 AI 新闻",
	}

	recordBuiltInWebSearchStep(t.Context(), ec, map[string]any{"enable_search": true})

	steps := tracker.Steps()
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	step := steps[0]
	if step.StepType != model.StepToolCall || step.Name != "web_search" {
		t.Fatalf("unexpected step identity: type=%s name=%s", step.StepType, step.Name)
	}
	if !strings.Contains(step.Input, "今天的 AI 新闻") {
		t.Fatalf("step input should include user query, got %s", step.Input)
	}
	if !strings.Contains(step.Output, `"enable_search": true`) {
		t.Fatalf("step output should include enable_search, got %s", step.Output)
	}
}

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
