package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/provider"
	"github.com/chowyu12/aiclaw/internal/skill"
	"github.com/chowyu12/aiclaw/internal/store"
	"github.com/chowyu12/aiclaw/internal/tool/mcp"
	"github.com/chowyu12/aiclaw/internal/workspace"
)

// ============================================================
//  类型定义
// ============================================================

type ExecuteResult struct {
	ConversationID string
	Content        string
	TokensUsed     int
	Steps          []model.ExecutionStep
}

type ProviderFactory func(p *model.Provider, modelName string) (provider.LLMProvider, error)

type ExecutorOption func(*Executor)

func WithProviderFactory(f ProviderFactory) ExecutorOption {
	return func(e *Executor) { e.providerFactory = f }
}

type Executor struct {
	store           store.Store
	registry        *ToolRegistry
	memory          *MemoryManager
	providerFactory ProviderFactory
}

func NewExecutor(s store.Store, registry *ToolRegistry, opts ...ExecutorOption) *Executor {
	e := &Executor{
		store:           s,
		registry:        registry,
		memory:          NewMemoryManager(s, s),
		providerFactory: provider.NewFromProvider,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// execContext 聚合单次执行所需的全部上下文。
type execContext struct {
	ag      *model.Agent
	prov    *model.Provider
	llmProv provider.LLMProvider
	conv    *model.Conversation
	skills  []model.Skill
	tracker *StepTracker
	files   []*model.File
	userMsg string
	l       *log.Entry

	agentTools   []model.Tool
	mcpTools     []Tool
	skillTools   []Tool
	mcpManager   *mcp.Manager
	toolSkillMap map[string]string
}

func (ec *execContext) hasTools() bool {
	return len(ec.agentTools) > 0 || len(ec.mcpTools) > 0 || len(ec.skillTools) > 0
}

func (ec *execContext) closeMCP() {
	if ec.mcpManager != nil {
		ec.mcpManager.Close()
	}
}

func (ec *execContext) stepMeta() *model.StepMetadata {
	return &model.StepMetadata{
		Provider:    ec.prov.Name,
		Model:       ec.ag.ModelName,
		Temperature: ec.ag.Temperature,
	}
}

// ============================================================
//  对外入口
// ============================================================

func (e *Executor) Execute(ctx context.Context, req model.ChatRequest) (*ExecuteResult, error) {
	ec, err := e.prepare(ctx, req)
	if err != nil {
		return nil, err
	}
	defer ec.closeMCP()

	ctx = workspace.WithAgentUUID(ctx, ec.ag.UUID)

	ec.l.WithField("user", req.UserID).Info("[Execute] >> start")
	if body, err := json.Marshal(req); err == nil {
		ec.l.WithField("body", string(body)).Debug("[Execute]    request body")
	}

	return e.execute(ctx, ec)
}

func (e *Executor) ExecuteStream(ctx context.Context, req model.ChatRequest, chunkHandler func(chunk model.StreamChunk) error) error {
	ec, err := e.prepare(ctx, req)
	if err != nil {
		return err
	}
	defer ec.closeMCP()

	ctx = workspace.WithAgentUUID(ctx, ec.ag.UUID)

	ec.l.WithField("user", req.UserID).Info("[Execute] >> start (stream)")

	ec.tracker.SetOnStep(func(step model.ExecutionStep) {
		_ = chunkHandler(model.StreamChunk{
			ConversationID: ec.conv.UUID,
			Step:           &step,
		})
	})

	return e.stream(ctx, ec, chunkHandler)
}

// ============================================================
//  准备阶段：构建 execContext
// ============================================================

func (e *Executor) prepare(ctx context.Context, req model.ChatRequest) (*execContext, error) {
	ag, err := TryLoadAgent(ctx, e.store)
	if err != nil {
		log.WithError(err).Error("[Execute] load agent config failed")
		return nil, fmt.Errorf("agent not found: %w", err)
	}
	if req.AgentID != "" && req.AgentID != ag.UUID {
		log.WithFields(log.Fields{"want": ag.UUID, "got": req.AgentID}).Error("[Execute] agent uuid mismatch")
		return nil, fmt.Errorf("agent not found")
	}

	prov, err := e.store.GetProvider(ctx, ag.ProviderID)
	if err != nil {
		log.WithFields(log.Fields{"agent": ag.Name, "provider_id": ag.ProviderID}).WithError(err).Error("[Execute] provider not found")
		return nil, fmt.Errorf("provider not found: %w", err)
	}

	l := log.WithFields(log.Fields{"agent": ag.Name, "provider": prov.Name, "model": ag.ModelName})

	llmProv, err := e.providerFactory(prov, ag.ModelName)
	if err != nil {
		l.WithError(err).Error("[Execute] create llm provider failed")
		return nil, fmt.Errorf("create llm provider: %w", err)
	}

	agentTools, toolSkillMap, err := e.collectTools(ctx, ag)
	if err != nil {
		l.WithError(err).Error("[Execute] collect tools failed")
		return nil, err
	}

	skills, err := loadWorkspaceSkills()
	if err != nil {
		l.WithError(err).Warn("[Execute] load workspace skills failed, continuing without skills")
		skills = nil
	}

	isNewConv := req.ConversationID == ""
	conv, err := e.memory.GetOrCreateConversation(ctx, req.ConversationID, req.UserID)
	if err != nil {
		l.WithError(err).Error("[Execute] get/create conversation failed")
		return nil, fmt.Errorf("get conversation: %w", err)
	}
	if isNewConv {
		e.memory.AutoSetTitle(ctx, conv.ID, req.Message)
	}

	tracker := NewStepTracker(e.store, conv.ID)

	mcpServers, err := e.store.ListMCPServers(ctx)
	if err != nil {
		l.WithError(err).Warn("[Execute] list MCP servers failed, continuing without MCP")
		mcpServers = nil
	}
	mcpManager, mcpTools := e.connectMCPServers(ctx, mcpServers, tracker, toolSkillMap)
	skillTools := e.buildSkillManifestTools(skills, tracker, toolSkillMap)

	logResourceSummary(l, agentTools, skills)

	files := e.loadRequestFiles(ctx, req.Files, conv.ID)

	return &execContext{
		ag:           ag,
		prov:         prov,
		llmProv:      llmProv,
		conv:         conv,
		skills:       skills,
		tracker:      tracker,
		files:        files,
		userMsg:      req.Message,
		l:            l.WithField("conv", conv.UUID),
		agentTools:   agentTools,
		mcpTools:     mcpTools,
		skillTools:   skillTools,
		mcpManager:   mcpManager,
		toolSkillMap: toolSkillMap,
	}, nil
}

func loadWorkspaceSkills() ([]model.Skill, error) {
	dir := workspace.Skills()
	if dir == "" {
		return nil, nil
	}
	infos, err := skill.ScanAll(dir)
	if err != nil {
		return nil, err
	}
	out := make([]model.Skill, 0, len(infos))
	for i := range infos {
		s := skill.InfoToSkill(infos[i], model.SkillSourceLocal, "")
		s.Enabled = true
		out = append(out, *s)
	}
	return out, nil
}

func (e *Executor) connectMCPServers(ctx context.Context, servers []model.MCPServer, tracker *StepTracker, toolSkillMap map[string]string) (*mcp.Manager, []Tool) {
	if len(servers) == 0 {
		return nil, nil
	}

	mgr := mcp.NewManager()
	if err := mgr.Connect(ctx, servers); err != nil {
		log.WithError(err).Warn("[MCP] connect failed")
		return nil, nil
	}
	if !mgr.HasTools() {
		mgr.Close()
		return nil, nil
	}

	infos := mgr.Tools()
	mcpTools := make([]Tool, 0, len(infos))
	for _, info := range infos {
		info := info
		toolSkillMap[info.Name] = "mcp:" + info.ServerName
		base := &dynamicTool{
			toolName: info.Name,
			toolDesc: info.Description,
			params:   info.Parameters,
			handler: func(ctx context.Context, input string) (string, error) {
				return mgr.CallTool(ctx, info.Name, input)
			},
		}
		mcpTools = append(mcpTools, &trackedTool{
			baseTool:  base,
			name:      info.Name,
			skillName: "mcp:" + info.ServerName,
			tracker:   tracker,
		})
	}
	log.WithField("count", len(mcpTools)).Info("[MCP] tools loaded")
	return mgr, mcpTools
}

func (e *Executor) buildSkillManifestTools(skills []model.Skill, tracker *StepTracker, toolSkillMap map[string]string) []Tool {
	var result []Tool
	for _, sk := range skills {
		if !sk.Enabled || len(sk.ToolDefs) == 0 {
			continue
		}
		var toolDefs []model.SkillManifestTool
		if err := json.Unmarshal(sk.ToolDefs, &toolDefs); err != nil {
			log.WithError(err).WithField("skill", sk.Name).Warn("[Skill] parse tool_defs failed")
			continue
		}
		for _, td := range toolDefs {
			td := td
			toolSkillMap[td.Name] = sk.Name
			var handler func(ctx context.Context, input string) (string, error)

			if sk.MainFile != "" {
				skillDir := workspace.SkillDir(sk.DirName)
				if skillDir != "" {
					mainFile := sk.MainFile
					handler = func(ctx context.Context, input string) (string, error) {
						return skill.RunTool(ctx, skillDir, mainFile, td.Name, input, nil, 0)
					}
				}
			}
			if handler == nil {
				instruction := sk.Instruction
				handler = func(_ context.Context, input string) (string, error) {
					return fmt.Sprintf("[skill:%s] 请根据技能指令处理。输入: %s\n指令: %s", sk.Name, input, instruction), nil
				}
			}

			base := &dynamicTool{
				toolName: td.Name,
				toolDesc: td.Description,
				params:   td.Parameters,
				handler:  handler,
			}
			result = append(result, &trackedTool{
				baseTool:  base,
				name:      td.Name,
				skillName: sk.Name,
				tracker:   tracker,
			})
		}
		log.WithFields(log.Fields{"skill": sk.Name, "manifest_tools": len(toolDefs)}).Debug("[Execute]    skill manifest tools loaded")
	}
	return result
}

func (e *Executor) collectTools(ctx context.Context, ag *model.Agent) ([]model.Tool, map[string]string, error) {
	var agentTools []model.Tool
	seen := make(map[int64]bool)

	if ag.ToolSearchEnabled {
		items, _, err := e.store.ListTools(ctx, model.ListQuery{Page: 1, PageSize: 10000})
		if err != nil {
			return nil, nil, fmt.Errorf("list all tools: %w", err)
		}
		for _, t := range items {
			if t.Enabled {
				agentTools = append(agentTools, *t)
				seen[t.ID] = true
			}
		}
		log.WithField("count", len(agentTools)).Info("[Execute]    tool_search: loaded all enabled tools")
	} else {
		for _, tid := range ag.ToolIDs {
			t, err := e.store.GetTool(ctx, tid)
			if err != nil || t == nil || !t.Enabled {
				continue
			}
			if !seen[t.ID] {
				agentTools = append(agentTools, *t)
				seen[t.ID] = true
			}
		}
	}

	toolSkillMap := make(map[string]string)
	return agentTools, toolSkillMap, nil
}

// ============================================================
//  核心执行（非流式，统一有/无工具）
// ============================================================

func (e *Executor) execute(ctx context.Context, ec *execContext) (*ExecuteResult, error) {
	if t := ec.ag.TimeoutSeconds(); t > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(t)*time.Second)
		defer cancel()
	}

	st, err := e.bootstrapAgentTurn(ctx, ec, false)
	if err != nil {
		return nil, err
	}

	var finalContent string
	var totalTokens int
	totalStart := time.Now()

	maxIter := ec.ag.IterationLimit()
	completed := false
	loopDet := newToolLoopDetector(ec.l)
	calledTools := make(map[string]bool)

	for i := range maxIter {
		req := openai.ChatCompletionRequest{
			Model:    ec.ag.ModelName,
			Messages: st.Messages,
			Tools:    toolsSentToLLM(st.TSMode, st.AllToolDefs, st.Discovered),
		}
		applyModelCaps(&req, ec.ag, ec.l)
		ec.l.WithFields(log.Fields{"round": i + 1, "model": ec.ag.ModelName}).Info("[LLM] >> call")
		iterStart := time.Now()
		resp, err := ec.llmProv.CreateChatCompletion(ctx, req)
		iterDur := time.Since(iterStart)

		if err != nil {
			ec.l.WithFields(log.Fields{"round": i + 1, "duration": iterDur}).WithError(err).Error("[LLM] << failed")
			ec.tracker.RecordStep(ctx, model.StepLLMCall, ec.ag.ModelName, ec.userMsg, "", model.StepError, err.Error(), iterDur, 0, ec.stepMeta())
			return nil, fmt.Errorf("generate content: %w", err)
		}

		totalTokens += resp.Usage.TotalTokens

		if len(resp.Choices) == 0 {
			ec.l.Warn("[LLM] << empty response")
			completed = true
			break
		}

		choice := resp.Choices[0]

		if len(choice.Message.ToolCalls) == 0 {
			finalContent = choice.Message.Content
			completed = true
			ec.l.WithFields(log.Fields{
				"round":    i + 1,
				"duration": iterDur,
				"tokens":   resp.Usage.TotalTokens,
				"len":      len(finalContent),
				"preview":  truncateLog(finalContent, 200),
			}).Info("[LLM] << final answer")
			break
		}

		tcNames := make([]string, 0, len(choice.Message.ToolCalls))
		for _, tc := range choice.Message.ToolCalls {
			tcNames = append(tcNames, tc.Function.Name)
		}
		ec.l.WithFields(log.Fields{"round": i + 1, "duration": iterDur, "tool_calls": tcNames}).Info("[LLM] << tool calls requested")

		st.Messages = e.appendAssistantToolRound(ctx, ec, st.Messages, choice.Message, st.ToolMap, st.TSMode, st.AllToolDefs, st.Discovered, loopDet, calledTools)
	}

	if !completed {
		ec.l.WithField("max_iterations", maxIter).Error("[Execute] max iterations reached without final answer")
		errMsg := fmt.Sprintf("已达到最大迭代次数 %d，Agent 未能给出最终回答", maxIter)
		ec.tracker.RecordStep(ctx, model.StepLLMCall, ec.ag.ModelName, ec.userMsg, "", model.StepError, errMsg, time.Since(totalStart), totalTokens, ec.stepMeta())
		return nil, errors.New(errMsg)
	}

	if ec.hasTools() {
		e.recordUsedSkillSteps(ctx, ec.skills, ec.toolSkillMap, calledTools, ec.tracker)
	}

	return e.saveResult(ctx, ec, finalContent, totalTokens, time.Since(totalStart))
}

