package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	openai "github.com/chowyu12/go-openai"
	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/provider"
	harnesspkg "github.com/chowyu12/aiclaw/pkg/harness"
	"github.com/chowyu12/aiclaw/pkg/modelcaps"
)

// ── LLM 调用抽象 ─────────────────────────────────────────────

// llmRoundResult 一次 LLM 调用的结果（流式/阻塞通用）。
type llmRoundResult struct {
	content          string
	toolCalls        []openai.ToolCall
	rawToolCallCount int
	tokens           int
	promptTokens     int
	finishReason     openai.FinishReason
	outcome          llmRoundOutcome
}

// llmCaller 封装单次 LLM 请求，流式与阻塞各实现一份。
type llmCaller func(ctx context.Context, req openai.ChatCompletionRequest) (llmRoundResult, error)

type llmRoundOutcome string

const (
	llmRoundFinalCandidate              llmRoundOutcome = "final_candidate"
	llmRoundCompleteToolCalls           llmRoundOutcome = "complete_tool_calls"
	llmRoundTruncatedToolCall           llmRoundOutcome = "truncated_tool_call"
	llmRoundTruncatedContent            llmRoundOutcome = "truncated_content"
	llmRoundContentFiltered             llmRoundOutcome = "content_filtered"
	llmRoundProviderResourceInterrupted llmRoundOutcome = "provider_resource_interrupted"
	llmRoundEmptyResponse               llmRoundOutcome = "empty_response"
)

func newLLMRoundResult(content string, toolCalls []openai.ToolCall, tokens, promptTokens int, finishReason openai.FinishReason) llmRoundResult {
	outcome, executableToolCalls := classifyLLMRound(content, toolCalls, finishReason)
	return llmRoundResult{
		content:          content,
		toolCalls:        executableToolCalls,
		rawToolCallCount: len(toolCalls),
		tokens:           tokens,
		promptTokens:     promptTokens,
		finishReason:     finishReason,
		outcome:          outcome,
	}
}

func classifyLLMRound(content string, toolCalls []openai.ToolCall, finishReason openai.FinishReason) (llmRoundOutcome, []openai.ToolCall) {
	switch finishReason {
	case openai.FinishReasonContentFilter:
		return llmRoundContentFiltered, nil
	case openai.FinishReasonLength:
		if len(toolCalls) > 0 {
			return llmRoundTruncatedToolCall, nil
		}
		return llmRoundTruncatedContent, nil
	}
	if string(finishReason) == "insufficient_system_resource" {
		return llmRoundProviderResourceInterrupted, nil
	}
	if len(toolCalls) > 0 {
		if finishReason == "" || finishReason == openai.FinishReasonToolCalls || finishReason == openai.FinishReasonFunctionCall {
			return llmRoundCompleteToolCalls, toolCalls
		}
		return llmRoundTruncatedToolCall, nil
	}
	if strings.TrimSpace(content) == "" {
		return llmRoundEmptyResponse, nil
	}
	return llmRoundFinalCandidate, nil
}

type llmRoundRecovery struct {
	reason       string
	action       string
	finalMessage string
	retryable    bool
}

func (r llmRoundRecovery) Prompt() string {
	action := strings.TrimSpace(r.action)
	if action == "" {
		action = "请重新生成本轮输出；如果无法继续，请明确说明阻塞原因。"
	}
	reason := strings.TrimSpace(r.reason)
	if reason == "" {
		reason = "上一轮模型输出未形成可执行或可交付结果"
	}
	return reason + "。\n" + action
}

