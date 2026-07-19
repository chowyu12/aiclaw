package agent

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	openai "github.com/chowyu12/go-openai"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"

	memorypkg "github.com/chowyu12/aiclaw/internal/memory"
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

	streamMidwayRetries int
	fallbackActivated   bool
	activeModel         string
}

type agentHarness struct {
	executor  *Executor
	ec        *execContext
	call      llmCaller
	streaming bool
	sink      harnesspkg.Sink
	onDelta   func(string) error

	emitMu     sync.Mutex
	sinkErr    error
	sinkCancel context.CancelCauseFunc

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

func newAgentHarness(e *Executor, ec *execContext, call llmCaller, streaming bool, sink harnesspkg.Sink, onDelta func(string) error) *agentHarness {
	if sink == nil {
		sink = harnesspkg.NoopSink{}
	}
	runID := uuid.NewString()
	if ec != nil && ec.run != nil && ec.run.UUID != "" {
		runID = ec.run.UUID
	}
	h := &agentHarness{
		executor:   e,
		ec:         ec,
		call:       call,
		streaming:  streaming,
		sink:       sink,
		onDelta:    onDelta,
		runID:      runID,
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

func (h *agentHarness) emit(evt harnesspkg.Event) error {
	h.emitMu.Lock()
	defer h.emitMu.Unlock()
	if h.sinkErr != nil {
		return h.sinkErr
	}
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
		h.sinkErr = err
		h.ec.l.WithError(err).WithField("event", evt.Type).Warn("[Harness] event sink failed")
		if h.sinkCancel != nil {
			h.sinkCancel(err)
		}
		return err
	}
	return nil
}

func (h *agentHarness) eventError() error {
	h.emitMu.Lock()
	defer h.emitMu.Unlock()
	return h.sinkErr
}

func (e *Executor) runHarness(ctx context.Context, ec *execContext, call llmCaller, streaming bool, sink harnesspkg.Sink, onDelta func(string) error) (*ExecuteResult, error) {
	return newAgentHarness(e, ec, call, streaming, sink, onDelta).Run(ctx)
}

func (h *agentHarness) Run(ctx context.Context) (*ExecuteResult, error) {
	baseCtx, cancelSink := context.WithCancelCause(ctx)
	h.sinkCancel = cancelSink
	defer cancelSink(nil)
	ctx, cancelTimeout := h.lifecycle.begin(baseCtx)
	defer cancelTimeout()
	if err := h.eventError(); err != nil {
		return nil, err
	}

	st, err := h.context.bootstrap(ctx)
	if err != nil {
		h.lifecycle.fail(err)
		return nil, err
	}
	h.state = st
	if err := h.bootstrapHarnessPlan(ctx); err != nil {
		h.lifecycle.fail(err)
		return nil, err
	}
	if err := h.eventError(); err != nil {
		h.lifecycle.fail(err)
		return nil, err
	}

	runner := &agentTurnRunner{
		h:       h,
		maxIter: h.ec.ag.IterationLimit(),
	}
	return runner.run(ctx)
}

func (h *agentHarness) bootstrapHarnessPlan(ctx context.Context) error {
	if h.ec == nil || h.ec.plan == nil || h.state == nil {
		return nil
	}
	runtime := newHarnessRuntime(ctx, h.ec, h.state)
	if !runtime.Contract.RequirePlan {
		return nil
	}
	output, created, err := h.ec.plan.BootstrapHarnessPlan(ctx, runtime.Contract, "")
	if err != nil {
		return err
	}
	if !created {
		return nil
	}
	meta := h.ec.stepMeta()
	meta.Harness = &model.StepHarnessMeta{
		Stage:    "plan_bootstrap",
		Allowed:  true,
		Evidence: harnessEvidenceMeta(runtime.Evidence),
	}
	h.ec.tracker.RecordStep(ctx, model.StepHarness, "bootstrap_plan", runtime.Contract.Objective, output, model.StepSuccess, "", 0, 0, meta)
	h.emit(harnesspkg.Event{
		Type:   harnesspkg.EventControlUpdate,
		Layer:  harnesspkg.LayerControl,
		Name:   "plan_bootstrap",
		Status: harnesspkg.StatusCompleted,
		Metadata: map[string]any{
			"items": len(harnesspkg.InitialPlanTemplate(runtime.Contract, "")),
		},
	})
	return nil
}

type agentTurnRunner struct {
	h       *agentHarness
	maxIter int

	finalContent string
	completed    bool
}

type harnessRoundControl int

const (
	harnessRoundNext harnessRoundControl = iota
	harnessRoundBreak
)

func (r *agentTurnRunner) run(ctx context.Context) (*ExecuteResult, error) {
	for round := 1; round <= r.maxIter; round++ {
		if err := r.h.eventError(); err != nil {
			r.h.lifecycle.fail(err)
			return nil, err
		}
		r.h.lifecycle.beginTurn(round)
		if err := r.h.eventError(); err != nil {
			r.h.lifecycle.fail(err)
			return nil, err
		}

		ctrl, err := r.runRound(ctx, round)
		if err != nil {
			r.h.lifecycle.fail(err)
			return nil, err
		}
		r.h.lifecycle.completeTurn(round)
		if err := r.h.eventError(); err != nil {
			r.h.lifecycle.fail(err)
			return nil, err
		}
		if ctrl == harnessRoundBreak {
			break
		}
	}

	if !r.completed {
		errMsg := fmt.Sprintf("Maximum iteration count reached (%d); the Agent did not produce a final answer", r.maxIter)
		err := errors.New(errMsg)
		r.h.control.maxIterationsReached(ctx, errMsg)
		r.h.lifecycle.fail(err)
		return nil, err
	}

	res, err := r.h.control.save(ctx, r.finalContent)
	if err != nil {
		r.h.lifecycle.fail(err)
		return nil, err
	}
	r.h.lifecycle.complete()
	if err := r.h.eventError(); err != nil {
		return nil, err
	}
	return res, nil
}

func (r *agentTurnRunner) runRound(ctx context.Context, round int) (harnessRoundControl, error) {
	req, err := r.h.context.compileRound(ctx, round)
	if err != nil {
		return harnessRoundBreak, err
	}

	result, err := r.h.model.callRound(ctx, round, req)
	if err != nil {
		if r.h.streaming && r.h.state.shouldRetryStreamMidway(err) {
			backoff := streamMidwayBackoff(r.h.state.streamMidwayRetries)
			r.h.ec.l.WithFields(log.Fields{
				"retry":   r.h.state.streamMidwayRetries,
				"max":     maxStreamMidwayRetries,
				"backoff": backoff,
			}).Warn("[LLM] stream midway error, retrying current round")
			select {
			case <-ctx.Done():
				return harnessRoundBreak, ctx.Err()
			case <-time.After(backoff):
			}
			return harnessRoundNext, nil
		}
		r.h.control.failRunningPlan(ctx, err.Error())
		return harnessRoundBreak, err
	}

	if result.outcome != llmRoundCompleteToolCalls {
		return r.handleFinalCandidate(ctx, round, result)
	}
	return r.handleToolRound(ctx, round, result)
}

func (r *agentTurnRunner) handleFinalCandidate(ctx context.Context, round int, result llmRoundResult) (harnessRoundControl, error) {
	runtime := newHarnessRuntime(ctx, r.h.ec, r.h.state)
	if recovery, ok := llmRoundRecoveryFor(result, runtime.Contract, runtime.Evidence); ok {
		return r.handleLLMRoundRecovery(ctx, round, result, runtime.Contract, runtime.Evidence, recovery)
	}
	outputDecision := runtime.DecideAssistantOutput(result.content)
	if outputDecision.ShouldContinueExecution() && r.h.state.canNudge(maxHarnessNudges(runtime.Contract), round, r.maxIter) {
		attempt := r.h.state.consumeNudge()
		recordHarnessContinuationStep(ctx, r.h.ec, runtime.Evidence, outputDecision, attempt, maxHarnessNudges(runtime.Contract))
		r.h.state.Messages = append(r.h.state.Messages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleUser,
			Content: outputDecision.Prompt(),
		})
		return harnessRoundNext, nil
	}
	return r.gateFinalAnswer(ctx, round, result.content, result.deltas, false)
}

func (r *agentTurnRunner) gateFinalAnswer(ctx context.Context, round int, content string, deltas []string, explicit bool) (harnessRoundControl, error) {
	answer, done, retry := r.h.verifier.verifyFinalAnswer(ctx, round, r.maxIter, content, explicit)
	if retry {
		return harnessRoundNext, nil
	}
	r.finalContent = answer
	r.completed = done
	if done {
		if r.h.state.verifier.FinalFailed {
			if err := r.h.publishValidatedOutput([]string{answer}); err != nil {
				return harnessRoundBreak, err
			}
		} else if err := r.h.publishValidatedOutput(deltas, answer); err != nil {
			return harnessRoundBreak, err
		}
	}
	return harnessRoundBreak, nil
}

// publishValidatedOutput releases text only after the final gate accepts it.
// Intermediate, retried, and corrected model rounds stay inside the harness.
func (h *agentHarness) publishValidatedOutput(deltas []string, fallback ...string) error {
	if !h.streaming {
		return nil
	}
	if len(deltas) == 0 && len(fallback) > 0 && strings.TrimSpace(fallback[0]) != "" {
		deltas = []string{fallback[0]}
	}
	for _, delta := range deltas {
		if delta == "" {
			continue
		}
		if err := h.emit(harnesspkg.Event{
			Type:   harnesspkg.EventModelDelta,
			Layer:  harnesspkg.LayerModel,
			Status: harnesspkg.StatusRunning,
			Delta:  delta,
		}); err != nil {
			return err
		}
		if h.onDelta != nil {
			if err := h.onDelta(delta); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *agentTurnRunner) handleLLMRoundRecovery(ctx context.Context, round int, result llmRoundResult, contract harnesspkg.TaskContract, evidence harnesspkg.EvidenceLedger, recovery llmRoundRecovery) (harnessRoundControl, error) {
	maxNudges := maxHarnessNudges(contract)
	if recovery.retryable && r.h.state.canNudge(maxNudges, round, r.maxIter) {
		attempt := r.h.state.consumeNudge()
		recordLLMRoundRecoveryStep(ctx, r.h.ec, evidence, result, recovery, attempt, maxNudges, model.StepSuccess)
		r.h.state.Messages = append(r.h.state.Messages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleUser,
			Content: recovery.Prompt(),
		})
		return harnessRoundNext, nil
	}
	r.h.state.verifier.FinalFailed = true
	if strings.TrimSpace(recovery.finalMessage) != "" {
		r.finalContent = recovery.finalMessage
	} else {
		r.finalContent = "Agent 未能完成本轮模型输出恢复：" + strings.TrimSpace(recovery.reason)
	}
	recordLLMRoundRecoveryStep(ctx, r.h.ec, evidence, result, recovery, r.h.state.currentNudges(), maxNudges, model.StepError)
	r.completed = true
	return harnessRoundBreak, nil
}

func (r *agentTurnRunner) handleToolRound(ctx context.Context, round int, result llmRoundResult) (harnessRoundControl, error) {
	if r.h.control.tokenBudgetExceeded() {
		r.finalContent = result.content
		if r.finalContent == "" {
			r.finalContent = fmt.Sprintf("Token budget limit reached (%d); %d tokens have been used. Start a new conversation to continue.",
				r.h.ec.ag.TokenBudget, r.h.budget.Consumed())
		}
		r.completed = true
		return harnessRoundBreak, nil
	}

	finishSink := &finishResultSink{}
	toolCtx := withFinishResultSink(ctx, finishSink)
	toolRound := r.h.action.executeRound(toolCtx, result)
	r.h.control.afterToolRound(ctx, toolRound)
	if called, answer := finishSink.result(); called {
		if !toolRound.ToolFailed {
			r.h.state.resetNudges()
		}
		return r.gateFinalAnswer(ctx, round, answer, nil, true)
	}
	content, done, retry := r.h.verifier.verifyToolRound(ctx, round, r.maxIter, toolRound)
	if retry {
		return harnessRoundNext, nil
	}
	if done {
		r.finalContent = content
		r.completed = true
		return harnessRoundBreak, nil
	}
	if !toolRound.ToolFailed {
		r.h.state.resetNudges()
	}
	return harnessRoundNext, nil
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
	persistentMem := ""
	if !ec.ephemeral {
		identity := memorypkg.ExecutionContextFromContext(ec.ctx)
		memoryContext, memoryErr := c.h.executor.memories.BuildContext(ctx, identity, ec.userMsg)
		if memoryErr != nil {
			ec.l.WithError(memoryErr).Warn("[Memory] retrieve durable memory failed")
		} else {
			ec.memory = memoryContext
			persistentMem = memoryContext.Prompt
			if err := c.h.executor.memories.RecordUsage(ctx, memoryContext, identity); err != nil {
				ec.l.WithError(err).Warn("[Memory] record memory usage failed")
			}
		}
	}
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
		activeModel := st.currentModel(ec.ag.ModelName)
		compressionModel := cmp.Or(ec.ag.FastModelName, activeModel)
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
	modelName := st.currentModel(ec.ag.ModelName)
	reqAgent := *ec.ag
	reqAgent.ModelName = modelName
	req := openai.ChatCompletionRequest{
		Model:    modelName,
		Messages: msgs,
		Tools:    tools,
	}
	applyModelCaps(&req, &reqAgent, ec.prov.Type, ec.l)
	if webSearchEffective(&reqAgent) && !c.h.builtinWebSearchStepRecorded {
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
	primaryModel := strings.TrimSpace(req.Model)
	if primaryModel == "" {
		primaryModel = ec.ag.ModelName
	}
	if action := m.h.executor.hooks.Fire(ctx, HookPreLLMCall, &HookPayload{Model: primaryModel, Round: round}); action == HookAbort {
		ec.l.WithField("round", round).Warn("[Hook] agent execution aborted by pre_llm_call hook")
		return llmRoundResult{}, errors.New("agent execution aborted by hook")
	}

	m.h.emit(harnesspkg.Event{
		Type:   harnesspkg.EventModelStarted,
		Layer:  harnesspkg.LayerModel,
		Name:   primaryModel,
		Status: harnesspkg.StatusRunning,
		Metadata: map[string]any{
			"round": round,
		},
	})

	ec.l.WithFields(log.Fields{"round": round, "model": primaryModel}).Info("[LLM] >> call")
	llmStep := ec.tracker.BeginStep(ctx, model.StepLLMCall, primaryModel, ec.userMsg, stepMetaForModel(ec, primaryModel))
	start := time.Now()
	result, callErr := m.h.call(ctx, req)
	dur := time.Since(start)

	if callErr != nil {
		ec.l.WithFields(log.Fields{"round": round, "duration": dur, "model": primaryModel}).WithError(callErr).Error("[LLM] << failed")
		ec.tracker.FinalizeStep(ctx, llmStep, "", model.StepError, callErr.Error(), dur, 0, stepMetaForModel(ec, primaryModel))
		err := fmt.Errorf("generate content: %w", callErr)
		m.h.emit(harnesspkg.Event{
			Type:   harnesspkg.EventModelFailed,
			Layer:  harnesspkg.LayerModel,
			Name:   primaryModel,
			Status: harnesspkg.StatusFailed,
			Error:  err.Error(),
			Metadata: map[string]any{
				"round":       round,
				"duration_ms": dur.Milliseconds(),
			},
		})
		fallbackModel := fallbackModelForLLMError(ec.ag, primaryModel, callErr, m.h.state.fallbackAlreadyActivated(), req)
		if fallbackModel == "" {
			return llmRoundResult{}, err
		}
		return m.callFallbackRound(ctx, round, req, primaryModel, fallbackModel, callErr)
	}

	m.recordLLMSuccess(ctx, round, primaryModel, llmStep, result, dur, nil)
	return result, nil
}

func (m *harnessModelDriverLayer) callFallbackRound(ctx context.Context, round int, req openai.ChatCompletionRequest, primaryModel, fallbackModel string, reason error) (llmRoundResult, error) {
	ec := m.h.ec
	m.h.state.markFallbackActivated()
	m.h.emit(harnesspkg.Event{
		Type:   harnesspkg.EventControlUpdate,
		Layer:  harnesspkg.LayerModel,
		Name:   "llm_fallback",
		Status: harnesspkg.StatusRunning,
		Metadata: map[string]any{
			"round":    round,
			"primary":  primaryModel,
			"fallback": fallbackModel,
			"reason":   reason.Error(),
		},
	})
	if action := m.h.executor.hooks.Fire(ctx, HookPreLLMCall, &HookPayload{Model: fallbackModel, Round: round}); action == HookAbort {
		ec.l.WithFields(log.Fields{"round": round, "model": fallbackModel}).Warn("[Hook] fallback LLM call aborted by pre_llm_call hook")
		return llmRoundResult{}, errors.New("agent execution aborted by hook")
	}

	m.h.emit(harnesspkg.Event{
		Type:   harnesspkg.EventModelStarted,
		Layer:  harnesspkg.LayerModel,
		Name:   fallbackModel,
		Status: harnesspkg.StatusRunning,
		Metadata: map[string]any{
			"round":    round,
			"fallback": true,
			"primary":  primaryModel,
		},
	})
	ec.l.WithFields(log.Fields{"round": round, "model": fallbackModel, "primary": primaryModel}).Info("[LLM] >> fallback call")
	fallbackReq := requestForFallbackModel(req, ec.ag, fallbackModel, ec.prov.Type, ec.l)
	fallbackStep := ec.tracker.BeginStep(ctx, model.StepLLMCall, fallbackModel, ec.userMsg, stepMetaForModel(ec, fallbackModel))
	start := time.Now()
	result, callErr := m.h.call(ctx, fallbackReq)
	dur := time.Since(start)
	if callErr != nil {
		ec.l.WithFields(log.Fields{"round": round, "duration": dur, "model": fallbackModel}).WithError(callErr).Error("[LLM] << fallback failed")
		ec.tracker.FinalizeStep(ctx, fallbackStep, "", model.StepError, callErr.Error(), dur, 0, stepMetaForModel(ec, fallbackModel))
		err := fmt.Errorf("generate content with fallback %s after %s failed: %w", fallbackModel, primaryModel, callErr)
		m.h.emit(harnesspkg.Event{
			Type:   harnesspkg.EventModelFailed,
			Layer:  harnesspkg.LayerModel,
			Name:   fallbackModel,
			Status: harnesspkg.StatusFailed,
			Error:  err.Error(),
			Metadata: map[string]any{
				"round":       round,
				"fallback":    true,
				"primary":     primaryModel,
				"duration_ms": dur.Milliseconds(),
			},
		})
		return llmRoundResult{}, err
	}
	m.h.state.activateFallbackModel(fallbackModel)
	m.recordLLMSuccess(ctx, round, fallbackModel, fallbackStep, result, dur, map[string]any{
		"fallback": true,
		"primary":  primaryModel,
	})
	m.h.emit(harnesspkg.Event{
		Type:   harnesspkg.EventControlUpdate,
		Layer:  harnesspkg.LayerModel,
		Name:   "llm_fallback",
		Status: harnesspkg.StatusCompleted,
		Metadata: map[string]any{
			"round":    round,
			"primary":  primaryModel,
			"fallback": fallbackModel,
		},
	})
	return result, nil
}

func (m *harnessModelDriverLayer) recordLLMSuccess(ctx context.Context, round int, modelName string, llmStep *model.ExecutionStep, result llmRoundResult, dur time.Duration, extra map[string]any) {
	ec := m.h.ec
	ec.tracker.FinalizeStep(ctx, llmStep, result.content, model.StepSuccess, "", dur, result.tokens, stepMetaForModel(ec, modelName))
	m.h.totalTokens += result.tokens
	m.h.budget.Add(result.tokens)
	m.h.compressor.UpdatePromptTokens(result.promptTokens)
	m.h.executor.hooks.Fire(ctx, HookPostLLMCall, &HookPayload{Model: modelName, Round: round, Tokens: result.tokens})

	metadata := map[string]any{
		"round":         round,
		"duration_ms":   dur.Milliseconds(),
		"tokens":        result.tokens,
		"prompt_tokens": result.promptTokens,
	}
	for k, v := range extra {
		metadata[k] = v
	}
	m.h.emit(harnesspkg.Event{
		Type:   harnesspkg.EventModelCompleted,
		Layer:  harnesspkg.LayerModel,
		Name:   modelName,
		Status: harnesspkg.StatusCompleted,
		Output: map[string]any{
			"tool_calls":  len(result.toolCalls),
			"content_len": len(result.content),
		},
		Metadata: metadata,
	})

	if len(result.toolCalls) == 0 {
		ec.l.WithFields(log.Fields{
			"round": round, "model": modelName, "duration": dur, "tokens": result.tokens,
			"len": len(result.content), "preview": truncateLog(result.content, 200),
		}).Info("[LLM] << final answer")
	} else {
		names := make([]string, 0, len(result.toolCalls))
		for _, tc := range result.toolCalls {
			names = append(names, tc.Function.Name)
		}
		ec.l.WithFields(log.Fields{"round": round, "model": modelName, "duration": dur, "tokens": result.tokens, "tool_calls": names}).Info("[LLM] << tool calls")
	}
}

type harnessActionRuntimeLayer struct {
	h *agentHarness
}

func (a *harnessActionRuntimeLayer) executeRound(ctx context.Context, result llmRoundResult) toolRoundResult {
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
	toolRound := a.h.executor.appendAssistantToolRound(ctx, a.h.ec, a.h.state, asst, func(tc openai.ToolCall, result ToolResult) {
		status := harnessActionStatus(result.Status)
		a.h.emit(harnesspkg.Event{
			Type:   harnesspkg.EventActionFinished,
			Layer:  harnesspkg.LayerAction,
			ItemID: tc.ID,
			Name:   tc.Function.Name,
			Status: status,
			Output: result.Content,
			Error:  result.Error,
			Metadata: map[string]any{
				"duration_ms":          result.DurationMs,
				"related_plan_item_id": result.PlanItemID,
			},
		})
	})
	return toolRound
}

func harnessActionStatus(status harnesspkg.ToolStatus) harnesspkg.Status {
	switch status {
	case harnesspkg.ToolStatusError, harnesspkg.ToolStatusBlocked:
		return harnesspkg.StatusFailed
	case harnesspkg.ToolStatusSkipped:
		return harnesspkg.StatusSkipped
	default:
		return harnesspkg.StatusCompleted
	}
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

func (c *harnessControlPlaneLayer) afterToolRound(ctx context.Context, toolRound toolRoundResult) {
	if c.h.ec.plan == nil || !toolRound.HasRealTool || toolRound.HasPlanTool {
		return
	}
	// Tool evidence is associated with the currently running item, but evidence
	// alone must not change Plan State. The model's plan update or final lifecycle
	// decides whether that item is actually complete.
	c.h.emit(harnesspkg.Event{
		Type:   harnesspkg.EventControlUpdate,
		Layer:  harnesspkg.LayerControl,
		Name:   "plan_evidence_recorded",
		Status: harnesspkg.StatusCompleted,
	})
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
