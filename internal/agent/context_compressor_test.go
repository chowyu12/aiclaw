package agent

import (
	"context"
	"strings"
	"testing"

	openai "github.com/chowyu12/go-openai"
	log "github.com/sirupsen/logrus"
)

func testLogger() *log.Entry {
	return log.WithField("test", true)
}

// ── NeedCompress ────────────────────────────────────────────

func TestContextCompressor_NeedCompress_NoContextWindow(t *testing.T) {
	c := &ContextCompressor{contextWindow: 0, threshold: 0.5, minProtectTail: 5, logger: testLogger()}
	msgs := make([]openai.ChatCompletionMessage, 30)
	if c.NeedCompress(msgs) {
		t.Error("should not compress when contextWindow is 0")
	}
}

func TestContextCompressor_NeedCompress_TooFewMessages(t *testing.T) {
	c := &ContextCompressor{contextWindow: 100000, threshold: 0.5, minProtectTail: 20, logger: testLogger()}
	msgs := make([]openai.ChatCompletionMessage, 10)
	if c.NeedCompress(msgs) {
		t.Error("should not compress when message count is below head+tail+middle minimum")
	}
}

func TestContextCompressor_NeedCompress_UsesActualTokens(t *testing.T) {
	c := &ContextCompressor{contextWindow: 1000, threshold: 0.5, minProtectTail: 5, logger: testLogger()}
	msgs := make([]openai.ChatCompletionMessage, 30)

	c.lastPromptTokens = 400
	if c.NeedCompress(msgs) {
		t.Error("400 < 500 threshold, should not compress")
	}

	c.lastPromptTokens = 600
	if !c.NeedCompress(msgs) {
		t.Error("600 >= 500 threshold, should compress")
	}
}

func TestContextCompressor_NeedCompress_UsesEstimation(t *testing.T) {
	c := &ContextCompressor{contextWindow: 200, threshold: 0.5, minProtectTail: 5, logger: testLogger()}

	msgs := make([]openai.ChatCompletionMessage, 30)
	for i := range msgs {
		msgs[i] = openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleUser,
			Content: strings.Repeat("hello world ", 20),
		}
	}

	if !c.NeedCompress(msgs) {
		t.Error("estimated tokens should exceed threshold for large messages with small context window")
	}
}

// ── alignBoundaryBackward ───────────────────────────────────

func TestAlignBoundaryBackward_NoToolMessages(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem},
		{Role: openai.ChatMessageRoleUser},
		{Role: openai.ChatMessageRoleAssistant},
		{Role: openai.ChatMessageRoleUser},
		{Role: openai.ChatMessageRoleAssistant},
	}
	got := alignBoundaryBackward(msgs, 3)
	if got != 3 {
		t.Errorf("expected 3, got %d", got)
	}
}

func TestAlignBoundaryBackward_LandsOnToolMsg(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem},
		{Role: openai.ChatMessageRoleUser},
		{Role: openai.ChatMessageRoleAssistant, ToolCalls: []openai.ToolCall{{ID: "tc1"}}},
		{Role: openai.ChatMessageRoleTool},
		{Role: openai.ChatMessageRoleTool},
		{Role: openai.ChatMessageRoleUser},
	}
	got := alignBoundaryBackward(msgs, 4)
	if got != 2 {
		t.Errorf("expected 2 (before assistant with tool_calls), got %d", got)
	}
}

func TestAlignBoundaryBackward_LandsOnAssistantWithToolCalls(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem},
		{Role: openai.ChatMessageRoleUser},
		{Role: openai.ChatMessageRoleAssistant, ToolCalls: []openai.ToolCall{{ID: "tc1"}}},
		{Role: openai.ChatMessageRoleTool},
		{Role: openai.ChatMessageRoleUser},
	}
	// idx=2 is assistant (not tool), loop doesn't execute; tail starts here,
	// keeping the entire tool group [assistant(tc), tool] in the tail.
	got := alignBoundaryBackward(msgs, 2)
	if got != 2 {
		t.Errorf("expected 2 (assistant with tool_calls stays as tail start), got %d", got)
	}
}

func TestAlignBoundaryBackward_RespectsMinHead(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem},
		{Role: openai.ChatMessageRoleTool},
		{Role: openai.ChatMessageRoleTool},
		{Role: openai.ChatMessageRoleUser},
	}
	got := alignBoundaryBackward(msgs, 1)
	if got != protectHeadCount {
		t.Errorf("expected %d (protectHeadCount), got %d", protectHeadCount, got)
	}
}

// ── findTailStart ───────────────────────────────────────────