func llmRoundRecoveryFor(result llmRoundResult, contract harnesspkg.TaskContract, evidence harnesspkg.EvidenceLedger) (llmRoundRecovery, bool) {
	switch result.outcome {
	case llmRoundTruncatedToolCall:
		return llmRoundRecovery{
			reason:    fmt.Sprintf("模型工具调用被截断（finish_reason=%s），本轮工具未执行", result.finishReason),
			action:    "请重新发起完整工具调用；如果参数过长，请缩短代码、减少内联数据、分批处理，或先生成较小的中间结果。",
			retryable: true,
		}, true
	case llmRoundTruncatedContent:
		return llmRoundRecovery{
			reason:    fmt.Sprintf("模型输出被截断（finish_reason=%s）", result.finishReason),
			action:    "请继续完成未输出的结果；如果需要生成文件或运行代码，请改用完整工具调用，不要只输出进度说明。",
			retryable: true,
		}, true
	case llmRoundProviderResourceInterrupted:
		return llmRoundRecovery{
			reason:    "模型服务推理资源不足，上一轮生成被中断",
			action:    "请重新尝试本轮输出；如果仍然失败，请简化输出规模或分批完成。",
			retryable: true,
		}, true
	case llmRoundContentFiltered:
		return llmRoundRecovery{
			reason:       "模型输出被安全策略过滤",
			finalMessage: "模型输出被安全策略过滤，无法返回本轮结果。请调整输入或输出要求后重试。",
		}, true
	case llmRoundEmptyResponse:
		if !shouldRecoverEmptyLLMRound(contract, evidence) {
			return llmRoundRecovery{}, false
		}
		return llmRoundRecovery{
			reason:    "模型返回空内容且没有工具调用，但执行契约尚未闭合",
			action:    "请不要空回复；如果任务尚未完成，直接调用下一步工具或更新 plan；如果无法继续，请明确说明阻塞原因。",
			retryable: true,
		}, true
	default:
		return llmRoundRecovery{}, false
	}
}

func shouldRecoverEmptyLLMRound(contract harnesspkg.TaskContract, evidence harnesspkg.EvidenceLedger) bool {
	if len(evidence.TerminalBlockers()) > 0 {
		return false
	}
	if contract.RequirePlanTerminal && !harnesspkg.PlanReadyForFinalAnswer(evidence.Plan) {
		return true
	}
	requiredArtifacts := contract.RequiredArtifactCount
	if requiredArtifacts == 0 && (contract.OutputMode == harnesspkg.OutputModeFile || contract.OutputMode == harnesspkg.OutputModeMixed) {
		requiredArtifacts = 1
	}
	if requiredArtifacts > 0 && evidence.ActualArtifactCount() < requiredArtifacts {
		return true
	}
	return contract.RequireEvidence && !evidence.HasSuccessfulEvidence()
}

func blockingCaller(llm provider.LLMProvider) llmCaller {
	return func(ctx context.Context, req openai.ChatCompletionRequest) (llmRoundResult, error) {
		resp, err := llm.CreateChatCompletion(ctx, req)
		if err != nil {
			return llmRoundResult{}, err
		}
		if len(resp.Choices) == 0 {
			return newLLMRoundResult("", nil, resp.Usage.TotalTokens, resp.Usage.PromptTokens, ""), nil
		}
		ch := resp.Choices[0]
		return newLLMRoundResult(ch.Message.Content, ch.Message.ToolCalls, resp.Usage.TotalTokens, resp.Usage.PromptTokens, ch.FinishReason), nil
	}
}

