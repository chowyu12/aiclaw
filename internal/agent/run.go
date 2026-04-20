package agent

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	openai "github.com/chowyu12/go-openai"
	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/provider"
	"github.com/chowyu12/aiclaw/pkg/modelcaps"
)

// ── LLM 调用抽象 ─────────────────────────────────────────────

// llmRoundResult 一次 LLM 调用的结果（流式/阻塞通用）。
type llmRoundResult struct {
	content      string
	toolCalls    []openai.ToolCall
	tokens       int
	promptTokens int
}

// llmCaller 封装单次 LLM 请求，流式与阻塞各实现一份。
type llmCaller func(ctx context.Context, req openai.ChatCompletionRequest) (llmRoundResult, error)

func blockingCaller(llm provider.LLMProvider) llmCaller {
	return func(ctx context.Context, req openai.ChatCompletionRequest) (llmRoundResult, error) {
		resp, err := llm.CreateChatCompletion(ctx, req)
		if err != nil {
			return llmRoundResult{}, err
		}
		if len(resp.Choices) == 0 {
			return llmRoundResult{tokens: resp.Usage.TotalTokens, promptTokens: resp.Usage.PromptTokens}, nil
		}
		ch := resp.Choices[0]
		return llmRoundResult{
			content:      ch.Message.Content,
			toolCalls:    ch.Message.ToolCalls,
			tokens:       resp.Usage.TotalTokens,
			promptTokens: resp.Usage.PromptTokens,
		}, nil
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

		if finishReason != openai.FinishReasonToolCalls {
			toolCalls = nil
		}
		return llmRoundResult{content: buf.String(), toolCalls: toolCalls, tokens: tokens, promptTokens: promptTokens}, nil
	}
}

// ── 统一执行循环 ─────────────────────────────────────────────

func (e *Executor) run(ctx context.Context, ec *execContext, call llmCaller, streaming bool) (*ExecuteResult, error) {
	if t := ec.ag.TimeoutSeconds(); t > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(t)*time.Second)
		defer cancel()
	}

	st, err := e.bootstrapAgentTurn(ctx, ec, streaming)
	if err != nil {
		return nil, err
	}

	st.loopDet = newToolLoopDetector(ec.l)
	st.calledTools = make(map[string]bool)

	budget := NewBudgetTracker(ec.ag.TokenBudget)
	compressor := NewContextCompressor(ec.ag.ModelName, ec.l)

	var totalTokens int
	var finalContent string
	totalStart := time.Now()
	maxIter := ec.ag.IterationLimit()
	completed := false

	for i := range maxIter {
		// 上下文压缩检查：token 占用超过模型窗口阈值时，用 LLM 摘要压缩中间轮次
		if compressor.NeedCompress(st.Messages) {
			compressionModel := cmp.Or(ec.ag.FastModelName, ec.ag.ModelName)
			compressed, compErr := compressor.Compress(ctx, st.Messages, ec.llmProv, compressionModel)
			if compErr != nil {
				ec.l.WithError(compErr).Warn("[Compress] context compression failed, continuing with full context")
			} else {
				st.Messages = compressed
			}
		}

		// PreLLMCall hook
		if action := e.hooks.Fire(ctx, HookPreLLMCall, &HookPayload{Model: ec.ag.ModelName, Round: i + 1}); action == HookAbort {
			ec.l.WithField("round", i+1).Warn("[Hook] agent execution aborted by pre_llm_call hook")
			return nil, errors.New("agent execution aborted by hook")
		}

		// 每轮刷新 system prompt 中的 todo 列表
		if i > 0 && !ec.ephemeral {
			refreshTodoInSystemMessage(st.Messages, ec.conv.UUID)
		}

		msgs := sanitizeMessages(st.Messages)
		tools := filterURLGatedTools(toolsSentToLLM(st), userMessagesHaveURL(msgs))
		req := openai.ChatCompletionRequest{
			Model:    ec.ag.ModelName,
			Messages: msgs,
			Tools:    tools,
		}
		applyModelCaps(&req, ec.ag, ec.prov.Type, ec.l)

		ec.l.WithFields(log.Fields{"round": i + 1, "model": ec.ag.ModelName}).Info("[LLM] >> call")
		iterStart := time.Now()
		result, err := call(ctx, req)
		iterDur := time.Since(iterStart)

		if err != nil {
			ec.l.WithFields(log.Fields{"round": i + 1, "duration": iterDur}).WithError(err).Error("[LLM] << failed")
			ec.tracker.RecordStep(ctx, model.StepLLMCall, ec.ag.ModelName, ec.userMsg, "", model.StepError, err.Error(), iterDur, 0, ec.stepMeta())
			if st.llmRetries < maxLLMRetries && isTransientLLMError(err) {
				st.llmRetries++
				ec.l.WithFields(log.Fields{"retry": st.llmRetries, "max": maxLLMRetries}).Warn("[LLM] transient error, retrying")
				continue
			}
			return nil, fmt.Errorf("generate content: %w", err)
		}

		totalTokens += result.tokens
		budget.Add(result.tokens)
		compressor.UpdatePromptTokens(result.promptTokens)

		// PostLLMCall hook
		e.hooks.Fire(ctx, HookPostLLMCall, &HookPayload{Model: ec.ag.ModelName, Round: i + 1, Tokens: result.tokens})

		if len(result.toolCalls) == 0 {
			finalContent = result.content
			completed = true
			ec.l.WithFields(log.Fields{
				"round": i + 1, "duration": iterDur, "tokens": result.tokens,
				"len": len(finalContent), "preview": truncateLog(finalContent, 200),
			}).Info("[LLM] << final answer")
			break
		}

		// 检查 token 预算：超出后不再执行工具，沿用当前回复内容收尾
		if budget.Exceeded() {
			finalContent = result.content
			if finalContent == "" {
				finalContent = fmt.Sprintf("已达到 token 预算上限（%d），已消耗 %d token。请开始新对话继续。",
					ec.ag.TokenBudget, budget.Consumed())
			}
			completed = true
			ec.l.WithFields(log.Fields{
				"round": i + 1, "budget_limit": ec.ag.TokenBudget, "consumed": budget.Consumed(),
			}).Warn("[Budget] token budget exceeded, stopping after this round")
			break
		}

		tcNames := make([]string, 0, len(result.toolCalls))
		for _, tc := range result.toolCalls {
			tcNames = append(tcNames, tc.Function.Name)
		}
		ec.l.WithFields(log.Fields{"round": i + 1, "duration": iterDur, "tokens": result.tokens, "tool_calls": tcNames}).Info("[LLM] << tool calls")

		asst := openai.ChatCompletionMessage{
			Role:      openai.ChatMessageRoleAssistant,
			Content:   result.content,
			ToolCalls: result.toolCalls,
		}
		e.appendAssistantToolRound(ctx, ec, st, asst)
	}

	if !completed {
		ec.l.WithField("max_iterations", maxIter).Error("[Execute] max iterations reached")
		errMsg := fmt.Sprintf("已达到最大迭代次数 %d，Agent 未能给出最终回答", maxIter)
		ec.tracker.RecordStep(ctx, model.StepLLMCall, ec.ag.ModelName, ec.userMsg, "", model.StepError, errMsg, time.Since(totalStart), totalTokens, ec.stepMeta())
		return nil, errors.New(errMsg)
	}

	return e.saveResult(ctx, ec, st, finalContent, totalTokens, time.Since(totalStart))
}

