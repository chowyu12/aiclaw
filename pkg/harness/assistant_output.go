package harness

import (
	"fmt"
	"strings"
)

type AssistantOutputIntent string

const (
	OutputIntentFinalCandidate    AssistantOutputIntent = "final_candidate"
	OutputIntentContinueExecution AssistantOutputIntent = "continue_execution"
)

type AssistantOutputDecision struct {
	Intent           AssistantOutputIntent
	Reason           string
	RequiredAction   string
	CandidateOutcome FinalOutcome
}

func (r Runtime) DecideAssistantOutput(content string) AssistantOutputDecision {
	candidate := Candidate{Content: strings.TrimSpace(content)}
	assessment := AssessFinalOutcome(r.Contract, r.Evidence, candidate)
	decision := AssistantOutputDecision{
		Intent:           OutputIntentFinalCandidate,
		CandidateOutcome: assessment.CandidateOutcome,
	}
	if assessment.CandidateOutcome != OutcomeProgressOnly || assessment.HasTerminalBlocker {
		return decision
	}
	if !progressCanContinue(r.Contract, r.Evidence, candidate.Content) {
		return decision
	}
	decision.Intent = OutputIntentContinueExecution
	decision.Reason = "模型输出的是执行意图或进度声明，不是最终答复"
	decision.RequiredAction = continueExecutionAction(r.Contract, r.Evidence, candidate.Content)
	return decision
}

func (d AssistantOutputDecision) ShouldContinueExecution() bool {
	return d.Intent == OutputIntentContinueExecution
}

func (d AssistantOutputDecision) Prompt() string {
	action := strings.TrimSpace(d.RequiredAction)
	if action == "" {
		action = "继续执行刚才声明的下一步；需要工具就直接调用工具，无法继续则说明具体阻塞原因。"
	}
	reason := strings.TrimSpace(d.Reason)
	if reason == "" {
		reason = "上一轮输出不是最终答复"
	}
	return fmt.Sprintf(`%s。

请不要继续输出“正在/即将/稍后/现在开始”等进度说明。
%s
完成真实执行后，再输出最终结果。`, reason, action)
}

func progressCanContinue(contract TaskContract, evidence EvidenceLedger, content string) bool {
	if len(evidence.TerminalBlockers()) > 0 {
		return false
	}
	if contract.RequiredArtifactCount > 0 || contract.OutputMode == OutputModeFile || contract.OutputMode == OutputModeMixed {
		return evidence.ActualArtifactCount() < maxInt(1, contract.RequiredArtifactCount)
	}
	if candidateClaimsFile(content) {
		return evidence.ActualArtifactCount() == 0
	}
	return true
}

func continueExecutionAction(contract TaskContract, evidence EvidenceLedger, content string) string {
	if contract.RequiredArtifactCount > 0 || contract.OutputMode == OutputModeFile || contract.OutputMode == OutputModeMixed || candidateClaimsFile(content) {
		if evidence.ActualArtifactCount() == 0 {
			return "如果任务需要生成报告或文件，下一轮必须直接调用可用工具生成并登记文件产物；如果无法生成，说明缺少的工具、权限或数据。"
		}
		return "文件产物已存在时，请基于产物和工具结果汇总最终答复，并包含真实文件信息。"
	}
	if len(evidence.ExecutionTools) == 0 {
		return "如果刚才声明需要查询、计算、整理或运行代码，下一轮必须直接调用对应工具；如果没有可用工具，说明具体阻塞原因。"
	}
	return "请基于已完成的工具结果继续处理或汇总；若还缺少证据，直接调用下一步工具。"
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