func streamingCaller(llm provider.LLMProvider, convUUID string, onChunk func(model.StreamChunk) error) llmCaller {
	return func(ctx context.Context, req openai.ChatCompletionRequest) (llmRoundResult, error) {
		req.Stream = true
		req.StreamOptions = &openai.StreamOptions{IncludeUsage: true}

		s, err := llm.CreateChatCompletionStream(ctx, req)
		if err != nil {
			return llmRoundResult{}, err
		}
		defer s.Close()

		var buf strings.Builder
		var toolCalls []openai.ToolCall
		var tokens, promptTokens int
		var finishReason openai.FinishReason

		for {
			resp, recvErr := s.Recv()
			if errors.Is(recvErr, io.EOF) {
				break
			}
			if recvErr != nil {
				return llmRoundResult{}, recvErr
			}
			if resp.Usage != nil {
				tokens = resp.Usage.TotalTokens
				promptTokens = resp.Usage.PromptTokens
			}
			if len(resp.Choices) == 0 {
				continue
			}
			choice := resp.Choices[0]
			if choice.FinishReason != "" {
				finishReason = choice.FinishReason
			}
			if choice.Delta.Content != "" {
				buf.WriteString(choice.Delta.Content)
				if err := onChunk(model.StreamChunk{ConversationID: convUUID, Delta: choice.Delta.Content}); err != nil {
					return llmRoundResult{}, err
				}
			}
			for _, tc := range choice.Delta.ToolCalls {
				idx := 0
				if tc.Index != nil {
					idx = *tc.Index
				}
				for len(toolCalls) <= idx {
					toolCalls = append(toolCalls, openai.ToolCall{Type: openai.ToolTypeFunction})
				}
				if tc.ID != "" {
					toolCalls[idx].ID = tc.ID
				}
				if tc.Type != "" {
					toolCalls[idx].Type = tc.Type
				}
				toolCalls[idx].Function.Name += tc.Function.Name
				toolCalls[idx].Function.Arguments += tc.Function.Arguments
			}
		}

		return newLLMRoundResult(buf.String(), toolCalls, tokens, promptTokens, finishReason), nil
	}
}

// refreshPlanInSystemMessage 在每轮 LLM 调用前，用最新 Plan State 替换 system prompt 中的 plan 段落。
func refreshPlanInSystemMessage(messages []openai.ChatCompletionMessage, pm *PlanManager) {
	if len(messages) == 0 || messages[0].Role != openai.ChatMessageRoleSystem {
		return
	}
	newBlock := ""
	if pm != nil {
		newBlock = pm.PromptBlock(context.Background())
	}
	content := messages[0].Content

	const planHeader = "\n\n<plan_state>\n"
	if idx := strings.Index(content, planHeader); idx >= 0 {
		content = content[:idx]
	}
	if newBlock != "" {
		content += "\n\n" + newBlock
	}
	messages[0].Content = content
}

func toolsSentToLLM(st *harnessTurnState) []openai.Tool {
	if !st.TSMode {
		return st.AllToolDefs
	}
	if st.cachedLLMDefs != nil && len(st.Discovered) == st.lastDiscoveredLen {
		return st.cachedLLMDefs
	}
	st.cachedLLMDefs = buildToolSearchDefs(st.AllToolDefs, st.Discovered)
	st.lastDiscoveredLen = len(st.Discovered)
	return st.cachedLLMDefs
}

// ── 工具函数 ────────────────────────────────────────────────

func recordBuiltInWebSearchStep(ctx context.Context, ec *execContext, extraBody map[string]any) {
	input := map[string]any{
		"mode":  model.WebSearchModeBuiltin,
		"query": ec.userMsg,
	}
	output := map[string]any{
		"enable_search": extraBody["enable_search"] == true,
		"summary":       "Built-in model web search is enabled for this LLM request. Search results are returned in the model response.",
	}
	inputJSON, _ := json.MarshalIndent(input, "", "  ")
	outputJSON, _ := json.MarshalIndent(output, "", "  ")
	ec.tracker.RecordStep(
		ctx,
		model.StepToolCall,
		"web_search",
		string(inputJSON),
		string(outputJSON),
		model.StepSuccess,
		"",
		0,
		0,
		&model.StepMetadata{
			Provider: ec.prov.Name,
			Model:    ec.ag.ModelName,
			ToolName: "builtin_web_search",
		},
	)
}

func extractContent(resp openai.ChatCompletionResponse) string {
	if len(resp.Choices) == 0 {
		return ""
	}
	return resp.Choices[0].Message.Content
}