// ── bootstrapAgentTurn ──────────────────────────────────────

type agentRunState struct {
	Messages    []openai.ChatCompletionMessage
	ToolMap     map[string]Tool
	AllToolDefs []openai.Tool
	TSMode      bool
	Discovered  map[string]bool

	mu          sync.Mutex
	loopDet     *toolLoopDetector
	calledTools map[string]bool
	llmRetries  int

	lastDiscoveredLen int
	cachedLLMDefs     []openai.Tool
}

func (e *Executor) bootstrapAgentTurn(ctx context.Context, ec *execContext, streaming bool) (*agentRunState, error) {
	var history []openai.ChatCompletionMessage
	if !ec.ephemeral {
		var err error
		history, err = e.memory.LoadHistory(ctx, ec.conv.ID, ec.ag.HistoryLimit())
		if err != nil {
			ec.l.WithError(err).Error("[LLM] load history failed")
			return nil, err
		}

		if _, err := e.memory.SaveUserMessage(ctx, ec.conv.ID, ec.userMsg, ec.files); err != nil {
			ec.l.WithError(err).Error("[LLM] save user message failed")
			return nil, err
		}
	}

	var toolMap map[string]Tool
	var allToolDefs []openai.Tool
	tsMode := false
	discovered := map[string]bool{}

	if ec.hasTools() {
		lcTools := e.registry.BuildTrackedTools(ec.agentTools, ec.tracker, ec.toolSkillMap)
		lcTools = append(lcTools, ec.mcpTools...)
		lcTools = append(lcTools, ec.skillTools...)
		toolMap = make(map[string]Tool, len(lcTools))
		for _, t := range lcTools {
			toolMap[t.Name()] = t
		}
		allToolDefs = buildLLMToolDefs(ec.agentTools, ec.mcpTools, ec.skillTools)

		tsMode = UseLazyToolSearch(ec.ag.ToolSearchEnabled, len(allToolDefs))
		tag := ""
		if streaming {
			tag = "stream + "
		}
		if tsMode {
			preloadSkillTools(ec.toolSkillMap, discovered)
			preloadEssentialBuiltins(discovered)
			ec.l.WithFields(log.Fields{"total_tools": len(allToolDefs), "preloaded": len(discovered)}).Debug("[Execute]    mode = " + tag + "tool-search")
		} else if ec.ag.ToolSearchEnabled && len(allToolDefs) > 0 {
			ec.l.WithFields(log.Fields{"total_tools": len(allToolDefs), "threshold": ToolSearchAutoFullThreshold}).Debug("[Execute]    mode = " + tag + "tool-augmented (auto full catalog)")
		} else {
			ec.l.Debug("[Execute]    mode = " + tag + "tool-augmented")
		}
	} else {
		if streaming {
			ec.l.Debug("[Execute]    mode = stream")
		} else {
			ec.l.Debug("[Execute]    mode = simple")
		}
	}

	memosCtx := recallMemories(ctx, ec.userMsg, ec.ag)
	sessionMem := loadSessionMemory(e.ws, ec.ag.UUID, ec.conv.UUID)
	persistentMem := loadPersistentMemory(e.ws)
	todoBlock := loadTodoBlock(ec.conv.UUID)

	var msgTools []model.Tool
	var msgToolSkillMap map[string]string
	if !tsMode {
		msgTools = ec.agentTools
		msgToolSkillMap = ec.toolSkillMap
	}
	messages := buildMessages(messagesBuildInput{
		Agent:            ec.ag,
		Skills:           ec.skills,
		History:          history,
		UserMsg:          ec.userMsg,
		AgentTools:       msgTools,
		ToolSkillMap:     msgToolSkillMap,
		Files:            ec.files,
		PersistentMemory: persistentMem,
		MemosContext:     memosCtx,
		SessionMemory:    sessionMem,
		TodoBlock:        todoBlock,
		ToolSearchMode:   tsMode,
		WebSearchEnabled: webSearchEffective(ec.ag),
		WS:               e.ws,
	})
	logMessages(ec.l, messages)

	return &agentRunState{
		Messages:    messages,
		ToolMap:     toolMap,
		AllToolDefs: allToolDefs,
		TSMode:      tsMode,
		Discovered:  discovered,
	}, nil
}

