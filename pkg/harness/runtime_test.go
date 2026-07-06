package harness

import (
	"strings"
	"testing"
)

func TestRuntimeRejectsEmptyFinalAnswer(t *testing.T) {
	runtime := NewRuntime(TaskContract{RequireFinalAnswer: true}, EvidenceLedger{})

	result := runtime.FinalGate("  ")
	if result.Allowed {
		t.Fatal("expected empty final answer to be rejected")
	}
	if got := result.ViolationCodes(); len(got) != 1 || got[0] != "final_answer_empty" {
		t.Fatalf("violation codes = %#v", got)
	}
}

func TestRuntimeRejectsProgressOnlyFinalAnswer(t *testing.T) {
	runtime := NewRuntime(TaskContract{RequireFinalAnswer: true}, EvidenceLedger{})

	result := runtime.FinalGate("正在生成报告，请稍等。")
	if result.Allowed {
		t.Fatal("expected progress-only final answer to be rejected")
	}
	if got := result.ViolationCodes(); len(got) != 1 || got[0] != "final_answer_incomplete_progress" {
		t.Fatalf("violation codes = %#v", got)
	}
}

func TestRuntimeRejectsClaimedFileWithoutArtifact(t *testing.T) {
	runtime := NewRuntime(TaskContract{RequireFinalAnswer: true}, EvidenceLedger{})

	result := runtime.FinalGate("已完成，文件已生成：report.html")
	if result.Allowed {
		t.Fatal("expected claimed file output without artifact to be rejected")
	}
	if got := result.ViolationCodes(); len(got) != 1 || got[0] != "required_artifact_missing" {
		t.Fatalf("violation codes = %#v", got)
	}
}

func TestRuntimeTreatsFailedPlanItemAsTerminal(t *testing.T) {
	runtime := NewRuntime(
		TaskContract{RequireFinalAnswer: true, RequirePlanTerminal: true},
		EvidenceLedger{Plan: &PlanSnapshot{Items: []PlanItemSnapshot{
			{ID: "collect", Title: "collect data", Status: "completed"},
			{ID: "query", Title: "query system", Status: "failed"},
		}}},
	)

	result := runtime.FinalGate("无法继续执行：目标系统返回权限错误。")
	if !result.Allowed {
		t.Fatalf("expected failed plan item to be terminal, violations = %#v", result.Violations)
	}
}

func TestRuntimeCorrectionStopsAfterMaxAttempts(t *testing.T) {
	runtime := NewRuntime(TaskContract{RequireFinalAnswer: true, MaxCorrectionAttempts: 1}, EvidenceLedger{})
	result := runtime.FinalGate(" ")
	state := CorrectionState{}

	first := runtime.Correct(&state, result)
	if first.Outcome != CorrectionContinue || first.Prompt == "" {
		t.Fatalf("first correction = %#v", first)
	}
	second := runtime.Correct(&state, result)
	if second.Outcome != CorrectionFailed || !strings.Contains(second.Message, "自检未通过") {
		t.Fatalf("second correction = %#v", second)
	}
}