func applyModelCaps(req *openai.ChatCompletionRequest, ag *model.Agent, pt model.ProviderType, l *log.Entry) {
	caps := modelcaps.GetModelCaps(ag.ModelName)
	if caps.NoTemperature || caps.NoTopP {
		l.WithFields(log.Fields{
			"model": ag.ModelName, "no_temperature": caps.NoTemperature, "no_top_p": caps.NoTopP,
		}).Debug("[LLM] model caps applied")
	}
	if ag.Temperature > 0 && !caps.NoTemperature {
		req.Temperature = float32(ag.Temperature)
	}
	if ag.MaxTokens > 0 {
		req.MaxCompletionTokens = ag.MaxTokens
	}

	extra := map[string]any{}
	effort := ag.EffectiveReasoningEffort()

	thinkingEnabled := false
	switch {
	case caps.AlwaysThinking:
		req.ReasoningEffort = effort
		thinkingEnabled = true
		l.WithFields(log.Fields{"model": ag.ModelName, "effort": effort}).Debug("[LLM] always-thinking model, effort applied")
	case !ag.EnableThinking:
		extra["enable_thinking"] = false
		req.ChatTemplateKwargs = map[string]any{"enable_thinking": false}
		l.WithField("model", ag.ModelName).Debug("[LLM] thinking disabled")
	default:
		req.ReasoningEffort = effort
		extra["enable_thinking"] = true
		req.ChatTemplateKwargs = map[string]any{"enable_thinking": true}
		thinkingEnabled = true
		l.WithFields(log.Fields{"model": ag.ModelName, "effort": effort}).Debug("[LLM] thinking enabled")
	}

	// DashScope (Qwen) 在 OpenAI 兼容模式下，思考开启后若不显式传 thinking_budget，
	// 会取模型最大思维链长度（如 32768）作为默认值；当用户主动设了较小的
	// max_completion_tokens 时（如 8192），就会触发
	// "max_completion_tokens must be greater than thinking_budget" 校验失败。
	// 仅在用户主动设置了 max_completion_tokens 时介入：按 reasoning_effort 比例
	// 显式下发 budget，并保证严格小于 max_completion_tokens；
	// 用户未设 max_tokens（=0）时不干预，由 DashScope 决定整体预算。
	if thinkingEnabled && pt == model.ProviderQwen && req.MaxCompletionTokens > 0 {
		if budget := computeQwenThinkingBudget(req.MaxCompletionTokens, effort); budget > 0 {
			extra["thinking_budget"] = budget
			l.WithFields(log.Fields{
				"model":                 ag.ModelName,
				"thinking_budget":       budget,
				"max_completion_tokens": req.MaxCompletionTokens,
			}).Debug("[LLM] qwen thinking_budget applied")
		}
	}

	if webSearchEffective(ag) {
		extra["enable_search"] = true
		req.ChatTemplateKwargs = mergeMap(req.ChatTemplateKwargs, map[string]any{"enable_search": true})
		l.WithField("model", ag.ModelName).Debug("[LLM] web search enabled")
	}

	if len(extra) > 0 {
		req.ExtraBody = extra
	}
}

// computeQwenThinkingBudget 根据 reasoning_effort 与 max_completion_tokens 计算
// 适用于 DashScope（百炼/Qwen）OpenAI 兼容接口的 thinking_budget。
// DashScope 对深度思考模型校验 max_completion_tokens > thinking_budget，
// 因此返回值会严格小于 maxOut（保留至少 256 token 作为可见输出预算）。
// 当 maxOut 太小、无法分出合理 budget 时返回 0，由调用方决定不下发该参数。
// 调用方需先确保 maxOut > 0；maxOut <= 0 时返回 0 表示"不干预，交由服务商决定"。
func computeQwenThinkingBudget(maxOut int, effort string) int {
	const (
		minBudget       = 1024
		minVisibleQuota = 256
	)
	if maxOut <= 0 {
		return 0
	}

	budget := int(float64(maxOut) * provider.EffortBudgetRatio(effort))
	if budget < minBudget {
		budget = minBudget
	}
	if budget >= maxOut-minVisibleQuota {
		budget = maxOut - minVisibleQuota
	}
	if budget < minBudget {
		return 0
	}
	return budget
}

