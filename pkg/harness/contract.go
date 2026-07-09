package harness

import "strings"

const PlanSourceHarness = "harness"

type Complexity string

const (
	ComplexitySimple  Complexity = "simple"
	ComplexityNormal  Complexity = "normal"
	ComplexityComplex Complexity = "complex"
)

type OutputMode string

const (
	OutputModeText  OutputMode = "text"
	OutputModeJSON  OutputMode = "json"
	OutputModeFile  OutputMode = "file"
	OutputModeMixed OutputMode = "mixed"
)

type RiskLevel string

const (
	RiskLevelLow    RiskLevel = "low"
	RiskLevelMedium RiskLevel = "medium"
	RiskLevelHigh   RiskLevel = "high"
)

type CheckKind string

const (
	CheckPlanTerminal CheckKind = "plan_terminal"
	CheckEvidence     CheckKind = "evidence"
	CheckArtifact     CheckKind = "artifact"
	CheckJSON         CheckKind = "json"
)

type ToolPolicy struct {
	Allowed         []string `json:"allowed,omitempty"`
	Blocked         []string `json:"blocked,omitempty"`
	AllowWrite      bool     `json:"allow_write,omitempty"`
	RequireApproval bool     `json:"require_approval,omitempty"`
}

type TaskContract struct {
	Objective             string      `json:"objective,omitempty"`
	Complexity            Complexity  `json:"complexity,omitempty"`
	OutputMode            OutputMode  `json:"output_mode,omitempty"`
	ResponseFormat        string      `json:"response_format,omitempty"`
	RequiredArtifactCount int         `json:"required_artifact_count,omitempty"`
	RequiredChecks        []CheckKind `json:"required_checks,omitempty"`
	RiskLevel             RiskLevel   `json:"risk_level,omitempty"`
	ToolPolicy            ToolPolicy  `json:"tool_policy,omitempty"`
	RequirePlan           bool        `json:"require_plan,omitempty"`
	RequireEvidence       bool        `json:"require_evidence,omitempty"`
	RequireToolEvidence   bool        `json:"require_tool_evidence,omitempty"`
	RequireFinalAnswer    bool        `json:"require_final_answer,omitempty"`
	RequirePlanTerminal   bool        `json:"require_plan_terminal,omitempty"`
	MaxCorrectionAttempts int         `json:"max_correction_attempts,omitempty"`
}

type AgentTaskProfile struct {
	HasMultiStepWorkflow bool
	RequiresValidation   bool
	RequiresFileOutput   bool
	RequiresDataAnalysis bool
	RequiresToolOrData   bool
}

type ContractInput struct {
	Objective             string
	AgentTaskProfile      AgentTaskProfile
	PlanEnabled           bool
	ForcePlan             bool
	ResponseFormatEnabled bool
	ResponseFormat        string
	ToolEvidenceRequired  bool
	ToolCount             int
	FileCount             int
	SubAgent              bool
}

func BuildContract(in ContractInput) TaskContract {
	objective := strings.TrimSpace(in.Objective)
	planRequired := in.PlanEnabled || in.ForcePlan || shouldRequirePlan(in)
	outputMode := OutputModeText
	requiredArtifacts := 0
	fileDeliverableRequired := hasFileDeliverableIntent(objective) ||
		(in.AgentTaskProfile.RequiresFileOutput && hasWorkIntent(strings.ToLower(objective)))
	if in.ResponseFormatEnabled {
		outputMode = OutputModeJSON
	} else if fileDeliverableRequired {
		outputMode = OutputModeFile
		requiredArtifacts = 1
	}
	if outputMode == OutputModeFile && in.FileCount > 0 {
		outputMode = OutputModeMixed
	}

	toolEvidenceRequired := in.ToolEvidenceRequired || in.ToolCount > 0
	requiresEvidence := planRequired || in.FileCount > 0 || requiredArtifacts > 0
	complexity := ComplexitySimple
	if planRequired {
		complexity = ComplexityComplex
	} else if requiresEvidence {
		complexity = ComplexityNormal
	}

	var checks []CheckKind
	if requiresEvidence {
		checks = append(checks, CheckEvidence)
	}
	if planRequired {
		checks = append(checks, CheckPlanTerminal)
	}
	if requiredArtifacts > 0 {
		checks = append(checks, CheckArtifact)
	}
	if in.ResponseFormatEnabled {
		checks = append(checks, CheckJSON)
	}

	return TaskContract{
		Objective:             objective,
		Complexity:            complexity,
		OutputMode:            outputMode,
		ResponseFormat:        strings.TrimSpace(in.ResponseFormat),
		RequiredArtifactCount: requiredArtifacts,
		RequiredChecks:        checks,
		RiskLevel:             RiskLevelLow,
		RequirePlan:           planRequired,
		RequireEvidence:       requiresEvidence,
		RequireToolEvidence:   toolEvidenceRequired,
		RequireFinalAnswer:    true,
		RequirePlanTerminal:   planRequired,
		MaxCorrectionAttempts: 2,
	}
}