func TestFindTailStart_SmallMessages(t *testing.T) {
	msgs := make([]openai.ChatCompletionMessage, 5)
	msgs[0] = openai.ChatCompletionMessage{Role: openai.ChatMessageRoleSystem}
	for i := 1; i < 5; i++ {
		msgs[i] = openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser}
	}
	got := findTailStart(msgs, 20)
	if got != protectHeadCount {
		t.Errorf("expected protectHeadCount=%d for small message list, got %d", protectHeadCount, got)
	}
}

func TestFindTailStart_NormalConversation(t *testing.T) {
	msgs := make([]openai.ChatCompletionMessage, 50)
	msgs[0] = openai.ChatCompletionMessage{Role: openai.ChatMessageRoleSystem}
	for i := 1; i < 50; i++ {
		if i%2 == 1 {
			msgs[i] = openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser}
		} else {
			msgs[i] = openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant}
		}
	}
	got := findTailStart(msgs, 20)
	if got != 30 {
		t.Errorf("expected 30, got %d", got)
	}
}

// ── formatMessagesForSummary ────────────────────────────────

func TestFormatMessagesForSummary_Basic(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "帮我写个函数"},
		{Role: openai.ChatMessageRoleAssistant, Content: "好的，这是实现"},
		{Role: openai.ChatMessageRoleAssistant, ToolCalls: []openai.ToolCall{
			{Function: openai.FunctionCall{Name: "write"}},
		}},
		{Role: openai.ChatMessageRoleTool, Name: "write", Content: "文件已写入"},
	}
	result := formatMessagesForSummary(msgs)

	if !strings.Contains(result, "User: 帮我写个函数") {
		t.Error("should contain user message")
	}
	if !strings.Contains(result, "Assistant: 好的") {
		t.Error("should contain assistant response")
	}
	if !strings.Contains(result, "调用工具: write") {
		t.Error("should contain tool call info")
	}
	if !strings.Contains(result, "Tool(write): 文件已写入") {
		t.Error("should contain tool result")
	}
}

func TestFormatMessagesForSummary_TruncatesLongContent(t *testing.T) {
	longContent := strings.Repeat("a", 2000)
	msgs := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: longContent},
	}
	result := formatMessagesForSummary(msgs)
	if !strings.Contains(result, "...(truncated)") {
		t.Error("should truncate long content")
	}
	if len([]rune(result)) > summaryInputMaxRunes+200 {
		t.Error("output should be reasonably truncated")
	}
}

func TestFormatMessagesForSummary_MultiContent(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		{
			Role: openai.ChatMessageRoleUser,
			MultiContent: []openai.ChatMessagePart{
				{Type: openai.ChatMessagePartTypeText, Text: "看看这个图片"},
				{Type: openai.ChatMessagePartTypeImageURL},
			},
		},
	}
	result := formatMessagesForSummary(msgs)
	if !strings.Contains(result, "看看这个图片") {
		t.Error("should extract text from MultiContent")
	}
}

// ── estimateMessagesTokens ──────────────────────────────────

func TestEstimateMessagesTokens_Empty(t *testing.T) {
	got := estimateMessagesTokens(nil)
	if got != 0 {
		t.Errorf("expected 0 for nil messages, got %d", got)
	}
}

func TestEstimateMessagesTokens_CountsAllParts(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "hello world"},
		{
			Role: openai.ChatMessageRoleAssistant,
			ToolCalls: []openai.ToolCall{
				{Function: openai.FunctionCall{Name: "read", Arguments: `{"path":"/tmp/f"}`}},
			},
		},
		{Role: openai.ChatMessageRoleTool, Content: "file contents here"},
	}
	got := estimateMessagesTokens(msgs)
	if got <= 0 {
		t.Errorf("expected positive token count, got %d", got)
	}
	if got < 10 {
		t.Errorf("expected at least 10 tokens for these messages, got %d", got)
	}
}

// ── Compress (end-to-end with mock LLM) ─────────────────────

func TestContextCompressor_Compress_TooFewMessages(t *testing.T) {
	c := &ContextCompressor{
		contextWindow: 100000, threshold: 0.5, minProtectTail: 20, logger: testLogger(),
	}
	msgs := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "system"},
		{Role: openai.ChatMessageRoleUser, Content: "hi"},
	}
	got, err := c.Compress(t.Context(), msgs, nil, "test-model")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(msgs) {
		t.Errorf("should return original messages when too few, got %d", len(got))
	}
}

