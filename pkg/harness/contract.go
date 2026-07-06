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
	RequireFinalAnswer    bool        `json:"require_final_answer,omitempty"`
	RequirePlanTerminal   bool        `json:"require_plan_terminal,omitempty"`
	MaxCorrectionAttempts int         `json:"max_correction_attempts,omitempty"`
}

type ContractInput struct {
	Objective             string
	PlanEnabled           bool
	ResponseFormatEnabled bool
	ResponseFormat        string
	ToolEvidenceRequired  bool
	FileCount             int
}

func BuildContract(in ContractInput) TaskContract {
	objective := strings.TrimSpace(in.Objective)
	outputMode := OutputModeText
	requiredArtifacts := 0
	if in.ResponseFormatEnabled {
		outputMode = OutputModeJSON
	} else if hasFileDeliverableIntent(objective) {
		outputMode = OutputModeFile
		requiredArtifacts = 1
	}
	if outputMode == OutputModeFile && in.FileCount > 0 {
		outputMode = OutputModeMixed
	}

	requiresEvidence := in.PlanEnabled || in.ToolEvidenceRequired || in.FileCount > 0 || requiredArtifacts > 0
	complexity := ComplexitySimple
	if in.PlanEnabled {
		complexity = ComplexityComplex
	} else if requiresEvidence {
		complexity = ComplexityNormal
	}

	var checks []CheckKind
	if requiresEvidence {
		checks = append(checks, CheckEvidence)
	}
	if in.PlanEnabled {
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
		RequirePlan:           in.PlanEnabled,
		RequireEvidence:       requiresEvidence,
		RequireFinalAnswer:    true,
		RequirePlanTerminal:   in.PlanEnabled,
		MaxCorrectionAttempts: 2,
	}
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
