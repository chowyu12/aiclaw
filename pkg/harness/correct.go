package harness

import (
	"fmt"
	"strings"
)

type CorrectionOutcome string

const (
	CorrectionContinue CorrectionOutcome = "continue"
	CorrectionFailed   CorrectionOutcome = "failed"
	CorrectionBlocked  CorrectionOutcome = "blocked"
)

type CorrectionState struct {
	Attempts       int
	MaxAttempts    int
	LastViolations []Violation
}

type CorrectionAction struct {
	Outcome     CorrectionOutcome
	Prompt      string
	Message     string
	Attempt     int
	MaxAttempts int
}

func (r Runtime) Correct(state *CorrectionState, v ValidationResult) CorrectionAction {
	if state == nil {
		state = &CorrectionState{}
	}
	maxAttempts := state.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = r.Contract.MaxCorrectionAttempts
	}
	if maxAttempts <= 0 {
		maxAttempts = 2
	}
	state.MaxAttempts = maxAttempts
	state.Attempts++
	state.LastViolations = append([]Violation(nil), v.Violations...)

	if state.Attempts > maxAttempts {
		return CorrectionAction{
			Outcome:     CorrectionFailed,
			Message:     "Agent 多次自检未通过，已停止继续重试：" + strings.Join(v.Reasons(), "；"),
			Attempt:     state.Attempts,
			MaxAttempts: maxAttempts,
		}
	}

	return CorrectionAction{
		Outcome:     CorrectionContinue,
		Prompt:      CorrectionPrompt(v, state.Attempts, maxAttempts),
		Attempt:     state.Attempts,
		MaxAttempts: maxAttempts,
	}
}

func (r Runtime) CorrectionPrompt(v ValidationResult) string {
	return CorrectionPrompt(v, 0, r.Contract.MaxCorrectionAttempts)
}

func CorrectionPrompt(v ValidationResult, attempt, maxAttempts int) string {
	reasons := linesOrDefault(v.Reasons(), "未知原因")
	actions := linesOrDefault(v.RequiredActions(), "继续完成缺口")
	progress := ""
	if attempt > 0 && maxAttempts > 0 {
		progress = fmt.Sprintf("纠偏次数：%d/%d\n\n", attempt, maxAttempts)
	}
	return fmt.Sprintf(`当前还不能直接结束。

%s原因：
%s

需要补救：
%s

已观察到：
- 已调用业务工具：%d 个
- 工具事件：%d 个
- 已生成文件：%d 个
- 计划是否完成：%t

请继续完成缺口。需要调用工具就继续调用；需要更新计划就调用 plan；确认已满足用户目标后，再给出最终答复。`,
		progress,
		reasons,
		actions,
		len(v.Evidence.ExecutionTools),
		len(v.Evidence.ToolEvents),
		v.Evidence.ActualArtifactCount(),
		PlanTerminal(v.Evidence.Plan),
	)
}

func linesOrDefault(lines []string, fallback string) string {
	if len(lines) == 0 {
		return "- " + fallback
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, "- "+line)
		}
	}
	if len(out) == 0 {
		return "- " + fallback
	}
	return strings.Join(out, "\n")
}
