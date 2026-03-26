package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/workspace"
)

func TestBuildSystemPrompt(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		ag := &model.Agent{}
		result := buildSystemPrompt(ag, nil, nil, nil, false)
		if result == "" {
			t.Error("expected default base prompt when system_prompt is empty")
		}
		if !strings.Contains(result, "Aiclaw") {
			t.Errorf("expected default Aiclaw intro, got %q", result)
		}
	})

	t.Run("with_prompt", func(t *testing.T) {
		ag := &model.Agent{SystemPrompt: "你是助手"}
		result := buildSystemPrompt(ag, nil, nil, nil, false)
		if result != "你是助手" {
			t.Errorf("expected '你是助手', got %q", result)
		}
	})

	t.Run("with_skills", func(t *testing.T) {
		ag := &model.Agent{SystemPrompt: "base"}
		skills := []model.Skill{{Name: "翻译", Description: "翻译技能描述"}}
		result := buildSystemPrompt(ag, skills, nil, nil, false)
		if !strings.Contains(result, "翻译") || !strings.Contains(result, "翻译技能描述") {
			t.Errorf("skill not included: %q", result)
		}
	})

	t.Run("with_tools", func(t *testing.T) {
		ag := &model.Agent{}
		tools := []model.Tool{
			{Name: "read", Description: "读取文件内容", Enabled: true},
			{Name: "exec", Description: "运行命令", Enabled: true},
		}
		result := buildSystemPrompt(ag, nil, tools, nil, false)
		if !strings.Contains(result, "执行策略") {
			t.Errorf("missing strategy section: %q", result)
		}
		if !strings.Contains(result, "工具优先") {
			t.Errorf("missing tool-first strategy: %q", result)
		}
	})

	t.Run("with_skill_tool_mapping", func(t *testing.T) {
		ag := &model.Agent{SystemPrompt: "base"}
		skills := []model.Skill{{Name: "翻译", Description: "翻译技能"}}
		tools := []model.Tool{
			{Name: "translate_api", Description: "文本翻译", Enabled: true},
		}
		toolSkillMap := map[string]string{"translate_api": "翻译"}
		result := buildSystemPrompt(ag, skills, tools, toolSkillMap, false)
		if !strings.Contains(result, "关联工具: translate_api") {
			t.Errorf("missing skill-tool association: %q", result)
		}
		if !strings.Contains(result, "技能路由") {
			t.Errorf("missing skill routing strategy: %q", result)
		}
	})

	t.Run("disabled_tools_excluded", func(t *testing.T) {
		ag := &model.Agent{}
		tools := []model.Tool{
			{Name: "enabled_tool", Description: "可用", Enabled: true},
			{Name: "disabled_tool", Description: "禁用", Enabled: false},
		}
		result := buildSystemPrompt(ag, nil, tools, nil, false)
		if !strings.Contains(result, "工具优先") {
			t.Errorf("expected tool strategy when tools present: %q", result)
		}
	})

	t.Run("full", func(t *testing.T) {
		ag := &model.Agent{SystemPrompt: "base prompt"}
		skills := []model.Skill{{Name: "代码审查", Description: "审查代码"}}
		tools := []model.Tool{
			{Name: "test_tool", Description: "测试工具", Enabled: true},
		}
		result := buildSystemPrompt(ag, skills, tools, nil, false)
		if !strings.Contains(result, "base prompt") {
			t.Error("missing base prompt")
		}
		if !strings.Contains(result, "代码审查") {
			t.Error("missing skill")
		}
		if !strings.Contains(result, "执行策略") {
			t.Error("missing strategy section")
		}
	})
}

func TestBuildLLMToolDefs(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		defs := buildLLMToolDefs(nil, nil, nil)
		if len(defs) != 0 {
			t.Errorf("expected 0, got %d", len(defs))
		}
	})

	t.Run("disabled_tools_skipped", func(t *testing.T) {
		modelTools := []model.Tool{
			{Name: "a", Description: "A", Enabled: false},
			{Name: "b", Description: "B", Enabled: true},
		}
		defs := buildLLMToolDefs(modelTools, nil, nil)
		if len(defs) != 1 {
			t.Fatalf("expected 1, got %d", len(defs))
		}
		if defs[0].Function.Name != "b" {
			t.Errorf("expected tool 'b', got %q", defs[0].Function.Name)
		}
	})

	t.Run("with_function_def", func(t *testing.T) {
		funcDef := testJSON(map[string]any{
			"description": "custom desc",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"city": map[string]any{"type": "string"},
				},
			},
		})
		modelTools := []model.Tool{
			{Name: "weather", Description: "orig", Enabled: true, FunctionDef: funcDef},
		}
		defs := buildLLMToolDefs(modelTools, nil, nil)
		if len(defs) != 1 {
			t.Fatalf("expected 1, got %d", len(defs))
		}
		if defs[0].Function.Description != "custom desc" {
			t.Errorf("expected 'custom desc', got %q", defs[0].Function.Description)
		}
		params, ok := defs[0].Function.Parameters.(map[string]any)
		if !ok {
			t.Fatal("parameters should be map")
		}
		if _, hasProps := params["properties"]; !hasProps {
			t.Error("missing properties in parameters")
		}
	})

	t.Run("no_parameters_adds_default", func(t *testing.T) {
		modelTools := []model.Tool{
			{Name: "simple", Description: "simple tool", Enabled: true},
		}
		defs := buildLLMToolDefs(modelTools, nil, nil)
		if defs[0].Function.Parameters == nil {
			t.Error("expected default parameters, got nil")
		}
	})
}

