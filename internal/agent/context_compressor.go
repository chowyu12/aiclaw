package agent

import (
	"context"
	"fmt"
	"strings"

	openai "github.com/chowyu12/go-openai"
	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/provider"
	"github.com/chowyu12/aiclaw/pkg/modelcaps"
)

const (
	defaultCompressThreshold = 0.50
	protectHeadCount         = 1
	defaultProtectTailCount  = 20
	toolPruneMaxRunes        = 200
	minMiddleMessages        = 5
	summaryMinTokens         = 2000
	summaryMaxTokens         = 4000
	summaryContentRatio      = 0.20
	summaryInputMaxRunes     = 1000
)

// ContextCompressor 使用 LLM 对对话上下文做有损摘要压缩。
// 算法分四阶段：裁剪旧工具输出 → 确定边界 → LLM 生成结构化摘要 → 组装压缩消息。
type ContextCompressor struct {
	contextWindow    int
	threshold        float64
	minProtectTail   int
	lastPromptTokens int
	previousSummary  string
	logger           *log.Entry
}

func NewContextCompressor(modelName string, l *log.Entry) *ContextCompressor {
	caps := modelcaps.GetModelCaps(modelName)
	return &ContextCompressor{
		contextWindow:  caps.ContextWindow,
		threshold:      defaultCompressThreshold,
		minProtectTail: defaultProtectTailCount,
		logger:         l,
	}
}

func (c *ContextCompressor) UpdatePromptTokens(tokens int) {
	if tokens > 0 {
		c.lastPromptTokens = tokens
	}
}

// NeedCompress 判断当前消息列表是否需要压缩。
// 使用 LLM 返回的真实 prompt token 数（如有），否则使用粗略估算。
func (c *ContextCompressor) NeedCompress(messages []openai.ChatCompletionMessage) bool {
	if c.contextWindow <= 0 {
		return false
	}
	if len(messages) <= protectHeadCount+c.minProtectTail+minMiddleMessages {
		return false
	}

	thresholdTokens := int(float64(c.contextWindow) * c.threshold)
	tokens := c.lastPromptTokens
	if tokens <= 0 {
		tokens = estimateMessagesTokens(messages)
	}
	return tokens >= thresholdTokens
}

// Compress 执行四阶段上下文压缩，返回压缩后的消息列表。
func (c *ContextCompressor) Compress(
	ctx context.Context,
	messages []openai.ChatCompletionMessage,
	llm provider.LLMProvider,
	modelName string,
) ([]openai.ChatCompletionMessage, error) {
	n := len(messages)
	if n <= protectHeadCount+c.minProtectTail {
		return messages, nil
	}

	// Phase 1: 裁剪保护区外的旧工具输出
	working := make([]openai.ChatCompletionMessage, n)
	copy(working, messages)

	tailStart := findTailStart(working, c.minProtectTail)
	for i := protectHeadCount; i < tailStart; i++ {
		if working[i].Role == openai.ChatMessageRoleTool {
			working[i].Content = truncateRunes(working[i].Content, toolPruneMaxRunes)
		}
	}

	// Phase 2: 确定 head / middle / tail 边界
	headEnd := protectHeadCount
	middle := working[headEnd:tailStart]
	if len(middle) < minMiddleMessages {
		return messages, nil
	}

	// Phase 3: 用 LLM 生成结构化摘要
	summary, err := c.generateSummary(ctx, middle, llm, modelName)
	if err != nil {
		return nil, fmt.Errorf("generate summary: %w", err)
	}

	// Phase 4: 组装压缩后的消息列表
	result := make([]openai.ChatCompletionMessage, 0, 1+1+n-tailStart)

	head := working[0]
	if !strings.Contains(head.Content, "[注意: 部分早期对话已被压缩为摘要]") {
		head.Content += "\n\n[注意: 部分早期对话已被压缩为摘要]"
	}
	result = append(result, head)

	summaryRole := openai.ChatMessageRoleAssistant
	if tailStart < n && working[tailStart].Role == openai.ChatMessageRoleAssistant {
		summaryRole = openai.ChatMessageRoleUser
	}
	result = append(result, openai.ChatCompletionMessage{
		Role:    summaryRole,
		Content: "[上下文压缩摘要]\n" + summary,
	})

	result = append(result, working[tailStart:]...)
	result = sanitizeToolCallSequence(result)

	c.previousSummary = summary
	c.lastPromptTokens = estimateMessagesTokens(result)

	c.logger.WithFields(log.Fields{
		"original_count":   n,
		"compressed_count": len(result),
		"middle_turns":     len(middle),
		"tail_start":       tailStart,
	}).Info("[Compress] context compressed via LLM summary")

	return result, nil
}

// findTailStart 从消息末尾反向查找尾部保护区起点，保证不拆分工具调用组。
func findTailStart(messages []openai.ChatCompletionMessage, minTail int) int {
	n := len(messages)
	idx := n - minTail
	if idx <= protectHeadCount {
		return protectHeadCount
	}
	return alignBoundaryBackward(messages, idx)
}