// webSearchEffective 判断当前 Agent 是否真正启用了内置联网搜索能力。
// 需同时满足：Agent 配置开启 && 选择内置模式 && 模型 caps 支持。
func webSearchEffective(ag *model.Agent) bool {
	if ag == nil || !ag.EnableWebSearch || ag.EffectiveWebSearchMode() != model.WebSearchModeBuiltin {
		return false
	}
	return modelcaps.GetModelCaps(ag.ModelName).WebSearch
}

func externalWebSearchEffective(ag *model.Agent) bool {
	return ag != nil && ag.EnableWebSearch && ag.EffectiveWebSearchMode() == model.WebSearchModeExternal
}

func webSearchPromptEnabled(ag *model.Agent) bool {
	return webSearchEffective(ag) || externalWebSearchEffective(ag)
}

func mergeMap(dst, src map[string]any) map[string]any {
	if dst == nil {
		return src
	}
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func logResourceSummary(l *log.Entry, agentTools []model.Tool, skills []model.Skill) {
	toolNames := make([]string, 0, len(agentTools))
	for _, t := range agentTools {
		toolNames = append(toolNames, t.Name)
	}
	skillNames := make([]string, 0, len(skills))
	for _, s := range skills {
		skillNames = append(skillNames, s.Name)
	}
	l.WithFields(log.Fields{"tools": toolNames, "skills": skillNames}).Debug("[Execute]    resources loaded")
	for _, sk := range skills {
		fields := log.Fields{"skill": sk.Name, "has_instruction": sk.Instruction != ""}
		if sk.Instruction != "" {
			fields["instruction_len"] = len(sk.Instruction)
		}
		l.WithFields(fields).Debug("[Execute]    skill detail")
	}
}

func logMessages(l *log.Entry, messages []openai.ChatCompletionMessage) {
	for i, msg := range messages {
		content := msg.Content
		if content == "" && len(msg.MultiContent) > 0 {
			var parts []string
			for _, p := range msg.MultiContent {
				if p.Type == openai.ChatMessagePartTypeText {
					parts = append(parts, p.Text)
				}
			}
			content = strings.Join(parts, "")
		}
		l.WithFields(log.Fields{"idx": i, "role": msg.Role, "len": len(content), "text": truncateLog(content, 300)}).Debug("[LLM]    message")
	}
}

func truncateLog(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// sanitizeMessages 对发送给 LLM 的消息做必要的修正：
//  1. tool 角色和带 tool_calls 的 assistant 必须有 content（部分 provider 的硬性要求，
//     go-openai SDK 用 omitempty 会省略空字符串导致 400）
//  2. 修复 tool_call arguments 中的残损 JSON（避免 provider 400 拒绝）
//
// 先扫描一遍判断是否真的需要修正，避免每轮都完整拷贝消息数组。
func sanitizeMessages(msgs []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	needsFix := false
	for i := range msgs {
		needContent := msgs[i].Role == openai.ChatMessageRoleTool ||
			(msgs[i].Role == openai.ChatMessageRoleAssistant && len(msgs[i].ToolCalls) > 0)
		if needContent && msgs[i].Content == "" && len(msgs[i].MultiContent) == 0 {
			needsFix = true
			break
		}
		for j := range msgs[i].ToolCalls {
			if a := msgs[i].ToolCalls[j].Function.Arguments; a != "" && !json.Valid([]byte(a)) {
				needsFix = true
				break
			}
		}
		if needsFix {
			break
		}
	}
	if !needsFix {
		return msgs
	}

	out := make([]openai.ChatCompletionMessage, len(msgs))
	copy(out, msgs)
	for i := range out {
		needContent := out[i].Role == openai.ChatMessageRoleTool ||
			(out[i].Role == openai.ChatMessageRoleAssistant && len(out[i].ToolCalls) > 0)
		if needContent && out[i].Content == "" && len(out[i].MultiContent) == 0 {
			out[i].Content = " "
		}
		for j := range out[i].ToolCalls {
			args := out[i].ToolCalls[j].Function.Arguments
			if args != "" && !json.Valid([]byte(args)) {
				out[i].ToolCalls[j].Function.Arguments = "{}"
			}
		}
	}
	return out
}
