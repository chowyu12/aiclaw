package harness

import (
	"encoding/json"
	"strings"
)

type FinalOutcome string

const (
	OutcomeUnknown      FinalOutcome = "unknown"
	OutcomeSuccess      FinalOutcome = "success"
	OutcomeBlocked      FinalOutcome = "blocked"
	OutcomePartial      FinalOutcome = "partial"
	OutcomeProgressOnly FinalOutcome = "progress_only"
)

type BlockerKind string

const (
	BlockerPermissionDenied BlockerKind = "permission_denied"
	BlockerAuthFailed       BlockerKind = "auth_failed"
	BlockerPolicyBlocked    BlockerKind = "policy_blocked"
	BlockerNotFound         BlockerKind = "not_found"
	BlockerRateLimited      BlockerKind = "rate_limited"
	BlockerTimeout          BlockerKind = "timeout"
	BlockerToolError        BlockerKind = "tool_error"
)

type BlockerEvidence struct {
	ToolName       string      `json:"tool_name,omitempty"`
	Kind           BlockerKind `json:"kind"`
	Message        string      `json:"message,omitempty"`
	Recoverable    bool        `json:"recoverable"`
	RequiredAction string      `json:"required_action,omitempty"`
}

type FinalAssessment struct {
	EvidenceOutcome             FinalOutcome
	CandidateOutcome            FinalOutcome
	HasSuccessfulEvidence       bool
	HasTerminalBlocker          bool
	CandidateExplainsBlocker    bool
	CandidateHasDeliverable     bool
	CandidateIsProgressOnly     bool
	TerminalBlockerEvidences    []BlockerEvidence
	RecoverableBlockerEvidences []BlockerEvidence
}

func AssessFinalOutcome(_ TaskContract, evidence EvidenceLedger, candidate Candidate) FinalAssessment {
	success := evidence.HasSuccessfulEvidence()
	terminal := evidence.TerminalBlockers()
	recoverable := evidence.RecoverableBlockers()
	candidateOutcome := assessCandidateOutcome(candidate.Content)
	assessment := FinalAssessment{
		HasSuccessfulEvidence:       success,
		HasTerminalBlocker:          len(terminal) > 0,
		CandidateOutcome:            candidateOutcome,
		CandidateExplainsBlocker:    candidateOutcome == OutcomeBlocked || candidateOutcome == OutcomePartial,
		CandidateHasDeliverable:     candidateOutcome == OutcomeSuccess || candidateOutcome == OutcomePartial,
		CandidateIsProgressOnly:     candidateOutcome == OutcomeProgressOnly,
		TerminalBlockerEvidences:    terminal,
		RecoverableBlockerEvidences: recoverable,
	}
	switch {
	case success && (len(terminal) > 0 || len(recoverable) > 0):
		assessment.EvidenceOutcome = OutcomePartial
	case success:
		assessment.EvidenceOutcome = OutcomeSuccess
	case len(terminal) > 0:
		assessment.EvidenceOutcome = OutcomeBlocked
	default:
		assessment.EvidenceOutcome = OutcomeUnknown
	}
	return assessment
}

func assessCandidateOutcome(content string) FinalOutcome {
	s := strings.ToLower(strings.TrimSpace(content))
	if s == "" {
		return OutcomeUnknown
	}
	hasBlocker := candidateExplainsBlocker(s)
	hasResult := candidateHasResultCue(s)
	switch {
	case hasBlocker && hasResult:
		return OutcomePartial
	case hasBlocker:
		return OutcomeBlocked
	case hasResult:
		return OutcomeSuccess
	case candidateLooksLikeProgressOnly(content):
		return OutcomeProgressOnly
	default:
		return OutcomeUnknown
	}
}

func ClassifyToolFailureKind(status ToolStatus, text string) BlockerKind {
	if status != ToolStatusError && status != ToolStatusBlocked {
		return ""
	}
	s := strings.ToLower(strings.TrimSpace(text))
	switch {
	case containsAnySubstring(s, []string{"permission denied", "access denied", "forbidden", "403", "无权限", "没权限", "权限不足", "无权访问", "被拒绝"}):
		return BlockerPermissionDenied
	case containsAnySubstring(s, []string{"unauthorized", "401", "auth failed", "authentication", "未授权", "认证失败", "鉴权失败", "登录失效"}):
		return BlockerAuthFailed
	case status == ToolStatusBlocked || containsAnySubstring(s, []string{"blocked by harness", "blocked by policy", "skipped by policy", "contract", "策略拦截", "规则拦截"}):
		return BlockerPolicyBlocked
	case containsAnySubstring(s, []string{"not found", "no such file", "no such tool", "tool not found", "404", "不存在", "未找到"}):
		return BlockerNotFound
	case containsAnySubstring(s, []string{"rate limit", "too many requests", "429", "限流", "频率限制"}):
		return BlockerRateLimited
	case containsAnySubstring(s, []string{"timeout", "deadline exceeded", "context deadline", "timed out", "超时"}):
		return BlockerTimeout
	default:
		return BlockerToolError
	}
}

