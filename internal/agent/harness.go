package agent

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	openai "github.com/chowyu12/go-openai"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/model"
	harnesspkg "github.com/chowyu12/aiclaw/pkg/harness"
)

type harnessTurnState struct {
	Messages    []openai.ChatCompletionMessage
	ToolMap     map[string]Tool
	AllToolDefs []openai.Tool
	TSMode      bool
	Discovered  map[string]bool

	mu          sync.Mutex
	loopDet     *toolLoopDetector
	calledTools map[string]bool

	lastDiscoveredLen int
	cachedLLMDefs     []openai.Tool

	verifier harnessVerifierState
}

type agentHarness struct {
	executor  *Executor
	ec        *execContext
	call      llmCaller
	streaming bool
	sink      harnesspkg.Sink

	runID  string
	turnID string
	state  *harnessTurnState

	budget      *BudgetTracker
	compressor  *ContextCompressor
	totalTokens int
	startedAt   time.Time

	builtinWebSearchStepRecorded bool

	lifecycle *harnessLifecycleLayer
	context   *harnessContextCompilerLayer
	model     *harnessModelDriverLayer
	action    *harnessActionRuntimeLayer
	verifier  *harnessVerifierLayer
	control   *harnessControlPlaneLayer
}

func newAgentHarness(e *Executor, ec *execContext, call llmCaller, streaming bool, sink harnesspkg.Sink) *agentHarness {
	if sink == nil {
		sink = harnesspkg.NoopSink{}
	}
	h := &agentHarness{
		executor:   e,
		ec:         ec,
		call:       call,
		streaming:  streaming,
		sink:       sink,
		runID:      uuid.NewString(),
		turnID:     uuid.NewString(),
		budget:     NewBudgetTracker(ec.ag.TokenBudget),
		compressor: NewContextCompressor(ec.ag.ModelName, ec.l),
		startedAt:  time.Now(),
	}
	h.lifecycle = &harnessLifecycleLayer{h: h}
	h.context = &harnessContextCompilerLayer{h: h}
	h.model = &harnessModelDriverLayer{h: h}
	h.action = &harnessActionRuntimeLayer{h: h}
	h.verifier = &harnessVerifierLayer{h: h}
	h.control = &harnessControlPlaneLayer{h: h}
	return h
}

func (h *agentHarness) emit(evt harnesspkg.Event) {
	if evt.Version == "" {
		evt.Version = harnesspkg.ProtocolVersion
	}
	if evt.CreatedAt.IsZero() {
		evt.CreatedAt = time.Now()
	}
	if evt.RunID == "" {
		evt.RunID = h.runID
	}
	if evt.TurnID == "" {
		evt.TurnID = h.turnID
	}
	if err := h.sink.Emit(evt); err != nil {
		h.ec.l.WithError(err).WithField("event", evt.Type).Warn("[Harness] event sink failed")
	}
}

func (e *Executor) runHarness(ctx context.Context, ec *execContext, call llmCaller, streaming bool, sink harnesspkg.Sink) (*ExecuteResult, error) {
	return newAgentHarness(e, ec, call, streaming, sink).Run(ctx)
}

func (h *agentHarness) Run(ctx context.Context) (*ExecuteResult, error) {
	ctx, cancel := h.lifecycle.begin(ctx)
	defer cancel()

	st, err := h.context.bootstrap(ctx)
	if err != nil {
		h.lifecycle.fail(err)
		return nil, err
	}
	h.state = st

	var finalContent string
	completed := false
	maxIter := h.ec.ag.IterationLimit()

	for round := 1; round <= maxIter; round++ {
		h.lifecycle.beginTurn(round)

		req, err := h.context.compileRound(ctx, round)
		if err != nil {
			h.lifecycle.fail(err)
			return nil, err
		}

		result, err := h.model.callRound(ctx, round, req)
		if err != nil {
			h.control.failRunningPlan(ctx, err.Error())
			h.lifecycle.fail(err)
			return nil, err
		}

		if len(result.toolCalls) == 0 {
			content, done, retry := h.verifier.verifyFinalAnswer(ctx, round, maxIter, result.content)
			if retry {
				h.lifecycle.completeTurn(round)
				continue
			}
			finalContent = content
			completed = done
			h.lifecycle.completeTurn(round)
			break
		}

		if h.control.tokenBudgetExceeded() {
			finalContent = result.content
			if finalContent == "" {
				finalContent = fmt.Sprintf("Token budget limit reached (%d); %d tokens have been used. Start a new conversation to continue.",
					h.ec.ag.TokenBudget, h.budget.Consumed())
			}
			completed = true
			h.lifecycle.completeTurn(round)
			break
		}

		hasRealTool, toolFailed, hasPlanTool := h.action.executeRound(ctx, result)
		h.control.afterToolRound(ctx, hasRealTool, toolFailed, hasPlanTool)
		content, done, retry := h.verifier.verifyToolRound(ctx, round, maxIter)
		if retry {
			h.lifecycle.completeTurn(round)
			continue
		}
		if done {
			finalContent = content
			completed = true
			h.lifecycle.completeTurn(round)
			break
		}
		h.lifecycle.completeTurn(round)
	}

	if !completed {
		errMsg := fmt.Sprintf("Maximum iteration count reached (%d); the Agent did not produce a final answer", maxIter)
		err := errors.New(errMsg)
		h.control.maxIterationsReached(ctx, errMsg)
		h.lifecycle.fail(err)
		return nil, err
	}

	res, err := h.control.save(ctx, finalContent)
	if err != nil {
		h.lifecycle.fail(err)
		return nil, err
	}
	h.lifecycle.complete()
	return res, nil
}

