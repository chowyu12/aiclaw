package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	openai "github.com/chowyu12/go-openai"
	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/model"
	harnesspkg "github.com/chowyu12/aiclaw/pkg/harness"
)

type harnessVerifierState struct {
	ToolEvents       []harnesspkg.ToolEvent
	ValidationEvents []harnesspkg.ValidationEvent
	CorrectionEvents []harnesspkg.CorrectionEvent
	Correction       harnesspkg.CorrectionState
	Nudges           int
	FinalFailed      bool
}

type harnessVerifierLayer struct {
	h *agentHarness
}

func (v *harnessVerifierLayer) verifyFinalAnswer(ctx context.Context, round, maxIter int, content string, explicit bool) (string, bool, bool) {
	runtime := newHarnessRuntime(ctx, v.h.ec, v.h.state)
	result := runtime.FinalGate(content)
	if explicit {
		result = runtime.FinalGateExplicit(content)
	}
	recordHarnessValidation(v.h.state, result)
	recordHarnessValidationStep(ctx, v.h.ec, result)
	if result.Allowed {
		return content, true, false
	}
	return v.correct(ctx, runtime, result, round, maxIter, "final_gate_rejected", "Agent 自检未通过，已停止继续重试")
}

func (v *harnessVerifierLayer) verifyToolRound(ctx context.Context, round, maxIter int, toolRound toolRoundResult) (string, bool, bool) {
	runtime := newHarnessRuntimeForToolRound(ctx, v.h.ec, v.h.state, toolRound)
	result := runtime.AfterTool()
	recordHarnessValidation(v.h.state, result)
	recordHarnessValidationStep(ctx, v.h.ec, result)
	if result.Allowed {
		return "", false, false
	}
	return v.correct(ctx, runtime, result, round, maxIter, "tool_evidence_rejected", "Agent 工具执行自检未通过，已停止继续重试")
}

func (v *harnessVerifierLayer) correct(ctx context.Context, runtime harnesspkg.Runtime, result harnesspkg.ValidationResult, round, maxIter int, name, fallbackPrefix string) (string, bool, bool) {
	action := v.h.state.nudgeCorrectionAction(runtime, result, round, maxIter)
	if action.Outcome != harnesspkg.CorrectionContinue && strings.TrimSpace(action.Message) == "" {
		action.Message = fallbackPrefix + "：" + strings.Join(result.Reasons(), "；")
	}
	recordHarnessCorrection(v.h.state, action, result)
	recordHarnessCorrectionStep(ctx, v.h.ec, action, result)
	v.h.emit(harnesspkg.Event{
		Type:   harnesspkg.EventControlUpdate,
		Layer:  harnesspkg.LayerControl,
		Name:   name,
		Status: harnesspkg.StatusFailed,
		Error:  strings.Join(result.Reasons(), "；"),
		Metadata: map[string]any{
			"round":           round,
			"attempt":         action.Attempt,
			"max_attempts":    action.MaxAttempts,
			"violation_codes": result.ViolationCodes(),
		},
	})
	v.h.ec.l.WithFields(log.Fields{
		"round":   round,
		"reasons": result.Reasons(),
		"attempt": action.Attempt,
	}).Warn("[Harness] validation rejected")
	if action.Outcome == harnesspkg.CorrectionContinue && round < maxIter {
		v.h.state.Messages = append(v.h.state.Messages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleUser,
			Content: action.Prompt,
		})
		return "", false, true
	}
	v.h.state.verifier.FinalFailed = true
	if strings.TrimSpace(action.Message) != "" {
		return action.Message, true, false
	}
	return fallbackPrefix + "：" + strings.Join(result.Reasons(), "；"), true, false
}