func TestContextCompressor_Compress_EndToEnd(t *testing.T) {
	mockLLM := &mockLLMProvider{
		responses: []openai.ChatCompletionResponse{
			{Choices: []openai.ChatCompletionChoice{{
				Message: openai.ChatCompletionMessage{
					Content: "## 目标\n用户在编写代码\n## 进展\n### 已完成\n- 写了文件\n### 进行中\n无",
				},
			}}},
		},
	}

	c := &ContextCompressor{
		contextWindow: 100000, threshold: 0.5, minProtectTail: 5, logger: testLogger(),
	}

	msgs := buildLargeConversation(30)

	compressed, err := c.Compress(t.Context(), msgs, mockLLM, "test-model")
	if err != nil {
		t.Fatal(err)
	}

	if len(compressed) >= len(msgs) {
		t.Errorf("compressed (%d) should be fewer than original (%d)", len(compressed), len(msgs))
	}

	if compressed[0].Role != openai.ChatMessageRoleSystem {
		t.Error("first message should still be system")
	}
	if !strings.Contains(compressed[0].Content, "[注意: 部分早期对话已被压缩为摘要]") {
		t.Error("system prompt should have compression note")
	}

	hasSummary := false
	for _, m := range compressed {
		if strings.Contains(m.Content, "[上下文压缩摘要]") {
			hasSummary = true
			break
		}
	}
	if !hasSummary {
		t.Error("compressed messages should contain summary message")
	}

	if c.previousSummary == "" {
		t.Error("previousSummary should be set after compression")
	}

	if c.lastPromptTokens <= 0 {
		t.Error("lastPromptTokens should be estimated from compressed messages after compression")
	}
}

func TestContextCompressor_Compress_IterativeSummary(t *testing.T) {
	callCount := 0
	mockLLM := &mockLLMProvider{
		responses: []openai.ChatCompletionResponse{
			{Choices: []openai.ChatCompletionChoice{{
				Message: openai.ChatCompletionMessage{Content: "first summary"},
			}}},
			{Choices: []openai.ChatCompletionChoice{{
				Message: openai.ChatCompletionMessage{Content: "updated summary"},
			}}},
		},
	}

	c := &ContextCompressor{
		contextWindow: 100000, threshold: 0.5, minProtectTail: 5, logger: testLogger(),
	}

	msgs := buildLargeConversation(30)
	_, err := c.Compress(t.Context(), msgs, mockLLM, "test-model")
	if err != nil {
		t.Fatal(err)
	}
	callCount++
	if c.previousSummary != "first summary" {
		t.Errorf("expected 'first summary', got %q", c.previousSummary)
	}

	msgs2 := buildLargeConversation(30)
	compressed2, err := c.Compress(t.Context(), msgs2, mockLLM, "test-model")
	if err != nil {
		t.Fatal(err)
	}
	callCount++
	_ = compressed2
	if c.previousSummary != "updated summary" {
		t.Errorf("expected 'updated summary', got %q", c.previousSummary)
	}

	if mockLLM.callCount() != 2 {
		t.Errorf("expected 2 LLM calls, got %d", mockLLM.callCount())
	}

	lastReq := mockLLM.calls[1]
	userContent := lastReq.Messages[0].Content
	if !strings.Contains(userContent, "之前的摘要") {
		t.Error("second compression should use iterative prompt with previous summary")
	}
}

func TestContextCompressor_Compress_ToolGroupsPreserved(t *testing.T) {
	mockLLM := &mockLLMProvider{
		responses: []openai.ChatCompletionResponse{
			{Choices: []openai.ChatCompletionChoice{{
				Message: openai.ChatCompletionMessage{Content: "summary"},
			}}},
		},
	}

	c := &ContextCompressor{
		contextWindow: 100000, threshold: 0.5, minProtectTail: 5, logger: testLogger(),
	}

	msgs := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "system"},
	}
	for i := range 15 {
		msgs = append(msgs,
			openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: "q" + strings.Repeat("x", 100)},
			openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: "a" + strings.Repeat("y", 100)},
		)
		_ = i
	}
	msgs = append(msgs,
		openai.ChatCompletionMessage{
			Role:      openai.ChatMessageRoleAssistant,
			ToolCalls: []openai.ToolCall{{ID: "tc1", Function: openai.FunctionCall{Name: "read"}}},
		},
		openai.ChatCompletionMessage{Role: openai.ChatMessageRoleTool, ToolCallID: "tc1", Name: "read", Content: "result"},
		openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: "thanks"},
		openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: "done"},
	)

	compressed, err := c.Compress(t.Context(), msgs, mockLLM, "test-model")
	if err != nil {
		t.Fatal(err)
	}

	for i, m := range compressed {
		if m.Role == openai.ChatMessageRoleTool && m.ToolCallID == "tc1" {
			if i == 0 {
				t.Fatal("tool message at index 0, can't check predecessor")
			}
			prev := compressed[i-1]
			if prev.Role != openai.ChatMessageRoleAssistant || len(prev.ToolCalls) == 0 {
				t.Error("tool response should be preceded by its assistant tool_call message")
			}
		}
	}
}