type harnessLifecycleLayer struct {
	h *agentHarness
}

func (l *harnessLifecycleLayer) begin(ctx context.Context) (context.Context, context.CancelFunc) {
	l.h.emit(harnesspkg.Event{
		Type:   harnesspkg.EventRunStarted,
		Layer:  harnesspkg.LayerLifecycle,
		Status: harnesspkg.StatusRunning,
		Metadata: map[string]any{
			"agent":           l.h.ec.ag.Name,
			"model":           l.h.ec.ag.ModelName,
			"streaming":       l.h.streaming,
			"ephemeral":       l.h.ec.ephemeral,
			"provider":        l.h.ec.prov.Name,
			"conversation_id": l.h.ec.conv.UUID,
		},
	})
	if t := l.h.ec.ag.TimeoutSeconds(); t > 0 {
		return context.WithTimeout(ctx, time.Duration(t)*time.Second)
	}
	return ctx, func() {}
}

func (l *harnessLifecycleLayer) beginTurn(round int) {
	l.h.emit(harnesspkg.Event{
		Type:   harnesspkg.EventTurnStarted,
		Layer:  harnesspkg.LayerLifecycle,
		Status: harnesspkg.StatusRunning,
		Metadata: map[string]any{
			"round": round,
		},
	})
}

func (l *harnessLifecycleLayer) completeTurn(round int) {
	l.h.emit(harnesspkg.Event{
		Type:   harnesspkg.EventTurnCompleted,
		Layer:  harnesspkg.LayerLifecycle,
		Status: harnesspkg.StatusCompleted,
		Metadata: map[string]any{
			"round": round,
		},
	})
}

func (l *harnessLifecycleLayer) complete() {
	l.h.emit(harnesspkg.Event{
		Type:   harnesspkg.EventRunCompleted,
		Layer:  harnesspkg.LayerLifecycle,
		Status: harnesspkg.StatusCompleted,
		Metadata: map[string]any{
			"duration_ms": time.Since(l.h.startedAt).Milliseconds(),
			"tokens":      l.h.totalTokens,
		},
	})
}

func (l *harnessLifecycleLayer) fail(err error) {
	l.h.emit(harnesspkg.Event{
		Type:   harnesspkg.EventRunFailed,
		Layer:  harnesspkg.LayerLifecycle,
		Status: harnesspkg.StatusFailed,
		Error:  err.Error(),
		Metadata: map[string]any{
			"duration_ms": time.Since(l.h.startedAt).Milliseconds(),
			"tokens":      l.h.totalTokens,
		},
	})
}

type harnessContextCompilerLayer struct {
	h *agentHarness
}