func TestExtractContent(t *testing.T) {
	t.Run("empty_choices", func(t *testing.T) {
		if got := extractContent(openai.ChatCompletionResponse{}); got != "" {
			t.Errorf("expected empty, got %q", got)
		}
	})
	t.Run("normal", func(t *testing.T) {
		resp := textResp("hello world")
		if got := extractContent(resp); got != "hello world" {
			t.Errorf("expected 'hello world', got %q", got)
		}
	})
}

func TestTruncateLog(t *testing.T) {
	t.Run("short_string", func(t *testing.T) {
		if got := truncateLog("abc", 10); got != "abc" {
			t.Errorf("expected 'abc', got %q", got)
		}
	})
	t.Run("long_string", func(t *testing.T) {
		got := truncateLog("abcdefghij", 5)
		if got != "abcde..." {
			t.Errorf("expected 'abcde...', got %q", got)
		}
	})
	t.Run("replaces_newlines", func(t *testing.T) {
		got := truncateLog("a\nb\nc", 100)
		if strings.Contains(got, "\n") {
			t.Errorf("should not contain newlines: %q", got)
		}
	})
}

// ==================== Executor Integration Tests ====================

func TestExecute_Simple(t *testing.T) {
	s := newMockStore()
	_, _ = seedAgent(t, s)
	mockLLM := &mockLLMProvider{responses: []openai.ChatCompletionResponse{textResp("你好世界")}}
	exec := newTestExecutor(s, NewToolRegistry(), mockLLM)

	result, err := exec.Execute(t.Context(), model.ChatRequest{UserID: "u1", Message: "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "你好世界" {
		t.Errorf("expected '你好世界', got %q", result.Content)
	}
	if result.ConversationID == "" {
		t.Error("conversation ID should not be empty")
	}
	if mockLLM.callCount() != 1 {
		t.Errorf("expected 1 LLM call, got %d", mockLLM.callCount())
	}
	if len(result.Steps) == 0 {
		t.Error("expected at least 1 execution step")
	}
}

func TestExecute_AgentNotInitialized(t *testing.T) {
	s := newMockStore()
	t.Cleanup(ClearTestAgent)
	ClearTestAgent()
	mockLLM := &mockLLMProvider{}
	exec := newTestExecutor(s, NewToolRegistry(), mockLLM)

	_, err := exec.Execute(t.Context(), model.ChatRequest{UserID: "u1", Message: "hello"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "agent not found") {
		t.Errorf("expected 'agent not found' error, got: %v", err)
	}
}

func TestExecute_ProviderNotFound(t *testing.T) {
	s := newMockStore()
	ctx := t.Context()
	t.Cleanup(ClearTestAgent)
	a := &model.Agent{UUID: "orphan", Name: "Orphan", ProviderID: 9999}
	SetTestAgent(a)
	mockLLM := &mockLLMProvider{}
	exec := newTestExecutor(s, NewToolRegistry(), mockLLM)

	_, err := exec.Execute(ctx, model.ChatRequest{UserID: "u1", Message: "hello"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "provider not found") {
		t.Errorf("expected 'provider not found' error, got: %v", err)
	}
}

func TestExecute_LLMError(t *testing.T) {
	s := newMockStore()
	seedAgent(t, s)
	mockLLM := &mockLLMProvider{
		errors: []error{errors.New("rate limit exceeded")},
	}
	exec := newTestExecutor(s, NewToolRegistry(), mockLLM)

	_, err := exec.Execute(t.Context(), model.ChatRequest{UserID: "u1", Message: "hello"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "rate limit exceeded") {
		t.Errorf("expected 'rate limit exceeded', got: %v", err)
	}
}

func TestExecute_WithToolCall(t *testing.T) {
	s := newMockStore()
	agent, _ := seedAgent(t, s)

	registry := NewToolRegistry()
	registry.RegisterBuiltin("test_echo", func(_ context.Context, args string) (string, error) {
		return "ECHO:" + args, nil
	})
	seedToolForAgent(t, s, agent.ID, "test_echo", "echo tool for test")

	mockLLM := &mockLLMProvider{
		responses: []openai.ChatCompletionResponse{
			toolCallResp("test_echo", `{"text":"ping"}`),
			textResp("工具返回了 ECHO:{\"text\":\"ping\"}"),
		},
	}
	exec := newTestExecutor(s, registry, mockLLM)

	result, err := exec.Execute(t.Context(), model.ChatRequest{
		UserID: "u1", Message: "测试工具",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Content, "ECHO") {
		t.Errorf("expected content to reference ECHO, got %q", result.Content)
	}
	if mockLLM.callCount() != 2 {
		t.Errorf("expected 2 LLM calls (tool request + final), got %d", mockLLM.callCount())
	}

	hasToolStep := false
	for _, step := range result.Steps {
		if step.StepType == model.StepToolCall && step.Name == "test_echo" {
			hasToolStep = true
			if !strings.Contains(step.Output, "ECHO:") {
				t.Errorf("tool step output should contain ECHO, got %q", step.Output)
			}
			if step.MessageID == 0 {
				t.Error("tool step message_id should not be 0 after SetMessageID")
			}
		}
	}
	if !hasToolStep {
		t.Error("expected a tool_call execution step for test_echo")
	}

	for _, step := range result.Steps {
		if step.StepType == model.StepToolCall {
			dbSteps, err := s.ListExecutionSteps(t.Context(), step.MessageID)
			if err != nil {
				t.Fatalf("ListExecutionSteps: %v", err)
			}
			found := false
			for _, ds := range dbSteps {
				if ds.Name == "test_echo" && ds.StepType == model.StepToolCall {
					found = true
				}
			}
			if !found {
				t.Error("tool step should be queryable by messageID from store")
			}
			break
		}
	}
}

func TestExecute_WithMultipleToolCalls(t *testing.T) {
	s := newMockStore()
	agent, _ := seedAgent(t, s)

	registry := NewToolRegistry()
	registry.RegisterBuiltin("tool_a", func(_ context.Context, _ string) (string, error) {
		return "result_a", nil
	})
	registry.RegisterBuiltin("tool_b", func(_ context.Context, _ string) (string, error) {
		return "result_b", nil
	})
	seedToolForAgent(t, s, agent.ID, "tool_a", "tool A")
	seedToolForAgent(t, s, agent.ID, "tool_b", "tool B")

	mockLLM := &mockLLMProvider{
		responses: []openai.ChatCompletionResponse{
			{Choices: []openai.ChatCompletionChoice{{
				Message: openai.ChatCompletionMessage{
					Role: openai.ChatMessageRoleAssistant,
					ToolCalls: []openai.ToolCall{
						{ID: "c1", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "tool_a", Arguments: "{}"}},
						{ID: "c2", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "tool_b", Arguments: "{}"}},
					},
				},
			}}},
			textResp("综合结果: result_a 和 result_b"),
		},
	}
	exec := newTestExecutor(s, registry, mockLLM)

	result, err := exec.Execute(t.Context(), model.ChatRequest{
		UserID: "u1", Message: "调用两个工具",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Content, "result_a") || !strings.Contains(result.Content, "result_b") {
		t.Errorf("expected both results, got %q", result.Content)
	}

	toolStepCount := 0
	for _, step := range result.Steps {
		if step.StepType == model.StepToolCall {
			toolStepCount++
		}
	}
	if toolStepCount != 2 {
		t.Errorf("expected 2 tool steps, got %d", toolStepCount)
	}
}

func TestExecute_ToolCallError(t *testing.T) {
	s := newMockStore()
	agent, _ := seedAgent(t, s)

	registry := NewToolRegistry()
	registry.RegisterBuiltin("failing_tool", func(_ context.Context, _ string) (string, error) {
		return "", errors.New("tool internal error")
	})
	seedToolForAgent(t, s, agent.ID, "failing_tool", "tool that fails")

	mockLLM := &mockLLMProvider{
		responses: []openai.ChatCompletionResponse{
			toolCallResp("failing_tool", "{}"),
			textResp("工具调用失败了，让我直接回答"),
		},
	}
	exec := newTestExecutor(s, registry, mockLLM)

	result, err := exec.Execute(t.Context(), model.ChatRequest{
		UserID: "u1", Message: "试试工具",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content == "" {
		t.Error("expected non-empty content even after tool error")
	}

	hasErrorStep := false
	for _, step := range result.Steps {
		if step.StepType == model.StepToolCall && step.Status == model.StepError {
			hasErrorStep = true
		}
	}
	if !hasErrorStep {
		t.Error("expected an error tool step")
	}
}

func TestExecute_ToolNotFoundByLLM(t *testing.T) {
	s := newMockStore()
	agent, _ := seedAgent(t, s)
	seedToolForAgent(t, s, agent.ID, "real_tool", "a real tool")

	mockLLM := &mockLLMProvider{
		responses: []openai.ChatCompletionResponse{
			toolCallResp("nonexistent_tool", "{}"),
			textResp("我没法使用那个工具"),
		},
	}
	exec := newTestExecutor(s, NewToolRegistry(), mockLLM)

	result, err := exec.Execute(t.Context(), model.ChatRequest{
		UserID: "u1", Message: "test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content == "" {
		t.Error("expected non-empty content")
	}
}

func TestExecute_WithSkills(t *testing.T) {
	t.Cleanup(workspace.ResetRootForTesting)
	tmpRoot := t.TempDir()
	if err := workspace.Init(tmpRoot); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(workspace.Skills(), "test-translate")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillMD := "---\nname: 翻译助手\ndescription: 翻译技能描述\n---\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		t.Fatal(err)
	}

	s := newMockStore()
	_, _ = seedAgent(t, s)
	ctx := t.Context()

	mockLLM := &mockLLMProvider{responses: []openai.ChatCompletionResponse{textResp("translated content")}}
	exec := newTestExecutor(s, NewToolRegistry(), mockLLM)

	result, err := exec.Execute(ctx, model.ChatRequest{
		UserID: "u1", Message: "translate this",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "translated content" {
		t.Errorf("unexpected content: %q", result.Content)
	}

	llmReq := mockLLM.calls[0]
	systemMsg := ""
	for _, msg := range llmReq.Messages {
		if msg.Role == openai.ChatMessageRoleSystem {
			systemMsg += msg.Content
		}
	}
	if !strings.Contains(systemMsg, "翻译助手") {
		t.Errorf("system prompt should include skill name from workspace, got %q", systemMsg)
	}
}

func TestExecute_ConversationReuse(t *testing.T) {
	s := newMockStore()
	_, _ = seedAgent(t, s)
	mockLLM := &mockLLMProvider{
		responses: []openai.ChatCompletionResponse{
			textResp("first response"),
			textResp("second response"),
		},
	}
	exec := newTestExecutor(s, NewToolRegistry(), mockLLM)
	ctx := t.Context()

	r1, err := exec.Execute(ctx, model.ChatRequest{
		UserID: "u1", Message: "第一条消息",
	})
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	convID := r1.ConversationID

	r2, err := exec.Execute(ctx, model.ChatRequest{
		UserID: "u1", Message: "第二条消息",
		ConversationID: convID,
	})
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if r2.ConversationID != convID {
		t.Errorf("expected same conversation %q, got %q", convID, r2.ConversationID)
	}
	if r2.Content != "second response" {
		t.Errorf("expected 'second response', got %q", r2.Content)
	}

	secondCallReq := mockLLM.calls[1]
	historyCount := 0
	for _, msg := range secondCallReq.Messages {
		if msg.Role == openai.ChatMessageRoleUser || msg.Role == openai.ChatMessageRoleAssistant {
			historyCount++
		}
	}
	if historyCount < 2 {
		t.Errorf("expected at least 2 history messages (prev user+assistant), got %d", historyCount)
	}
}

func TestExecuteStream_Simple(t *testing.T) {
	s := newMockStore()
	_, _ = seedAgent(t, s)
	mockLLM := &mockLLMProvider{streamContent: "这是流式响应内容"}
	exec := newTestExecutor(s, NewToolRegistry(), mockLLM)

	var chunks []model.StreamChunk
	err := exec.ExecuteStream(t.Context(), model.ChatRequest{
		UserID: "u1", Message: "hello",
	}, func(chunk model.StreamChunk) error {
		chunks = append(chunks, chunk)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("expected at least 1 chunk")
	}

	lastChunk := chunks[len(chunks)-1]
	if !lastChunk.Done {
		t.Error("last chunk should have Done=true")
	}

	var content strings.Builder
	for _, c := range chunks {
		content.WriteString(c.Delta)
	}
	if !strings.Contains(content.String(), "这是流式响应内容") {
		t.Errorf("content mismatch: %q", content.String())
	}
}

func TestExecuteStream_LLMError(t *testing.T) {
	s := newMockStore()
	seedAgent(t, s)
	mockLLM := &mockLLMProvider{streamErr: errors.New("stream broken")}
	exec := newTestExecutor(s, NewToolRegistry(), mockLLM)

	err := exec.ExecuteStream(t.Context(), model.ChatRequest{
		UserID: "u1", Message: "hello",
	}, func(_ model.StreamChunk) error { return nil })
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "stream broken") {
		t.Errorf("expected 'stream broken', got: %v", err)
	}
}

func TestExecuteStream_WithTools(t *testing.T) {
	s := newMockStore()
	agent, _ := seedAgent(t, s)

	registry := NewToolRegistry()
	registry.RegisterBuiltin("stream_echo", func(_ context.Context, args string) (string, error) {
		return "STREAM_ECHO:" + args, nil
	})
	seedToolForAgent(t, s, agent.ID, "stream_echo", "echo for stream test")

	mockLLM := &mockLLMProvider{
		responses: []openai.ChatCompletionResponse{
			toolCallResp("stream_echo", `{"msg":"hi"}`),
			textResp("流式工具结果已处理"),
		},
	}
	exec := newTestExecutor(s, registry, mockLLM)

	var chunks []model.StreamChunk
	err := exec.ExecuteStream(t.Context(), model.ChatRequest{
		UserID: "u1", Message: "stream with tool",
	}, func(chunk model.StreamChunk) error {
		chunks = append(chunks, chunk)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hasDone := false
	var content strings.Builder
	for _, c := range chunks {
		content.WriteString(c.Delta)
		if c.Done {
			hasDone = true
		}
	}
	if !hasDone {
		t.Error("expected a Done chunk")
	}
	if !strings.Contains(content.String(), "流式工具结果已处理") {
		t.Errorf("content mismatch: %q", content.String())
	}
}

func TestCollectTools(t *testing.T) {
	t.Run("normal", func(t *testing.T) {
		s := newMockStore()
		agent, _ := seedAgent(t, s)
		ctx := t.Context()

		directTool := seedToolForAgent(t, s, agent.ID, "direct_tool", "direct")
		ag, err := LoadAgent(ctx)
		if err != nil {
			t.Fatal(err)
		}

		registry := NewToolRegistry()
		builtinCount := len(registry.BuiltinDefs())

		exec := newTestExecutor(s, registry, &mockLLMProvider{})
		tools, _, err := exec.collectTools(ctx, ag)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(tools) != builtinCount+1 {
			t.Fatalf("expected %d tools (builtins + direct_tool), got %d", builtinCount+1, len(tools))
		}
		names := make(map[string]bool)
		for _, tool := range tools {
			names[tool.Name] = true
		}
		if !names[directTool.Name] {
			t.Errorf("expected tool %q in result", directTool.Name)
		}
	})

	t.Run("tool_search_loads_all", func(t *testing.T) {
		s := newMockStore()
		agent, _ := seedAgent(t, s)
		ctx := t.Context()

		seedToolForAgent(t, s, agent.ID, "agent_tool", "bound to agent")

		unbound := &model.Tool{Name: "unbound_tool", Description: "not bound to agent", HandlerType: model.HandlerBuiltin, Enabled: true}
		s.CreateTool(ctx, unbound)

		disabled := &model.Tool{Name: "disabled_tool", Description: "disabled", HandlerType: model.HandlerBuiltin, Enabled: false}
		s.CreateTool(ctx, disabled)

		ag, err := LoadAgent(ctx)
		if err != nil {
			t.Fatal(err)
		}
		ag.ToolSearchEnabled = true
		SetTestAgent(ag)

		exec := newTestExecutor(s, NewToolRegistry(), &mockLLMProvider{})
		tools, _, err := exec.collectTools(ctx, ag)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		names := make(map[string]bool)
		for _, tool := range tools {
			names[tool.Name] = true
		}
		if !names["agent_tool"] {
			t.Error("missing agent_tool")
		}
		if !names["unbound_tool"] {
			t.Error("missing unbound_tool — tool_search should load all enabled tools")
		}
		if names["disabled_tool"] {
			t.Error("disabled_tool should be excluded")
		}
	})

	t.Run("tool_search_lists_each_tool_once", func(t *testing.T) {
		s := newMockStore()
		agent, _ := seedAgent(t, s)
		ctx := t.Context()

		seedToolForAgent(t, s, agent.ID, "agent_tool", "bound")

		shared := &model.Tool{Name: "shared_tool", Description: "global", HandlerType: model.HandlerBuiltin, Enabled: true}
		s.CreateTool(ctx, shared)

		ag, err := LoadAgent(ctx)
		if err != nil {
			t.Fatal(err)
		}
		ag.ToolSearchEnabled = true
		SetTestAgent(ag)

		exec := newTestExecutor(s, NewToolRegistry(), &mockLLMProvider{})
		tools, _, err := exec.collectTools(ctx, ag)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		count := 0
		for _, tool := range tools {
			if tool.Name == "shared_tool" {
				count++
			}
		}
		if count != 1 {
			t.Errorf("expected shared_tool once, got %d", count)
		}
	})
}

func TestBuildMessages(t *testing.T) {
	ag := &model.Agent{SystemPrompt: "you are a bot"}
	skills := []model.Skill{{Name: "sk1", Instruction: "do stuff"}}
	history := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "prev question"},
		{Role: openai.ChatMessageRoleAssistant, Content: "prev answer"},
	}
	tools := []model.Tool{{Name: "tool1", Description: "test tool", Enabled: true}}

	msgs := buildMessages(messagesBuildInput{
		Agent:      ag,
		Skills:     skills,
		History:    history,
		UserMsg:    "new question",
		AgentTools: tools,
	})

	if len(msgs) < 4 {
		t.Fatalf("expected at least 4 messages (system + 2 history + user), got %d", len(msgs))
	}
	if msgs[0].Role != openai.ChatMessageRoleSystem {
		t.Errorf("first message should be system, got %s", msgs[0].Role)
	}
	lastMsg := msgs[len(msgs)-1]
	if lastMsg.Role != openai.ChatMessageRoleUser {
		t.Errorf("last message should be user, got %s", lastMsg.Role)
	}
	if lastMsg.Content != "new question" {
		t.Errorf("last message content should be 'new question', got %q", lastMsg.Content)
	}
}

func TestBuildMessages_WithFiles(t *testing.T) {
	ag := &model.Agent{SystemPrompt: "you are a bot"}

	files := []*model.File{
		{Filename: "readme.txt", FileType: model.FileTypeText, TextContent: "This is a readme file content."},
	}

	msgs := buildMessages(messagesBuildInput{
		Agent:   ag,
		UserMsg: "summarize the file",
		Files:   files,
	})

	lastMsg := msgs[len(msgs)-1]
	lastText := lastMsg.Content
	if !strings.Contains(lastText, "readme.txt") {
		t.Errorf("expected file reference in message, got %q", lastText)
	}
	if !strings.Contains(lastText, "This is a readme file content.") {
		t.Errorf("expected file content in message, got %q", lastText)
	}
	if !strings.Contains(lastText, "summarize the file") {
		t.Errorf("expected user message in text, got %q", lastText)
	}
}

// ==================== StepTracker Tests ====================

func TestStepTracker(t *testing.T) {
	ms := newMockStore()
	tracker := NewStepTracker(ms, 42)

	if steps := tracker.Steps(); len(steps) != 0 {
		t.Errorf("new tracker should have 0 steps, got %d", len(steps))
	}

	tracker.SetMessageID(10)
	ctx := t.Context()
	step := tracker.RecordStep(ctx, model.StepToolCall, "my_tool", "input", "output", model.StepSuccess, "", 100, 0, nil)

	if step.StepOrder != 1 {
		t.Errorf("expected step order 1, got %d", step.StepOrder)
	}
	if step.MessageID != 10 {
		t.Errorf("expected message ID 10, got %d", step.MessageID)
	}
	if step.ConversationID != 42 {
		t.Errorf("expected conversation ID 42, got %d", step.ConversationID)
	}

	steps := tracker.Steps()
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}

	tracker.RecordStep(ctx, model.StepLLMCall, "gpt", "q", "a", model.StepSuccess, "", 200, 0, nil)
	if len(tracker.Steps()) != 2 {
		t.Errorf("expected 2 steps after second record")
	}
}

// ==================== Memory Manager Tests ====================

func TestMemoryManager_GetOrCreateConversation(t *testing.T) {
	ms := newMockStore()
	mm := NewMemoryManager(ms, ms)
	ctx := t.Context()

	conv1, err := mm.GetOrCreateConversation(ctx, "", "user1")
	if err != nil {
		t.Fatal(err)
	}
	if conv1.UUID == "" {
		t.Error("new conversation should have UUID")
	}

	conv2, err := mm.GetOrCreateConversation(ctx, conv1.UUID, "user1")
	if err != nil {
		t.Fatal(err)
	}
	if conv2.ID != conv1.ID {
		t.Errorf("expected same conversation ID %d, got %d", conv1.ID, conv2.ID)
	}

	conv3, err := mm.GetOrCreateConversation(ctx, "nonexistent", "user1")
	if err != nil {
		t.Fatal(err)
	}
	if conv3.ID == conv1.ID {
		t.Error("nonexistent UUID should create new conversation")
	}
}

func TestMemoryManager_SaveAndLoadHistory(t *testing.T) {
	ms := newMockStore()
	mm := NewMemoryManager(ms, ms)
	ctx := t.Context()

	conv, _ := mm.GetOrCreateConversation(ctx, "", "user1")

	mm.SaveUserMessage(ctx, conv.ID, "你好", nil)
	mm.SaveAssistantMessage(ctx, conv.ID, "你好！", 0)
	mm.SaveUserMessage(ctx, conv.ID, "再见", nil)

	history, err := mm.LoadHistory(ctx, conv.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 {
		t.Fatalf("expected 3 history messages, got %d", len(history))
	}
	if history[0].Role != openai.ChatMessageRoleUser {
		t.Errorf("first message should be user, got %s", history[0].Role)
	}
	if history[1].Role != openai.ChatMessageRoleAssistant {
		t.Errorf("second message should be assistant, got %s", history[1].Role)
	}
}

func TestLoadHistory_WithToolCalls(t *testing.T) {
	ms := newMockStore()
	mm := NewMemoryManager(ms, ms)
	ctx := t.Context()

	conv, _ := mm.GetOrCreateConversation(ctx, "", "user1")

	ms.CreateMessage(ctx, &model.Message{
		ConversationID: conv.ID,
		Role:           "user",
		Content:        "今天天气怎么样",
	})

	toolCalls := []openai.ToolCall{{
		ID:       "call_abc123",
		Type:     openai.ToolTypeFunction,
		Function: openai.FunctionCall{Name: "get_weather", Arguments: `{"city":"北京"}`},
	}}
	toolCallsJSON, _ := json.Marshal(toolCalls)
	ms.CreateMessage(ctx, &model.Message{
		ConversationID: conv.ID,
		Role:           "assistant",
		Content:        "",
		ToolCalls:      toolCallsJSON,
	})

	ms.CreateMessage(ctx, &model.Message{
		ConversationID: conv.ID,
		Role:           "tool",
		Content:        `{"temperature": 25, "weather": "晴"}`,
		ToolCallID:     "call_abc123",
	})

	ms.CreateMessage(ctx, &model.Message{
		ConversationID: conv.ID,
		Role:           "assistant",
		Content:        "北京今天25度，天气晴朗",
	})

	history, err := mm.LoadHistory(ctx, conv.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 4 {
		t.Fatalf("expected 4 history messages, got %d", len(history))
	}

	assistantWithToolCalls := history[1]
	if assistantWithToolCalls.Role != openai.ChatMessageRoleAssistant {
		t.Errorf("expected assistant role, got %s", assistantWithToolCalls.Role)
	}
	if len(assistantWithToolCalls.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(assistantWithToolCalls.ToolCalls))
	}
	if assistantWithToolCalls.ToolCalls[0].ID != "call_abc123" {
		t.Errorf("expected tool call ID 'call_abc123', got %q", assistantWithToolCalls.ToolCalls[0].ID)
	}
	if assistantWithToolCalls.ToolCalls[0].Function.Name != "get_weather" {
		t.Errorf("expected function name 'get_weather', got %q", assistantWithToolCalls.ToolCalls[0].Function.Name)
	}

	toolMsg := history[2]
	if toolMsg.Role != openai.ChatMessageRoleTool {
		t.Errorf("expected tool role, got %s", toolMsg.Role)
	}
	if toolMsg.ToolCallID != "call_abc123" {
		t.Errorf("expected tool call ID 'call_abc123', got %q", toolMsg.ToolCallID)
	}

	finalAssistant := history[3]
	if finalAssistant.ToolCallID != "" {
		t.Errorf("final assistant should have empty ToolCallID, got %q", finalAssistant.ToolCallID)
	}
	if len(finalAssistant.ToolCalls) != 0 {
		t.Errorf("final assistant should have no ToolCalls, got %d", len(finalAssistant.ToolCalls))
	}
}

func TestGetOrCreateConversation_DBError(t *testing.T) {
	ms := newMockStore()
	mm := NewMemoryManager(ms, ms)
	ctx := t.Context()

	ms.getConvByUUIDErr = errors.New("connection refused")

	_, err := mm.GetOrCreateConversation(ctx, "some-uuid", "user1")
	if err == nil {
		t.Fatal("expected error when DB fails, got nil")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("expected 'connection refused' in error, got: %v", err)
	}

	ms.getConvByUUIDErr = nil
	conv, err := mm.GetOrCreateConversation(ctx, "", "user1")
	if err != nil {
		t.Fatalf("expected success with empty UUID: %v", err)
	}
	if conv.ID == 0 {
		t.Error("expected a valid conversation")
	}
}

func TestGetOrCreateConversation_WrongUser(t *testing.T) {
	ms := newMockStore()
	mm := NewMemoryManager(ms, ms)
	ctx := t.Context()

	conv, err := mm.GetOrCreateConversation(ctx, "", "owner")
	if err != nil {
		t.Fatal(err)
	}

	_, err = mm.GetOrCreateConversation(ctx, conv.UUID, "attacker")
	if err == nil {
		t.Fatal("expected error for wrong user, got nil")
	}
	if !strings.Contains(err.Error(), "does not belong to user") {
		t.Errorf("expected ownership error, got: %v", err)
	}

	got, err := mm.GetOrCreateConversation(ctx, conv.UUID, "owner")
	if err != nil {
		t.Fatalf("owner should succeed: %v", err)
	}
	if got.ID != conv.ID {
		t.Errorf("expected same conv %d, got %d", conv.ID, got.ID)
	}
}

func TestExecute_MultiRoundToolCalls(t *testing.T) {
	s := newMockStore()
	agent, _ := seedAgent(t, s)

	registry := NewToolRegistry()
	registry.RegisterBuiltin("weather", func(_ context.Context, args string) (string, error) {
		return `{"temp":25,"weather":"晴"}`, nil
	})
	registry.RegisterBuiltin("translate", func(_ context.Context, args string) (string, error) {
		return "sunny, 25 degrees", nil
	})
	seedToolForAgent(t, s, agent.ID, "weather", "get weather")
	seedToolForAgent(t, s, agent.ID, "translate", "translate text")

	mockLLM := &mockLLMProvider{
		responses: []openai.ChatCompletionResponse{
			toolCallResp("weather", `{"city":"北京"}`),
			textResp("北京今天25度，天气晴朗"),
			toolCallResp("translate", `{"text":"晴朗"}`),
			textResp("翻译结果：sunny, 25 degrees"),
		},
	}
	exec := newTestExecutor(s, registry, mockLLM)
	ctx := t.Context()

	r1, err := exec.Execute(ctx, model.ChatRequest{
		UserID: "u1", Message: "北京天气",
	})
	if err != nil {
		t.Fatalf("round 1: %v", err)
	}
	if !strings.Contains(r1.Content, "25度") {
		t.Errorf("round 1 content should mention 25度, got %q", r1.Content)
	}

	r2, err := exec.Execute(ctx, model.ChatRequest{
		UserID:         "u1",
		Message:        "翻译一下天气",
		ConversationID: r1.ConversationID,
	})
	if err != nil {
		t.Fatalf("round 2: %v", err)
	}
	if !strings.Contains(r2.Content, "sunny") {
		t.Errorf("round 2 content should contain 'sunny', got %q", r2.Content)
	}
	if r2.ConversationID != r1.ConversationID {
		t.Errorf("conversation should be reused: %q vs %q", r1.ConversationID, r2.ConversationID)
	}

	r2Req := mockLLM.calls[3]
	hasHistory := false
	for _, msg := range r2Req.Messages {
		if msg.Role == openai.ChatMessageRoleAssistant && strings.Contains(msg.Content, "25度") {
			hasHistory = true
		}
	}
	if !hasHistory {
		t.Error("round 2 LLM request should include round 1 history")
	}
}

func TestAutoSetTitle(t *testing.T) {
	ms := newMockStore()
	mm := NewMemoryManager(ms, ms)
	ctx := t.Context()

	conv, _ := mm.GetOrCreateConversation(ctx, "", "user1")
	if conv.Title != "New Conversation" {
		t.Fatalf("expected default title, got %q", conv.Title)
	}

	mm.AutoSetTitle(ctx, conv.ID, "这是一个很长的标题内容，当标题文字超过五十个字符的时候应该被自动截断处理而不是把完整内容展示给用户看到的最终效果")

	got, _ := ms.GetConversation(ctx, conv.ID)
	rs := []rune(got.Title)
	if len(rs) > 54 {
		t.Errorf("title should be truncated, got %d runes: %q", len(rs), got.Title)
	}
	if !strings.HasSuffix(got.Title, "...") {
		t.Errorf("truncated title should end with '...', got %q", got.Title)
	}
}

func TestMockStore_ListConversations(t *testing.T) {
	ms := newMockStore()
	ctx := t.Context()

	ms.CreateConversation(ctx, &model.Conversation{UserID: "u1", Title: "聊天一"})
	ms.CreateConversation(ctx, &model.Conversation{UserID: "u1", Title: "聊天二"})
	ms.CreateConversation(ctx, &model.Conversation{UserID: "u2", Title: "其他对话"})

	list, total, err := ms.ListConversations(ctx, "u1", model.ListQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Errorf("expected 2 conversations for user=u1, got %d", total)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 items, got %d", len(list))
	}

	list, total, _ = ms.ListConversations(ctx, "", model.ListQuery{Keyword: "其他"})
	if total != 1 {
		t.Errorf("expected 1 conversation matching keyword '其他', got %d", total)
	}
	if len(list) != 1 || list[0].Title != "其他对话" {
		t.Errorf("unexpected result: %v", list)
	}
}

func TestMockStore_DeleteConversation(t *testing.T) {
	ms := newMockStore()
	ctx := t.Context()

	conv := &model.Conversation{UserID: "u1", Title: "to be deleted"}
	ms.CreateConversation(ctx, conv)
	ms.CreateMessage(ctx, &model.Message{ConversationID: conv.ID, Role: "user", Content: "hello"})

	if err := ms.DeleteConversation(ctx, conv.ID); err != nil {
		t.Fatal(err)
	}

	_, err := ms.GetConversation(ctx, conv.ID)
	if err == nil {
		t.Error("conversation should be deleted")
	}

	msgs, _ := ms.ListMessages(ctx, conv.ID, 50)
	if len(msgs) != 0 {
		t.Errorf("messages should be cascaded deleted, got %d", len(msgs))
	}
}