func TestContextCompressor_Compress_PrunesOldToolResults(t *testing.T) {
	mockLLM := &mockLLMProvider{
		responses: []openai.ChatCompletionResponse{
			{Choices: []openai.ChatCompletionChoice{{
				Message: openai.ChatCompletionMessage{Content: "summary"},
			}}},
		},
	}

	c := &ContextCompressor{
		contextWindow: 100000, threshold: 0.5, minProtectTail: 3, logger: testLogger(),
	}

	longToolContent := strings.Repeat("very long tool output ", 50)
	msgs := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "system"},
	}
	for range 8 {
		msgs = append(msgs,
			openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: strings.Repeat("q", 100)},
			openai.ChatCompletionMessage{
				Role:      openai.ChatMessageRoleAssistant,
				ToolCalls: []openai.ToolCall{{ID: "tc", Function: openai.FunctionCall{Name: "exec"}}},
			},
			openai.ChatCompletionMessage{Role: openai.ChatMessageRoleTool, ToolCallID: "tc", Name: "exec", Content: longToolContent},
			openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: strings.Repeat("a", 100)},
		)
	}
	msgs = append(msgs,
		openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: "last question"},
		openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: "last answer"},
	)

	_, err := c.Compress(t.Context(), msgs, mockLLM, "test-model")
	if err != nil {
		t.Fatal(err)
	}

	req := mockLLM.calls[0]
	summaryInput := req.Messages[0].Content
	if strings.Contains(summaryInput, longToolContent) {
		t.Error("summary input should not contain the full long tool output (should be truncated)")
	}
}

func TestContextCompressor_Compress_LLMError(t *testing.T) {
	mockLLM := &mockLLMProvider{
		errors: []error{context.DeadlineExceeded},
	}

	c := &ContextCompressor{
		contextWindow: 100000, threshold: 0.5, minProtectTail: 5, logger: testLogger(),
	}

	msgs := buildLargeConversation(30)
	_, err := c.Compress(t.Context(), msgs, mockLLM, "test-model")
	if err == nil {
		t.Error("should return error when LLM fails")
	}
}

func TestContextCompressor_Compress_SystemPromptNoteOnlyOnce(t *testing.T) {
	mockLLM := &mockLLMProvider{
		responses: []openai.ChatCompletionResponse{
			{Choices: []openai.ChatCompletionChoice{{
				Message: openai.ChatCompletionMessage{Content: "summary1"},
			}}},
			{Choices: []openai.ChatCompletionChoice{{
				Message: openai.ChatCompletionMessage{Content: "summary2"},
			}}},
		},
	}

	c := &ContextCompressor{
		contextWindow: 100000, threshold: 0.5, minProtectTail: 5, logger: testLogger(),
	}

	msgs := buildLargeConversation(30)
	compressed, err := c.Compress(t.Context(), msgs, mockLLM, "test-model")
	if err != nil {
		t.Fatal(err)
	}

	msgs2 := make([]openai.ChatCompletionMessage, len(compressed))
	copy(msgs2, compressed)
	for range 15 {
		msgs2 = append(msgs2,
			openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: strings.Repeat("q", 200)},
			openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: strings.Repeat("a", 200)},
		)
	}

	compressed2, err := c.Compress(t.Context(), msgs2, mockLLM, "test-model")
	if err != nil {
		t.Fatal(err)
	}

	noteCount := strings.Count(compressed2[0].Content, "[注意: 部分早期对话已被压缩为摘要]")
	if noteCount != 1 {
		t.Errorf("compression note should appear exactly once, found %d", noteCount)
	}
}

// ── UpdatePromptTokens ──────────────────────────────────────

func TestContextCompressor_UpdatePromptTokens(t *testing.T) {
	c := &ContextCompressor{logger: testLogger()}

	c.UpdatePromptTokens(0)
	if c.lastPromptTokens != 0 {
		t.Error("should not update with 0")
	}

	c.UpdatePromptTokens(5000)
	if c.lastPromptTokens != 5000 {
		t.Errorf("expected 5000, got %d", c.lastPromptTokens)
	}

	c.UpdatePromptTokens(-1)
	if c.lastPromptTokens != 5000 {
		t.Error("should not update with negative value")
	}
}

// ── helpers ─────────────────────────────────────────────────

func buildLargeConversation(turns int) []openai.ChatCompletionMessage {
	msgs := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "你是一个测试助手"},
	}
	for range turns {
		msgs = append(msgs,
			openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: "这是用户的问题，内容比较长：" + strings.Repeat("问题内容", 20)},
			openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: "这是助手的回答，内容也比较长：" + strings.Repeat("回答内容", 20)},
		)
	}
	return msgs
}