func (c *harnessContextCompilerLayer) bootstrap(ctx context.Context) (*harnessTurnState, error) {
	ec := c.h.ec
	var history []openai.ChatCompletionMessage
	if !ec.ephemeral {
		var err error
		history, err = c.h.executor.memory.LoadHistory(ctx, ec.conv.ID, ec.ag.HistoryLimit())
		if err != nil {
			ec.l.WithError(err).Error("[LLM] load history failed")
			return nil, err
		}

		if _, err := c.h.executor.memory.SaveUserMessage(ctx, ec.conv.ID, ec.userMsg, ec.uploadedFiles); err != nil {
			ec.l.WithError(err).Error("[LLM] save user message failed")
			return nil, err
		}
	}

	toolMap, allToolDefs, tsMode, discovered := c.compileToolCatalog()
	persistentMem := loadPersistentMemory(c.h.executor.ws)
	planBlock := ""
	if ec.plan != nil {
		planBlock = ec.plan.PromptBlock(ctx)
	}

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
		PlanBlock:        planBlock,
		ToolSearchMode:   tsMode,
		WebSearchEnabled: webSearchPromptEnabled(ec.ag),
		WebSearchMode:    ec.ag.EffectiveWebSearchMode(),
		WS:               c.h.executor.ws,
	})
	logMessages(ec.l, messages)

	c.h.emit(harnesspkg.Event{
		Type:   harnesspkg.EventContextBuilt,
		Layer:  harnesspkg.LayerContext,
		Status: harnesspkg.StatusCompleted,
		Metadata: map[string]any{
			"messages":      len(messages),
			"tools":         len(allToolDefs),
			"tool_search":   tsMode,
			"history_items": len(history),
			"skills":        len(ec.skills),
			"files":         len(ec.files),
		},
	})

	return &harnessTurnState{
		Messages:    messages,
		ToolMap:     toolMap,
		AllToolDefs: allToolDefs,
		TSMode:      tsMode,
		Discovered:  discovered,
		loopDet:     newToolLoopDetector(ec.l),
		calledTools: make(map[string]bool),
	}, nil
}