// ============================================================
//  流式执行（统一有/无工具，均走真 SSE 流式）
// ============================================================

func (e *Executor) stream(ctx context.Context, ec *execContext, chunkHandler func(chunk model.StreamChunk) error) error {
	if t := ec.ag.TimeoutSeconds(); t > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(t)*time.Second)
		defer cancel()
	}

	st, err := e.bootstrapAgentTurn(ctx, ec, true)
	if err != nil {
		return err
	}

	var totalTokens int
	var finalContent string
	totalStart := time.Now()
	maxIter := ec.ag.IterationLimit()
	completed := false
	loopDet := newToolLoopDetector(ec.l)
	calledTools := make(map[string]bool)

	for i := range maxIter {
		apiReq := openai.ChatCompletionRequest{
			Model:    ec.ag.ModelName,
			Messages: st.Messages,
			Tools:    toolsSentToLLM(st.TSMode, st.AllToolDefs, st.Discovered),
			Stream:   true,
			StreamOptions: &openai.StreamOptions{
				IncludeUsage: true,
			},
		}
		applyModelCaps(&apiReq, ec.ag, ec.l)

		ec.l.WithFields(log.Fields{"round": i + 1, "model": ec.ag.ModelName}).Info("[LLM] >> call (stream)")
		iterStart := time.Now()

		s, err := ec.llmProv.CreateChatCompletionStream(ctx, apiReq)
		if err != nil {
			iterDur := time.Since(iterStart)
			ec.l.WithFields(log.Fields{"round": i + 1, "duration": iterDur}).WithError(err).Error("[LLM] << stream create failed")
			ec.tracker.RecordStep(ctx, model.StepLLMCall, ec.ag.ModelName, ec.userMsg, "", model.StepError, err.Error(), iterDur, 0, ec.stepMeta())
			return fmt.Errorf("stream content: %w", err)
		}

		var iterContent strings.Builder
		var toolCalls []openai.ToolCall
		var finishReason openai.FinishReason
		var roundTokens int

		for {
			response, recvErr := s.Recv()
			if errors.Is(recvErr, io.EOF) {
				break
			}
			if recvErr != nil {
				s.Close()
				iterDur := time.Since(iterStart)
				ec.l.WithFields(log.Fields{"round": i + 1, "duration": iterDur}).WithError(recvErr).Error("[LLM] << stream recv failed")
				ec.tracker.RecordStep(ctx, model.StepLLMCall, ec.ag.ModelName, ec.userMsg, "", model.StepError, recvErr.Error(), iterDur, 0, ec.stepMeta())
				return fmt.Errorf("stream content: %w", recvErr)
			}

			if response.Usage != nil {
				roundTokens = response.Usage.TotalTokens
			}
			if len(response.Choices) == 0 {
				continue
			}

			choice := response.Choices[0]
			if choice.FinishReason != "" {
				finishReason = choice.FinishReason
			}

			if choice.Delta.Content != "" {
				iterContent.WriteString(choice.Delta.Content)
				if err := chunkHandler(model.StreamChunk{
					ConversationID: ec.conv.UUID,
					Delta:          choice.Delta.Content,
				}); err != nil {
					s.Close()
					return err
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
		s.Close()

		totalTokens += roundTokens
		iterDur := time.Since(iterStart)
		content := iterContent.String()

		if finishReason != openai.FinishReasonToolCalls || len(toolCalls) == 0 {
			finalContent = content
			completed = true
			ec.l.WithFields(log.Fields{
				"round":    i + 1,
				"duration": iterDur,
				"tokens":   roundTokens,
				"len":      len(finalContent),
				"preview":  truncateLog(finalContent, 200),
			}).Info("[LLM] << final answer (stream)")
			break
		}

		tcNames := make([]string, 0, len(toolCalls))
		for _, tc := range toolCalls {
			tcNames = append(tcNames, tc.Function.Name)
		}
		ec.l.WithFields(log.Fields{"round": i + 1, "duration": iterDur, "tokens": roundTokens, "tool_calls": tcNames}).Info("[LLM] << tool calls requested (stream)")

		asst := openai.ChatCompletionMessage{
			Role:      openai.ChatMessageRoleAssistant,
			Content:   content,
			ToolCalls: toolCalls,
		}
		st.Messages = e.appendAssistantToolRound(ctx, ec, st.Messages, asst, st.ToolMap, st.TSMode, st.AllToolDefs, st.Discovered, loopDet, calledTools)
	}

	if !completed {
		ec.l.WithField("max_iterations", maxIter).Error("[Execute] max iterations reached without final answer (stream)")
		errMsg := fmt.Sprintf("已达到最大迭代次数 %d，Agent 未能给出最终回答", maxIter)
		ec.tracker.RecordStep(ctx, model.StepLLMCall, ec.ag.ModelName, ec.userMsg, "", model.StepError, errMsg, time.Since(totalStart), totalTokens, ec.stepMeta())
		return errors.New(errMsg)
	}

	if ec.hasTools() {
		e.recordUsedSkillSteps(ctx, ec.skills, ec.toolSkillMap, calledTools, ec.tracker)
	}

	if _, err := e.saveResult(ctx, ec, finalContent, totalTokens, time.Since(totalStart)); err != nil {
		return err
	}

	return chunkHandler(model.StreamChunk{
		ConversationID: ec.conv.UUID,
		Done:           true,
	})
}

// ============================================================
//  Tool Search 内联处理
// ============================================================

func (e *Executor) handleToolSearch(ctx context.Context, ec *execContext, tc openai.ToolCall, allDefs []openai.Tool, discovered map[string]bool) openai.ChatCompletionMessage {
	var args struct {
		Query string `json:"query"`
	}
	_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)

	ec.l.WithField("query", args.Query).Info("[ToolSearch] >> search")
	start := time.Now()
	results := searchTools(args.Query, allDefs)
	dur := time.Since(start)

	newCount := 0
	for _, r := range results {
		if !discovered[r.Name] {
			discovered[r.Name] = true
			newCount++
		}
	}

	resultJSON := formatToolSearchResults(results, len(allDefs), newCount, len(discovered))
	ec.l.WithFields(log.Fields{
		"query":            args.Query,
		"matches":          len(results),
		"newly_discovered": newCount,
		"total_discovered": len(discovered),
		"duration":         dur,
	}).Info("[ToolSearch] << done")

	ec.tracker.RecordStep(ctx, model.StepToolCall, toolSearchName, tc.Function.Arguments, resultJSON, model.StepSuccess, "", dur, 0, &model.StepMetadata{
		ToolName: toolSearchName,
	})

	return openai.ChatCompletionMessage{
		Role:       openai.ChatMessageRoleTool,
		Content:    resultJSON,
		ToolCallID: tc.ID,
		Name:       toolSearchName,
	}
}

// ============================================================
//  结果持久化
// ============================================================

func (e *Executor) saveResult(ctx context.Context, ec *execContext, content string, tokensUsed int, duration time.Duration) (*ExecuteResult, error) {
	msgID, err := e.memory.SaveAssistantMessage(ctx, ec.conv.ID, content, tokensUsed)
	if err != nil {
		ec.l.WithError(err).Error("[Execute] save assistant message failed")
		return nil, err
	}

	ec.tracker.SetMessageID(msgID)
	ec.tracker.RecordStep(ctx, model.StepLLMCall, ec.ag.ModelName, ec.userMsg, content, model.StepSuccess, "", duration, tokensUsed, ec.stepMeta())

	ec.l.WithFields(log.Fields{"msg_id": msgID, "duration": duration, "tokens": tokensUsed}).Info("[Execute] << done")

	e.memory.StoreMemories(ec.userMsg, content, ec.ag)

	return &ExecuteResult{
		ConversationID: ec.conv.UUID,
		Content:        content,
		TokensUsed:     tokensUsed,
		Steps:          ec.tracker.Steps(),
	}, nil
}