// alignBoundaryBackward 向前对齐索引，避免将 assistant(tool_calls) + tool 消息组拆开。
// 如果 idx 落在 tool 消息上，向前走到该组的 assistant 消息处，确保整个工具组在 tail 中。
func alignBoundaryBackward(messages []openai.ChatCompletionMessage, idx int) int {
	if idx <= 0 || idx >= len(messages) {
		return idx
	}
	for idx > 0 && messages[idx].Role == openai.ChatMessageRoleTool {
		idx--
	}
	return max(idx, protectHeadCount)
}

func (c *ContextCompressor) generateSummary(
	ctx context.Context,
	middle []openai.ChatCompletionMessage,
	llm provider.LLMProvider,
	modelName string,
) (string, error) {
	formatted := formatMessagesForSummary(middle)

	var userPrompt string
	if c.previousSummary != "" {
		userPrompt = fmt.Sprintf(iterativeCompressionPromptTpl, c.previousSummary, formatted)
	} else {
		userPrompt = fmt.Sprintf(compressionPromptTpl, formatted)
	}

	contentTokens := estimateTokens(formatted)
	maxTok := int(float64(contentTokens) * summaryContentRatio)
	maxTok = max(maxTok, summaryMinTokens)
	maxTok = min(maxTok, summaryMaxTokens)

	req := openai.ChatCompletionRequest{
		Model: modelName,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: userPrompt},
		},
		MaxCompletionTokens: maxTok,
		Temperature:         0.3,
	}

	resp, err := llm.CreateChatCompletion(ctx, req)
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("compression LLM returned empty choices")
	}
	return strings.TrimSpace(resp.Choices[0].Message.Content), nil
}

func formatMessagesForSummary(messages []openai.ChatCompletionMessage) string {
	var sb strings.Builder
	for _, msg := range messages {
		content := msg.Content
		if content == "" && len(msg.MultiContent) > 0 {
			var parts []string
			for _, p := range msg.MultiContent {
				if p.Type == openai.ChatMessagePartTypeText {
					parts = append(parts, p.Text)
				}
			}
			content = strings.Join(parts, " ")
		}

		switch msg.Role {
		case openai.ChatMessageRoleUser:
			sb.WriteString("User: ")
		case openai.ChatMessageRoleAssistant:
			if len(msg.ToolCalls) > 0 {
				names := make([]string, 0, len(msg.ToolCalls))
				for _, tc := range msg.ToolCalls {
					names = append(names, tc.Function.Name)
				}
				sb.WriteString(fmt.Sprintf("Assistant [调用工具: %s]: ", strings.Join(names, ", ")))
			} else {
				sb.WriteString("Assistant: ")
			}
		case openai.ChatMessageRoleTool:
			sb.WriteString(fmt.Sprintf("Tool(%s): ", msg.Name))
		default:
			sb.WriteString(fmt.Sprintf("%s: ", msg.Role))
		}

		rs := []rune(content)
		if len(rs) > summaryInputMaxRunes {
			content = string(rs[:summaryInputMaxRunes]) + "...(truncated)"
		}
		sb.WriteString(content)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// ── prompt 模板 ──────────────────────────────────────────────

var compressionPromptTpl = `你是一个对话压缩助手。请将以下对话历史压缩为结构化摘要，保留所有关键上下文信息。

%s

请严格按以下模板输出摘要：

## 目标
[用户正在尝试做什么]

## 约束与偏好
[用户偏好、代码风格、重要约束和决策]

## 进展
### 已完成
[已完成的工作 — 具体文件路径、执行的命令、结果]

### 进行中
[当前正在进行的工作]

### 受阻
[遇到的阻碍或问题]

## 关键决策
[重要的技术决策及原因]

## 相关文件
[读取、修改或创建的文件 — 附简要说明]

## 下一步
[接下来需要做什么]

## 关键上下文
[具体的值、错误信息、配置详情等不能丢失的信息]`

var iterativeCompressionPromptTpl = `你是一个对话压缩助手。以下是之前的摘要和新增的对话内容。请更新摘要，将新进展合并进来。

### 之前的摘要
%s

### 新增对话
%s

请严格按以下模板输出更新后的摘要：

## 目标
[用户正在尝试做什么]

## 约束与偏好
[用户偏好、代码风格、重要约束和决策]

## 进展
### 已完成
[已完成的工作 — 具体文件路径、执行的命令、结果]

### 进行中
[当前正在进行的工作]

### 受阻
[遇到的阻碍或问题]

## 关键决策
[重要的技术决策及原因]

## 相关文件
[读取、修改或创建的文件 — 附简要说明]

## 下一步
[接下来需要做什么]

## 关键上下文
[具体的值、错误信息、配置详情等不能丢失的信息]`
