package agent

import (
	"context"
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
	FinalFailed      bool
}

type harnessVerifierLayer struct {
	h *agentHarness
}

func (v *harnessVerifierLayer) verifyFinalAnswer(ctx context.Context, round, maxIter int, content string) (string, bool, bool) {
	runtime := newHarnessRuntime(ctx, v.h.ec, v.h.state)
	result := runtime.FinalGate(content)
	recordHarnessValidation(v.h.state, result)
	recordHarnessValidationStep(ctx, v.h.ec, result)
	if result.Allowed {
		return content, true, false
	}
	return v.correct(ctx, runtime, result, round, maxIter, "final_gate_rejected", "Agent 自检未通过，已停止继续重试")
}

func (v *harnessVerifierLayer) verifyToolRound(ctx context.Context, round, maxIter int) (string, bool, bool) {
	runtime := newHarnessRuntime(ctx, v.h.ec, v.h.state)
	result := runtime.AfterTool()
	recordHarnessValidation(v.h.state, result)
	recordHarnessValidationStep(ctx, v.h.ec, result)
	if result.Allowed {
		return "", false, false
	}
	return v.correct(ctx, runtime, result, round, maxIter, "tool_evidence_rejected", "Agent 工具执行自检未通过，已停止继续重试")
}

func (v *harnessVerifierLayer) correct(ctx context.Context, runtime harnesspkg.Runtime, result harnesspkg.ValidationResult, round, maxIter int, name, fallbackPrefix string) (string, bool, bool) {
	action := runtime.Correct(&v.h.state.verifier.Correction, result)
	if action.Outcome == harnesspkg.CorrectionContinue && round >= maxIter {
		action.Outcome = harnesspkg.CorrectionFailed
		action.Prompt = ""
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

func newHarnessRuntime(ctx context.Context, ec *execContext, st *harnessTurnState) harnesspkg.Runtime {
	evidence := newHarnessRuntimeEvidence(ctx, ec, st)
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
	}
	in.PlanEnabled = evidence.Plan != nil && len(evidence.Plan.Items) > 0
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

func newHarnessRuntimeEvidence(ctx context.Context, ec *execContext, st *harnessTurnState) harnesspkg.EvidenceLedger {
	evidence := harnesspkg.EvidenceLedger{}
	if ec != nil {
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
				evidence.PlanSource = "plan"
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
	out := &harnesspkg.PlanSnapshot{Items: make([]harnesspkg.PlanItemSnapshot, 0, len(state.Items))}
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
		case "", planToolName, toolSearchName:
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
	if st == nil || tr.ToolName == "" {
		return
	}
	files := make([]harnesspkg.ArtifactEvidence, 0, len(tr.Files))
	for _, f := range tr.Files {
		if f == nil {
			continue
		}
		files = append(files, artifactEvidenceFromFile(f, tr.ToolName))
	}
	recordHarnessToolEvent(st, harnesspkg.ToolEvent{
		CallID:        tr.ToolCallID,
		ToolName:      tr.ToolName,
		ArgsSummary:   truncateLog(args, 600),
		OutputSummary: truncateLog(tr.Content, 1200),
		Status:        tr.Status,
		Error:         tr.Error,
		Files:         files,
		DurationMs:    tr.DurationMs,
	})
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

func harnessStepMeta(result harnesspkg.ValidationResult, action *harnesspkg.CorrectionAction) *model.StepHarnessMeta {
	meta := &model.StepHarnessMeta{
		Stage:           string(result.Stage),
		Allowed:         result.Allowed,
		ViolationCodes:  result.ViolationCodes(),
		RequiredActions: result.RequiredActions(),
		Evidence: model.StepHarnessEvidenceMeta{
			ExecutionTools: result.Evidence.ExecutionTools,
			ToolEventCount: len(result.Evidence.ToolEvents),
			ArtifactCount:  result.Evidence.ActualArtifactCount(),
			PlanTerminal:   harnesspkg.PlanTerminal(result.Evidence.Plan),
		},
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