// refreshTodoInSystemMessage 在每轮 LLM 调用前，用最新的 todo 列表替换 system prompt 中的 todo 段落。
func refreshTodoInSystemMessage(messages []openai.ChatCompletionMessage, convUUID string) {
	if len(messages) == 0 || messages[0].Role != openai.ChatMessageRoleSystem {
		return
	}
	newBlock := loadTodoBlock(convUUID)
	content := messages[0].Content

	const todoHeader = "\n\n## 当前任务\n"
	if idx := strings.Index(content, todoHeader); idx >= 0 {
		content = content[:idx]
	}
	if newBlock != "" {
		content += "\n\n" + newBlock
	}
	messages[0].Content = content
}

func toolsSentToLLM(st *agentRunState) []openai.Tool {
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

// ── LLM 瞬态错误重试 ─────────────────────────────────────────

const maxLLMRetries = 2

// isTransientLLMError 判断 LLM 错误是否为可自动重试的瞬态错误。
func isTransientLLMError(err error) bool {
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.HTTPStatusCode {
		case 429, 500, 502, 503, 504:
			return true
		}
	}
	var reqErr *openai.RequestError
	if errors.As(err, &reqErr) {
		switch reqErr.HTTPStatusCode {
		case 429, 500, 502, 503, 504:
			return true
		}
	}
	msg := err.Error()
	return strings.Contains(msg, "too many empty messages") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "unexpected EOF")
}

// ── 工具函数 ────────────────────────────────────────────────

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

	switch {
	case caps.AlwaysThinking:
		req.ReasoningEffort = effort
		l.WithFields(log.Fields{"model": ag.ModelName, "effort": effort}).Debug("[LLM] always-thinking model, effort applied")
	case !ag.EnableThinking:
		extra["enable_thinking"] = false
		req.ChatTemplateKwargs = map[string]any{"enable_thinking": false}
		l.WithField("model", ag.ModelName).Debug("[LLM] thinking disabled")
	default:
		req.ReasoningEffort = effort
		extra["enable_thinking"] = true
		req.ChatTemplateKwargs = map[string]any{"enable_thinking": true}
		l.WithFields(log.Fields{"model": ag.ModelName, "effort": effort}).Debug("[LLM] thinking enabled")
	}

	if webSearchEffective(ag) {
		switch pt {
		case model.ProviderQwen:
			extra["enable_search"] = true
		case model.ProviderOpenAI:
			extra["web_search_options"] = map[string]any{
				"search_context_size": "medium",
				"user_location":      map[string]any{"type": "approximate"},
			}
		}
		req.ChatTemplateKwargs = mergeMap(req.ChatTemplateKwargs, map[string]any{"enable_search": true})
		l.WithField("model", ag.ModelName).Debug("[LLM] web search enabled")
	}

	if len(extra) > 0 {
		req.ExtraBody = extra
	}
}

// webSearchEffective 判断当前 Agent 是否真正启用了内置联网搜索能力。
// 需同时满足：Agent 配置开启 && 模型 caps 支持。
func webSearchEffective(ag *model.Agent) bool {
	if ag == nil || !ag.EnableWebSearch {
		return false
	}
	return modelcaps.GetModelCaps(ag.ModelName).WebSearch
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