func shouldRequirePlan(in ContractInput) bool {
	if in.SubAgent {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(in.Objective))
	if msg == "" {
		return false
	}
	if hasAny(msg,
		"规划", "执行计划", "制定计划", "分步骤", "完整流程", "完整重构", "重构", "实现", "开发", "修复", "排查", "定位",
		"验证", "运行测试", "补测试", "测试通过", "部署", "提测", "创建 mr", "建 mr", "提交 mr", "review", "refactor", "implement",
		"fix", "debug", "investigate", "run tests", "add tests", "verify", "deploy", "pull request", "merge request",
	) {
		return true
	}
	if in.FileCount > 0 && hasAny(msg, "修改", "处理", "分析", "生成", "转换", "整理") {
		return true
	}
	if in.ToolCount > 0 && len([]rune(msg)) >= 120 && hasAny(msg, "并", "然后", "再", "以及", "同时", "and", "then") {
		return true
	}
	return shouldRequirePlanByProfile(in.AgentTaskProfile, msg)
}

func InferAgentTaskProfile(systemPrompt string) AgentTaskProfile {
	text := strings.ToLower(strings.TrimSpace(systemPrompt))
	if text == "" {
		return AgentTaskProfile{}
	}
	return AgentTaskProfile{
		HasMultiStepWorkflow: hasAny(text,
			"多步骤", "分步骤", "完整流程", "执行流程", "workflow", "step-by-step", "step by step",
			"先", "然后", "再", "最后",
		),
		RequiresValidation: hasAny(text,
			"校验", "验证", "核验", "检查结果", "质量检查", "自检", "validate", "verify", "check",
		),
		RequiresFileOutput: hasFileDeliverableIntent(text),
		RequiresDataAnalysis: hasAny(text,
			"数据分析", "分析数据", "行情分析", "报告", "汇总", "统计", "打分", "排序", "筛选",
			"data analysis", "analyze data", "report",
		),
		RequiresToolOrData: hasAny(text,
			"工具", "调用", "数据源", "知识库", "api", "sql", "查询", "检索", "tool", "data source",
		),
	}
}

func shouldRequirePlanByProfile(profile AgentTaskProfile, msg string) bool {
	if !hasProfileComplexity(profile) || !hasWorkIntent(msg) {
		return false
	}
	if profile.RequiresFileOutput || profile.RequiresDataAnalysis {
		return true
	}
	return profile.HasMultiStepWorkflow && (profile.RequiresValidation || profile.RequiresToolOrData)
}

type PlanTaskTemplate struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Note  string `json:"note,omitempty"`
}

func InitialPlanTemplate(contract TaskContract, nextTool string) []PlanTaskTemplate {
	if !contract.RequirePlan || contract.Complexity == ComplexitySimple {
		return nil
	}
	toolNote := "请根据实际执行需要细化或更新计划。"
	if strings.TrimSpace(nextTool) != "" {
		toolNote = "请更新计划后继续执行 " + strings.TrimSpace(nextTool)
	}
	tasks := []PlanTaskTemplate{{
		ID:    "understand",
		Title: "理解目标和依赖信息",
	}, {
		ID:    "execute",
		Title: "执行必要工具并收集证据",
		Note:  toolNote,
	}}
	if contract.RequiresValidationStep() {
		tasks = append(tasks, PlanTaskTemplate{
			ID:    "validate",
			Title: "校验证据和输出要求",
		})
	}
	tasks = append(tasks, PlanTaskTemplate{
		ID:    "deliver",
		Title: "汇总结果并交付最终答复",
	})
	return tasks
}

func (c TaskContract) RequiresValidationStep() bool {
	if c.RequireEvidence || c.RequiredArtifactCount > 0 || c.OutputMode == OutputModeJSON || c.OutputMode == OutputModeFile || c.OutputMode == OutputModeMixed {
		return true
	}
	for _, check := range c.RequiredChecks {
		if check == CheckEvidence || check == CheckArtifact || check == CheckJSON || check == CheckPlanTerminal {
			return true
		}
	}
	return false
}

func hasProfileComplexity(profile AgentTaskProfile) bool {
	return profile.HasMultiStepWorkflow ||
		profile.RequiresValidation ||
		profile.RequiresFileOutput ||
		profile.RequiresDataAnalysis ||
		profile.RequiresToolOrData
}

func hasWorkIntent(msg string) bool {
	return hasAny(msg,
		"开始", "继续", "执行", "处理", "分析", "生成", "整理", "帮我", "按要求", "按上面", "按这个",
		"修复", "重构", "排查", "提测", "跑一下", "做一下", "start", "continue", "run", "execute", "process",
		"analyze", "generate",
	)
}

func hasFileDeliverableIntent(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return false
	}
	for _, marker := range []string{
		".md", ".html", ".xlsx", ".csv", ".docx", ".pdf", ".txt",
		"保存到", "导出", "生成文件", "输出文件", "下载文件", "报告文件", "html报告", "html 报告",
		"save as", "export", "write file",
	} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

func hasAny(s string, markers ...string) bool {
	for _, marker := range markers {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}