func (st *harnessTurnState) canNudge(max, round, maxIter int) bool {
	if st == nil {
		return false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.verifier.Nudges < max && round+1 < maxIter
}

func (st *harnessTurnState) consumeNudge() int {
	if st == nil {
		return 0
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	st.verifier.Nudges++
	return st.verifier.Nudges
}

func (st *harnessTurnState) resetNudges() {
	if st == nil {
		return
	}
	st.mu.Lock()
	st.verifier.Nudges = 0
	st.mu.Unlock()
}

func (st *harnessTurnState) currentNudges() int {
	if st == nil {
		return 0
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.verifier.Nudges
}

func (st *harnessTurnState) nudgeCorrectionAction(runtime harnesspkg.Runtime, result harnesspkg.ValidationResult, round, maxIter int) harnesspkg.CorrectionAction {
	maxNudges := maxHarnessNudges(runtime.Contract)
	if st.canNudge(maxNudges, round, maxIter) {
		attempt := st.consumeNudge()
		return harnesspkg.CorrectionAction{
			Outcome:     harnesspkg.CorrectionContinue,
			Prompt:      harnesspkg.CorrectionPrompt(result, attempt, maxNudges),
			Attempt:     attempt,
			MaxAttempts: maxNudges,
		}
	}
	attempt := 0
	if st != nil {
		st.mu.Lock()
		attempt = st.verifier.Nudges
		st.mu.Unlock()
	}
	return harnesspkg.CorrectionAction{
		Outcome:     harnesspkg.CorrectionFailed,
		Message:     "Agent 多次自检未通过，已停止继续重试：" + strings.Join(result.Reasons(), "；"),
		Attempt:     attempt,
		MaxAttempts: maxNudges,
	}
}

func maxHarnessNudges(contract harnesspkg.TaskContract) int {
	if contract.MaxCorrectionAttempts > 0 {
		return contract.MaxCorrectionAttempts
	}
	return 2
}

func newHarnessRuntime(ctx context.Context, ec *execContext, st *harnessTurnState) harnesspkg.Runtime {
	evidence := newHarnessRuntimeEvidence(ctx, ec, st)
	return harnesspkg.NewRuntime(newHarnessRuntimeContract(ec, st, evidence), evidence)
}

func newHarnessRuntimeForToolRound(ctx context.Context, ec *execContext, st *harnessTurnState, round toolRoundResult) harnesspkg.Runtime {
	evidence := newHarnessRuntimeEvidence(ctx, ec, st)
	if len(round.ToolEvents) > 0 {
		evidence.ToolEvents = append([]harnesspkg.ToolEvent(nil), round.ToolEvents...)
		evidence.ExecutionTools = harnessExecutionTools(evidence.ToolEvents)
	}
	return harnesspkg.NewRuntime(newHarnessRuntimeContract(ec, st, evidence), evidence)
}

func newHarnessRuntimeWithFiles(ctx context.Context, ec *execContext, st *harnessTurnState, files []*model.File) harnesspkg.Runtime {
	evidence := newHarnessRuntimeEvidence(ctx, ec, st)
	evidence.ArtifactCount = 0
	evidence.Artifacts = nil
	for _, f := range files {
		if f == nil {
			continue
		}
		evidence.ArtifactCount++
		evidence.Artifacts = append(evidence.Artifacts, artifactEvidenceFromFile(f, ""))
	}
	return harnesspkg.NewRuntime(newHarnessRuntimeContract(ec, st, evidence), evidence)
}

func newHarnessRuntimeContract(ec *execContext, st *harnessTurnState, evidence harnesspkg.EvidenceLedger) harnesspkg.TaskContract {
	in := harnesspkg.ContractInput{}
	if ec != nil {
		in.Objective = ec.userMsg
		in.FileCount = len(ec.files)
		if ec.ag != nil {
			in.AgentTaskProfile = harnesspkg.InferAgentTaskProfile(ec.ag.SystemPrompt)
		}
		in.ToolCount = len(ec.agentTools) + len(ec.mcpTools) + len(ec.skillTools)
		in.SubAgent = ec.ctx != nil && subAgentDepth(ec.ctx) > 0
	}
	in.PlanEnabled = evidence.Plan != nil && len(evidence.Plan.Items) > 0
	in.ForcePlan = in.PlanEnabled
	in.ToolEvidenceRequired = toolEvidenceRequired(st)
	return harnesspkg.BuildContract(in)
}

func toolEvidenceRequired(st *harnessTurnState) bool {
	if st == nil {
		return false
	}
	st.mu.Lock()
	if len(st.verifier.ToolEvents) > 0 {
		st.mu.Unlock()
		return true
	}
	called := make(map[string]bool, len(st.calledTools))
	for k, v := range st.calledTools {
		called[k] = v
	}
	st.mu.Unlock()
	return len(countExecutionTools(called)) > 0
}

func harnessExecutionTools(events []harnesspkg.ToolEvent) []string {
	if len(events) == 0 {
		return nil
	}
	called := make(map[string]bool, len(events))
	for _, ev := range events {
		name := strings.TrimSpace(ev.ToolName)
		if name == "" {
			continue
		}
		called[name] = true
	}
	return countExecutionTools(called)
}

func newHarnessRuntimeEvidence(ctx context.Context, ec *execContext, st *harnessTurnState) harnesspkg.EvidenceLedger {
	evidence := harnesspkg.EvidenceLedger{}
	if ec != nil {
		toolCount := len(ec.agentTools) + len(ec.mcpTools) + len(ec.skillTools)
		if toolCount > 0 {
			evidence.InformEvents = append(evidence.InformEvents, harnesspkg.InformEvent{
				Key:     "tools",
				Summary: fmt.Sprintf("已加载 %d 个可用工具", toolCount),
			})
		}
		if len(ec.files) > 0 {
			evidence.InformEvents = append(evidence.InformEvents, harnesspkg.InformEvent{
				Key:     "input_files",
				Summary: fmt.Sprintf("用户输入包含 %d 个附件", len(ec.files)),
			})
		}
		files := dedupeFiles(ec.toolFiles)
		evidence.ArtifactCount = len(files)
		for _, f := range files {
			if f == nil {
				continue
			}
			evidence.Artifacts = append(evidence.Artifacts, artifactEvidenceFromFile(f, ""))
		}
		if ec.plan != nil {
			if plan := planSnapshot(ctx, ec.plan); plan != nil {
				evidence.Plan = plan
				evidence.PlanSource = string(plan.Source)
			}
		}
	}
	if st != nil {
		st.mu.Lock()
		called := make(map[string]bool, len(st.calledTools))
		for k, v := range st.calledTools {
			called[k] = v
		}
		evidence.ToolEvents = append([]harnesspkg.ToolEvent(nil), st.verifier.ToolEvents...)
		evidence.ValidationEvents = append([]harnesspkg.ValidationEvent(nil), st.verifier.ValidationEvents...)
		evidence.Corrections = append([]harnesspkg.CorrectionEvent(nil), st.verifier.CorrectionEvents...)
		st.mu.Unlock()
		evidence.ExecutionTools = countExecutionTools(called)
	}
	return evidence
}

func planSnapshot(ctx context.Context, pm *PlanManager) *harnesspkg.PlanSnapshot {
	if pm == nil {
		return nil
	}
	state, err := pm.activeState(ctx)
	if err != nil || state == nil || len(state.Items) == 0 {
		return nil
	}
	out := &harnesspkg.PlanSnapshot{Source: string(state.Source), Items: make([]harnesspkg.PlanItemSnapshot, 0, len(state.Items))}
	for _, item := range state.Items {
		out.Items = append(out.Items, harnesspkg.PlanItemSnapshot{
			ID:     strings.TrimSpace(item.ItemKey),
			Title:  strings.TrimSpace(item.Title),
			Status: string(item.Status),
		})
	}
	return out
}

func countExecutionTools(called map[string]bool) []string {
	if len(called) == 0 {
		return nil
	}
	out := make([]string, 0, len(called))
	for name, ok := range called {
		if !ok {
			continue
		}
		switch name {
		case "", planToolName, toolSearchName, finishToolName:
			continue
		default:
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func artifactEvidenceFromFile(f *model.File, sourceTool string) harnesspkg.ArtifactEvidence {
	if f == nil {
		return harnesspkg.ArtifactEvidence{}
	}
	return harnesspkg.ArtifactEvidence{
		UUID:        strings.TrimSpace(f.UUID),
		Filename:    strings.TrimSpace(f.Filename),
		StoragePath: strings.TrimSpace(f.StoragePath),
		MimeType:    strings.TrimSpace(f.ContentType),
		Size:        f.FileSize,
		SourceTool:  strings.TrimSpace(sourceTool),
	}
}

func recordHarnessToolEvent(st *harnessTurnState, ev harnesspkg.ToolEvent) {
	if st == nil || strings.TrimSpace(ev.ToolName) == "" {
		return
	}
	st.mu.Lock()
	st.verifier.ToolEvents = append(st.verifier.ToolEvents, ev)
	st.mu.Unlock()
}

func recordHarnessToolResult(st *harnessTurnState, args string, tr ToolResult) {
	ev, ok := harnessToolEventFromResult(args, tr)
	if !ok {
		return
	}
	recordHarnessToolEvent(st, ev)
}

func harnessToolEventFromResult(args string, tr ToolResult) (harnesspkg.ToolEvent, bool) {
	if tr.ToolName == "" {
		return harnesspkg.ToolEvent{}, false
	}
	files := make([]harnesspkg.ArtifactEvidence, 0, len(tr.Files))
	for _, f := range tr.Files {
		if f == nil {
			continue
		}
		files = append(files, artifactEvidenceFromFile(f, tr.ToolName))
	}
	return harnesspkg.ToolEvent{
		CallID:            tr.ToolCallID,
		ToolName:          tr.ToolName,
		ArgsSummary:       truncateLog(args, 600),
		OutputSummary:     truncateLog(tr.Content, 1200),
		Status:            tr.Status,
		Error:             tr.Error,
		FailureKind:       harnesspkg.ClassifyToolFailureKind(tr.Status, tr.Error+" "+tr.Content),
		Files:             files,
		DurationMs:        tr.DurationMs,
		RelatedPlanItemID: tr.PlanItemID,
	}, true
}

func recordHarnessValidation(st *harnessTurnState, result harnesspkg.ValidationResult) {
	if st == nil {
		return
	}
	st.mu.Lock()
	st.verifier.ValidationEvents = append(st.verifier.ValidationEvents, harnesspkg.ValidationEvent{
		Stage:      result.Stage,
		Allowed:    result.Allowed,
		Violations: append([]harnesspkg.Violation(nil), result.Violations...),
	})
	st.mu.Unlock()
}

func recordHarnessCorrection(st *harnessTurnState, action harnesspkg.CorrectionAction, result harnesspkg.ValidationResult) {
	if st == nil {
		return
	}
	st.mu.Lock()
	st.verifier.CorrectionEvents = append(st.verifier.CorrectionEvents, harnesspkg.CorrectionEvent{
		Attempt:         action.Attempt,
		Outcome:         action.Outcome,
		ViolationCodes:  result.ViolationCodes(),
		RequiredActions: result.RequiredActions(),
	})
	st.mu.Unlock()
}

func recordHarnessValidationStep(ctx context.Context, ec *execContext, result harnesspkg.ValidationResult) {
	if ec == nil || ec.tracker == nil || len(result.Violations) == 0 {
		return
	}
	reasons := strings.Join(result.Reasons(), "\n")
	status := model.StepSuccess
	stepErr := ""
	if !result.Allowed {
		status = model.StepError
		stepErr = reasons
	}
	meta := ec.stepMeta()
	meta.Harness = harnessStepMeta(result, nil)
	ec.tracker.RecordStep(ctx, model.StepHarness, "validate_"+string(result.Stage), "", reasons, status, stepErr, 0, 0, meta)
}

func recordHarnessCorrectionStep(ctx context.Context, ec *execContext, action harnesspkg.CorrectionAction, result harnesspkg.ValidationResult) {
	if ec == nil || ec.tracker == nil {
		return
	}
	status := model.StepSuccess
	stepErr := ""
	output := action.Prompt
	if action.Outcome != harnesspkg.CorrectionContinue {
		status = model.StepError
		output = action.Message
		stepErr = action.Message
	}
	meta := ec.stepMeta()
	meta.Harness = harnessStepMeta(result, &action)
	ec.tracker.RecordStep(ctx, model.StepHarness, "correct_"+string(result.Stage), strings.Join(result.Reasons(), "\n"), output, status, stepErr, time.Duration(0), 0, meta)
}

func recordHarnessContinuationStep(ctx context.Context, ec *execContext, evidence harnesspkg.EvidenceLedger, decision harnesspkg.AssistantOutputDecision, attempt, maxAttempts int) {
	if ec == nil || ec.tracker == nil {
		return
	}
	action := strings.TrimSpace(decision.RequiredAction)
	meta := ec.stepMeta()
	meta.Harness = &model.StepHarnessMeta{
		Stage:           "assistant_output",
		Allowed:         true,
		RequiredActions: nonEmptyStrings(action),
		Evidence:        harnessEvidenceMeta(evidence),
		Correction: &model.StepHarnessCorrection{
			Attempt:     attempt,
			MaxAttempts: maxAttempts,
			Outcome:     string(harnesspkg.CorrectionContinue),
		},
	}
	input := strings.TrimSpace(decision.Reason)
	if attempt > 0 && maxAttempts > 0 {
		input = fmt.Sprintf("%s (%d/%d)", input, attempt, maxAttempts)
	}
	ec.tracker.RecordStep(ctx, model.StepHarness, "continue_execution", input, decision.Prompt(), model.StepSuccess, "", 0, 0, meta)
}

func recordLLMRoundRecoveryStep(ctx context.Context, ec *execContext, evidence harnesspkg.EvidenceLedger, result llmRoundResult, recovery llmRoundRecovery, attempt, maxAttempts int, status model.StepStatus) {
	if ec == nil || ec.tracker == nil {
		return
	}
	meta := ec.stepMeta()
	meta.Harness = &model.StepHarnessMeta{
		Stage:           "llm_round",
		Allowed:         status != model.StepError,
		RequiredActions: nonEmptyStrings(recovery.action),
		Evidence:        harnessEvidenceMeta(evidence),
	}
	if attempt > 0 || maxAttempts > 0 {
		meta.Harness.Correction = &model.StepHarnessCorrection{
			Attempt:     attempt,
			MaxAttempts: maxAttempts,
		}
		if status == model.StepError {
			meta.Harness.Correction.Outcome = string(harnesspkg.CorrectionFailed)
		} else {
			meta.Harness.Correction.Outcome = string(harnesspkg.CorrectionContinue)
		}
	}
	input := strings.TrimSpace(recovery.reason)
	if attempt > 0 && maxAttempts > 0 {
		input = fmt.Sprintf("%s (%d/%d)", input, attempt, maxAttempts)
	}
	output := recovery.Prompt()
	stepErr := ""
	if status == model.StepError {
		output = strings.TrimSpace(recovery.finalMessage)
		if output == "" {
			output = recovery.Prompt()
		}
		stepErr = output
	}
	if output == "" {
		output = recovery.Prompt()
	}
	_ = result
	ec.tracker.RecordStep(ctx, model.StepHarness, "recover_llm_round", input, output, status, stepErr, 0, 0, meta)
}

func nonEmptyStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func harnessStepMeta(result harnesspkg.ValidationResult, action *harnesspkg.CorrectionAction) *model.StepHarnessMeta {
	meta := &model.StepHarnessMeta{
		Stage:           string(result.Stage),
		Allowed:         result.Allowed,
		ViolationCodes:  result.ViolationCodes(),
		RequiredActions: result.RequiredActions(),
		Evidence:        harnessEvidenceMeta(result.Evidence),
	}
	if action != nil {
		meta.Correction = &model.StepHarnessCorrection{
			Attempt:     action.Attempt,
			MaxAttempts: action.MaxAttempts,
			Outcome:     string(action.Outcome),
		}
	}
	return meta
}

func harnessEvidenceMeta(evidence harnesspkg.EvidenceLedger) model.StepHarnessEvidenceMeta {
	return model.StepHarnessEvidenceMeta{
		ExecutionTools: evidence.ExecutionTools,
		ToolEventCount: len(evidence.ToolEvents),
		ArtifactCount:  evidence.ActualArtifactCount(),
		PlanTerminal:   harnesspkg.PlanTerminal(evidence.Plan),
	}
}
