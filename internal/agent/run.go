package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/provider"
)

// ── LLM 调用抽象 ─────────────────────────────────────────────

// llmRoundResult 一次 LLM 调用的结果（流式/阻塞通用）。
type llmRoundResult struct {
	content   string
	toolCalls []openai.ToolCall
	tokens    int
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
			return llmRoundResult{tokens: resp.Usage.TotalTokens}, nil
		}
		ch := resp.Choices[0]
		return llmRoundResult{
			content:   ch.Message.Content,
			toolCalls: ch.Message.ToolCalls,
			tokens:    resp.Usage.TotalTokens,
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
		var tokens int
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
		return llmRoundResult{content: buf.String(), toolCalls: toolCalls, tokens: tokens}, nil
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

	var totalTokens int
	var finalContent string
	totalStart := time.Now()
	maxIter := ec.ag.IterationLimit()
	completed := false

	for i := range maxIter {
		req := openai.ChatCompletionRequest{
			Model:    ec.ag.ModelName,
			Messages: st.Messages,
			Tools:    toolsSentToLLM(st.TSMode, st.AllToolDefs, st.Discovered),
		}
		applyModelCaps(&req, ec.ag, ec.l)

		ec.l.WithFields(log.Fields{"round": i + 1, "model": ec.ag.ModelName}).Info("[LLM] >> call")
		iterStart := time.Now()
		result, err := call(ctx, req)
		iterDur := time.Since(iterStart)

		if err != nil {
			ec.l.WithFields(log.Fields{"round": i + 1, "duration": iterDur}).WithError(err).Error("[LLM] << failed")
			ec.tracker.RecordStep(ctx, model.StepLLMCall, ec.ag.ModelName, ec.userMsg, "", model.StepError, err.Error(), iterDur, 0, ec.stepMeta())
			return nil, fmt.Errorf("generate content: %w", err)
		}

		totalTokens += result.tokens

		if len(result.toolCalls) == 0 {
			finalContent = result.content
			completed = true
			ec.l.WithFields(log.Fields{
				"round": i + 1, "duration": iterDur, "tokens": result.tokens,
				"len": len(finalContent), "preview": truncateLog(finalContent, 200),
			}).Info("[LLM] << final answer")
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

	if ec.hasTools() {
		e.recordUsedSkillSteps(ctx, ec.skills, ec.toolSkillMap, st.calledTools, ec.tracker)
	}
	return e.saveResult(ctx, ec, finalContent, totalTokens, time.Since(totalStart))
}

// ── bootstrapAgentTurn（从 executor_run.go 迁入） ────────────

type agentRunState struct {
	Messages    []openai.ChatCompletionMessage
	ToolMap     map[string]Tool
	AllToolDefs []openai.Tool
	TSMode      bool
	Discovered  map[string]bool

	loopDet     *toolLoopDetector
	calledTools map[string]bool
}

func (e *Executor) bootstrapAgentTurn(ctx context.Context, ec *execContext, streaming bool) (*agentRunState, error) {
	history, err := e.memory.LoadHistory(ctx, ec.conv.ID, ec.ag.HistoryLimit())
	if err != nil {
		ec.l.WithError(err).Error("[LLM] load history failed")
		return nil, err
	}

	if _, err := e.memory.SaveUserMessage(ctx, ec.conv.ID, ec.userMsg, ec.files); err != nil {
		ec.l.WithError(err).Error("[LLM] save user message failed")
		return nil, err
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
			ec.l.WithFields(log.Fields{"total_tools": len(allToolDefs), "skill_preloaded": len(discovered)}).Info("[Execute]    mode = " + tag + "tool-search")
		} else if ec.ag.ToolSearchEnabled && len(allToolDefs) > 0 {
			ec.l.WithFields(log.Fields{"total_tools": len(allToolDefs), "threshold": ToolSearchAutoFullThreshold}).Info("[Execute]    mode = " + tag + "tool-augmented (auto full catalog)")
		} else {
			ec.l.Info("[Execute]    mode = " + tag + "tool-augmented")
		}
	} else {
		if streaming {
			ec.l.Info("[Execute]    mode = stream")
		} else {
			ec.l.Info("[Execute]    mode = simple")
		}
	}

	memosCtx := e.memory.RecallMemories(ctx, ec.userMsg, ec.ag)

	var msgTools []model.Tool
	var msgToolSkillMap map[string]string
	if !tsMode {
		msgTools = ec.agentTools
		msgToolSkillMap = ec.toolSkillMap
	}
	messages := buildMessages(messagesBuildInput{
		Agent:          ec.ag,
		Skills:         ec.skills,
		History:        history,
		UserMsg:        ec.userMsg,
		AgentTools:     msgTools,
		ToolSkillMap:   msgToolSkillMap,
		Files:          ec.files,
		MemosContext:   memosCtx,
		ToolSearchMode: tsMode,
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

func toolsSentToLLM(tsMode bool, allDefs []openai.Tool, discovered map[string]bool) []openai.Tool {
	if tsMode {
		return buildToolSearchDefs(allDefs, discovered)
	}
	return allDefs
}

// ── 从 executor_util.go 迁入的工具函数 ──────────────────────

func extractContent(resp openai.ChatCompletionResponse) string {
	if len(resp.Choices) == 0 {
		return ""
	}
	return resp.Choices[0].Message.Content
}

func applyModelCaps(req *openai.ChatCompletionRequest, ag *model.Agent, l *log.Entry) {
	caps := model.GetModelCaps(ag.ModelName)
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
}

func (e *Executor) recordUsedSkillSteps(ctx context.Context, skills []model.Skill, toolSkillMap map[string]string, calledTools map[string]bool, tracker *StepTracker) {
	usedSkills := make(map[string]bool)
	for toolName := range calledTools {
		if skillName, ok := toolSkillMap[toolName]; ok {
			usedSkills[skillName] = true
		}
	}
	for _, sk := range skills {
		if !usedSkills[sk.Name] {
			continue
		}
		var calledToolNames []string
		for toolName, skillName := range toolSkillMap {
			if skillName == sk.Name && calledTools[toolName] {
				calledToolNames = append(calledToolNames, toolName)
			}
		}
		input := sk.Instruction
		if input == "" {
			input = "(no instruction)"
		}
		output := fmt.Sprintf("used %d tools: %s", len(calledToolNames), strings.Join(calledToolNames, ", "))
		tracker.RecordStep(ctx, model.StepSkillMatch, sk.Name, input, output, model.StepSuccess, "", 0, 0, &model.StepMetadata{
			SkillName: sk.Name, SkillTools: calledToolNames,
		})
		log.WithFields(log.Fields{"skill": sk.Name, "used_tools": calledToolNames}).Info("[Skill] skill used")
	}
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
	l.WithFields(log.Fields{"tools": toolNames, "skills": skillNames}).Info("[Execute]    resources loaded")
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