func ToolFailureRecoverable(kind BlockerKind) bool {
	switch kind {
	case BlockerPermissionDenied, BlockerAuthFailed, BlockerPolicyBlocked, BlockerNotFound:
		return false
	case BlockerRateLimited, BlockerTimeout, BlockerToolError:
		return true
	default:
		return true
	}
}

func blockerRequiredAction(kind BlockerKind) string {
	switch kind {
	case BlockerPermissionDenied, BlockerAuthFailed:
		return "说明缺少权限或认证失败的资源/工具，并提示用户补充授权"
	case BlockerPolicyBlocked:
		return "说明工具被策略或 Harness 契约拦截，停止执行被禁止的操作"
	case BlockerNotFound:
		return "说明目标工具或资源不存在，提示用户确认名称或配置"
	case BlockerRateLimited:
		return "稍后重试，或说明外部服务限流"
	case BlockerTimeout:
		return "重试可用工具，或说明外部服务超时"
	default:
		return "重试可用工具；若仍无法完成，说明失败原因"
	}
}

func candidateLooksLikeProgressOnly(content string) bool {
	s := strings.ToLower(strings.TrimSpace(content))
	if s == "" {
		return false
	}
	if json.Valid([]byte(s)) || candidateExplainsBlocker(s) || candidateHasResultCue(s) {
		return false
	}
	return containsAnySubstring(s, []string{
		"请稍等", "稍等", "稍后", "马上",
		"让我直接", "让我继续", "我来继续", "我会继续", "我将继续",
		"继续执行", "继续生成", "继续整理", "继续查询", "继续分析",
		"现在正在", "正在生成报告", "正在生成文件", "正在生成 html", "正在生成html",
		"正在编写", "正在整理", "正在处理", "正在执行", "正在调用", "正在查询", "正在分析", "正在导出", "正在保存",
		"生成中", "编写中", "整理中", "处理中", "执行中",
		"现在用", "现在使用", "现在开始", "现在整理", "现在生成", "现在编写", "现在处理",
		"现在执行", "现在查询", "现在分析", "现在运行", "现在导出", "现在保存", "现在汇总",
		"接下来我会", "接下来我将", "接下来会", "接下来将", "下一步我会", "下一步我将", "下一步会", "下一步将",
	})
}

func candidateExplainsBlocker(s string) bool {
	if strings.Contains(s, "权限") && containsAnySubstring(s, []string{"无法", "不能", "没有", "缺少", "不足", "被拒绝", "失败"}) {
		return true
	}
	return containsAnySubstring(s, []string{
		"无法继续", "无法完成", "不能继续", "不能完成", "缺少必要", "被拒绝",
		"权限不足", "缺少权限", "无权限", "没权限", "无权访问", "未授权", "认证失败", "鉴权失败",
		"工具失败", "工具报错", "调用失败", "执行失败", "请求失败", "超时", "限流", "不存在", "未找到",
		"blocked", "failed", "failure", "error", "forbidden", "unauthorized", "permission denied", "access denied",
	})
}

func candidateHasResultCue(s string) bool {
	if containsAnySubstring(s, []string{"如下", "结果如下", "结论", "概览", "摘要", "明细", "清单", "排名", "列表", "报告内容", "输出文件", "交付文件", "文件清单", "下载链接"}) {
		return true
	}
	if strings.Contains(s, "\n## ") || strings.Contains(s, "\n### ") {
		return true
	}
	if strings.Contains(s, "\n|") && (strings.Contains(s, "|---") || strings.Contains(s, "| ---")) {
		return true
	}
	if strings.Contains(s, "outputs/") || strings.Contains(s, "sandbox:/") {
		return true
	}
	if strings.Contains(s, "](") {
		for _, ext := range []string{".md", ".html", ".xlsx", ".csv", ".docx", ".pdf", ".txt"} {
			if strings.Contains(s, ext) {
				return true
			}
		}
	}
	if containsAnySubstring(s, []string{"已生成", "已保存", "已完成"}) {
		for _, ext := range []string{".md", ".html", ".xlsx", ".csv", ".docx", ".pdf", ".txt"} {
			if strings.Contains(s, ext) {
				return true
			}
		}
	}
	return false
}

func containsAnySubstring(s string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}