func (c *harnessContextCompilerLayer) compileToolCatalog() (map[string]Tool, []openai.Tool, bool, map[string]bool) {
	ec := c.h.ec
	var toolMap map[string]Tool
	var allToolDefs []openai.Tool
	tsMode := false
	discovered := map[string]bool{}

	if !ec.hasTools() {
		if c.h.streaming {
			ec.l.Debug("[Execute]    mode = stream")
		} else {
			ec.l.Debug("[Execute]    mode = simple")
		}
		return nil, nil, false, discovered
	}

	lcTools := c.h.executor.registry.BuildTrackedTools(ec.agentTools, ec.tracker, ec.toolSkillMap)
	lcTools = append(lcTools, ec.mcpTools...)
	lcTools = append(lcTools, ec.skillTools...)
	toolMap = make(map[string]Tool, len(lcTools))
	for _, t := range lcTools {
		toolMap[t.Name()] = t
	}
	allToolDefs = buildLLMToolDefs(ec.agentTools, ec.mcpTools, ec.skillTools)

	tsMode = UseLazyToolSearch(ec.ag.ToolSearchEnabled, len(allToolDefs))
	tag := ""
	if c.h.streaming {
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
	return toolMap, allToolDefs, tsMode, discovered
}

func (c *harnessContextCompilerLayer) compileRound(ctx context.Context, round int) (openai.ChatCompletionRequest, error) {
	st := c.h.state
	ec := c.h.ec
	if c.h.compressor.NeedCompress(st.Messages) {
		compressionModel := cmp.Or(ec.ag.FastModelName, ec.ag.ModelName)
		compressed, compErr := c.h.compressor.Compress(ctx, st.Messages, ec.llmProv, compressionModel)
		if compErr != nil {
			ec.l.WithError(compErr).Warn("[Compress] context compression failed, continuing with full context")
		} else {
			st.Messages = compressed
			c.h.emit(harnesspkg.Event{
				Type:   harnesspkg.EventControlUpdate,
				Layer:  harnesspkg.LayerContext,
				Name:   "context_compression",
				Status: harnesspkg.StatusCompleted,
				Metadata: map[string]any{
					"round":    round,
					"messages": len(st.Messages),
				},
			})
		}
	}

	refreshPlanInSystemMessage(st.Messages, ec.plan)
	msgs := sanitizeMessages(st.Messages)
	tools := filterURLGatedTools(toolsSentToLLM(st), userMessagesHaveURL(msgs))
	req := openai.ChatCompletionRequest{
		Model:    ec.ag.ModelName,
		Messages: msgs,
		Tools:    tools,
	}
	applyModelCaps(&req, ec.ag, ec.prov.Type, ec.l)
	if webSearchEffective(ec.ag) && !c.h.builtinWebSearchStepRecorded {
		recordBuiltInWebSearchStep(ctx, ec, req.ExtraBody)
		c.h.builtinWebSearchStepRecorded = true
	}
	c.h.emit(harnesspkg.Event{
		Type:   harnesspkg.EventContextBuilt,
		Layer:  harnesspkg.LayerContext,
		Status: harnesspkg.StatusCompleted,
		Metadata: map[string]any{
			"round":    round,
			"messages": len(msgs),
			"tools":    len(tools),
		},
	})
	return req, nil
}

type harnessModelDriverLayer struct {
	h *agentHarness
}

func (m *harnessModelDriverLayer) callRound(ctx context.Context, round int, req openai.ChatCompletionRequest) (llmRoundResult, error) {
	ec := m.h.ec
	if action := m.h.executor.hooks.Fire(ctx, HookPreLLMCall, &HookPayload{Model: ec.ag.ModelName, Round: round}); action == HookAbort {
		ec.l.WithField("round", round).Warn("[Hook] agent execution aborted by pre_llm_call hook")
		return llmRoundResult{}, errors.New("agent execution aborted by hook")
	}

	m.h.emit(harnesspkg.Event{
		Type:   harnesspkg.EventModelStarted,
		Layer:  harnesspkg.LayerModel,
		Name:   ec.ag.ModelName,
		Status: harnesspkg.StatusRunning,
		Metadata: map[string]any{
			"round": round,
		},
	})

	ec.l.WithFields(log.Fields{"round": round, "model": ec.ag.ModelName}).Info("[LLM] >> call")
	llmStep := ec.tracker.BeginStep(ctx, model.StepLLMCall, ec.ag.ModelName, ec.userMsg, ec.stepMeta())
	start := time.Now()
	result, callErr := m.h.call(ctx, req)
	dur := time.Since(start)

	if callErr != nil {
		ec.l.WithFields(log.Fields{"round": round, "duration": dur}).WithError(callErr).Error("[LLM] << failed")
		ec.tracker.FinalizeStep(ctx, llmStep, "", model.StepError, callErr.Error(), dur, 0, ec.stepMeta())
		err := fmt.Errorf("generate content: %w", callErr)
		m.h.emit(harnesspkg.Event{
			Type:   harnesspkg.EventModelFailed,
			Layer:  harnesspkg.LayerModel,
			Name:   ec.ag.ModelName,
			Status: harnesspkg.StatusFailed,
			Error:  err.Error(),
			Metadata: map[string]any{
				"round":       round,
				"duration_ms": dur.Milliseconds(),
			},
		})
		return llmRoundResult{}, err
	}

	ec.tracker.FinalizeStep(ctx, llmStep, result.content, model.StepSuccess, "", dur, result.tokens, ec.stepMeta())
	m.h.totalTokens += result.tokens
	m.h.budget.Add(result.tokens)
	m.h.compressor.UpdatePromptTokens(result.promptTokens)
	m.h.executor.hooks.Fire(ctx, HookPostLLMCall, &HookPayload{Model: ec.ag.ModelName, Round: round, Tokens: result.tokens})

	m.h.emit(harnesspkg.Event{
		Type:   harnesspkg.EventModelCompleted,
		Layer:  harnesspkg.LayerModel,
		Name:   ec.ag.ModelName,
		Status: harnesspkg.StatusCompleted,
		Output: map[string]any{
			"tool_calls":  len(result.toolCalls),
			"content_len": len(result.content),
		},
		Metadata: map[string]any{
			"round":         round,
			"duration_ms":   dur.Milliseconds(),
			"tokens":        result.tokens,
			"prompt_tokens": result.promptTokens,
		},
	})

	if len(result.toolCalls) == 0 {
		ec.l.WithFields(log.Fields{
			"round": round, "duration": dur, "tokens": result.tokens,
			"len": len(result.content), "preview": truncateLog(result.content, 200),
		}).Info("[LLM] << final answer")
	} else {
		names := make([]string, 0, len(result.toolCalls))
		for _, tc := range result.toolCalls {
			names = append(names, tc.Function.Name)
		}
		ec.l.WithFields(log.Fields{"round": round, "duration": dur, "tokens": result.tokens, "tool_calls": names}).Info("[LLM] << tool calls")
	}
	return result, nil
}

type harnessActionRuntimeLayer struct {
	h *agentHarness
}

func (a *harnessActionRuntimeLayer) executeRound(ctx context.Context, result llmRoundResult) (bool, bool, bool) {
	asst := openai.ChatCompletionMessage{
		Role:      openai.ChatMessageRoleAssistant,
		Content:   result.content,
		ToolCalls: result.toolCalls,
	}
	for _, tc := range result.toolCalls {
		a.h.emit(harnesspkg.Event{
			Type:   harnesspkg.EventActionStarted,
			Layer:  harnesspkg.LayerAction,
			ItemID: tc.ID,
			Name:   tc.Function.Name,
			Status: harnesspkg.StatusRunning,
			Input:  tc.Function.Arguments,
		})
	}
	hasRealTool, toolFailed, hasPlanTool := a.h.executor.appendAssistantToolRound(ctx, a.h.ec, a.h.state, asst)
	for _, tc := range result.toolCalls {
		status := harnesspkg.StatusCompleted
		if toolFailed {
			status = harnesspkg.StatusFailed
		}
		a.h.emit(harnesspkg.Event{
			Type:   harnesspkg.EventActionFinished,
			Layer:  harnesspkg.LayerAction,
			ItemID: tc.ID,
			Name:   tc.Function.Name,
			Status: status,
		})
	}
	return hasRealTool, toolFailed, hasPlanTool
}

type harnessControlPlaneLayer struct {
	h *agentHarness
}

func (c *harnessControlPlaneLayer) tokenBudgetExceeded() bool {
	if !c.h.budget.Exceeded() {
		return false
	}
	c.h.ec.l.WithFields(log.Fields{
		"budget_limit": c.h.ec.ag.TokenBudget,
		"consumed":     c.h.budget.Consumed(),
	}).Warn("[Budget] token budget exceeded, stopping after this round")
	c.h.emit(harnesspkg.Event{
		Type:   harnesspkg.EventControlUpdate,
		Layer:  harnesspkg.LayerControl,
		Name:   "token_budget",
		Status: harnesspkg.StatusCompleted,
		Metadata: map[string]any{
			"budget_limit": c.h.ec.ag.TokenBudget,
			"consumed":     c.h.budget.Consumed(),
		},
	})
	return true
}

func (c *harnessControlPlaneLayer) afterToolRound(ctx context.Context, hasRealTool, toolFailed, hasPlanTool bool) {
	if c.h.ec.plan != nil && hasRealTool && !toolFailed && !hasPlanTool {
		c.h.ec.plan.CompleteRunning(ctx)
		c.h.emit(harnesspkg.Event{
			Type:   harnesspkg.EventControlUpdate,
			Layer:  harnesspkg.LayerControl,
			Name:   "plan_advance",
			Status: harnesspkg.StatusCompleted,
		})
	}
}

func (c *harnessControlPlaneLayer) failRunningPlan(ctx context.Context, reason string) {
	if c.h.ec.plan != nil {
		c.h.ec.plan.FailRunning(ctx, reason)
	}
	c.h.emit(harnesspkg.Event{
		Type:   harnesspkg.EventControlUpdate,
		Layer:  harnesspkg.LayerControl,
		Name:   "plan_failed",
		Status: harnesspkg.StatusFailed,
		Error:  reason,
	})
}

func (c *harnessControlPlaneLayer) maxIterationsReached(ctx context.Context, errMsg string) {
	ec := c.h.ec
	ec.l.WithField("max_iterations", ec.ag.IterationLimit()).Error("[Execute] max iterations reached")
	ec.tracker.RecordStep(ctx, model.StepLLMCall, ec.ag.ModelName, ec.userMsg, "", model.StepError, errMsg, time.Since(c.h.startedAt), c.h.totalTokens, ec.stepMeta())
	c.failRunningPlan(ctx, errMsg)
}

func (c *harnessControlPlaneLayer) save(ctx context.Context, finalContent string) (*ExecuteResult, error) {
	res, err := c.h.executor.saveResult(ctx, c.h.ec, c.h.state, finalContent, c.h.totalTokens, time.Since(c.h.startedAt))
	if err != nil {
		return nil, err
	}
	c.h.emit(harnesspkg.Event{
		Type:   harnesspkg.EventPersisted,
		Layer:  harnesspkg.LayerPersistence,
		Status: harnesspkg.StatusCompleted,
		Metadata: map[string]any{
			"message_id":      res.MessageID,
			"conversation_id": res.ConversationID,
			"files":           len(res.ToolFiles),
			"tokens":          res.TokensUsed,
		},
	})
	return res, nil
}
