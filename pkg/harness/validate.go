package harness

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	SeverityCritical = "critical"
	SeverityWarning  = "warning"
)

type ValidationStage string

const (
	StagePreTool  ValidationStage = "pre_tool"
	StagePostTool ValidationStage = "post_tool"
	StagePreFinal ValidationStage = "pre_final"
	StagePreSave  ValidationStage = "pre_save"
)

type Candidate struct {
	Content  string
	ToolName string
	ToolArgs string
}

type Violation struct {
	Code           string `json:"code"`
	Message        string `json:"message"`
	Severity       string `json:"severity"`
	RequiredAction string `json:"required_action,omitempty"`
}

type ValidationResult struct {
	Stage      ValidationStage
	Allowed    bool
	Violations []Violation
	Contract   TaskContract
	Evidence   EvidenceLedger
}

func (r ValidationResult) Reasons() []string {
	if len(r.Violations) == 0 {
		return nil
	}
	reasons := make([]string, 0, len(r.Violations))
	for _, v := range r.Violations {
		if msg := strings.TrimSpace(v.Message); msg != "" {
			reasons = append(reasons, msg)
		}
	}
	return reasons
}

func (r ValidationResult) RequiredActions() []string {
	if len(r.Violations) == 0 {
		return nil
	}
	actions := make([]string, 0, len(r.Violations))
	seen := make(map[string]bool, len(r.Violations))
	for _, v := range r.Violations {
		action := strings.TrimSpace(v.RequiredAction)
		if action == "" || seen[action] {
			continue
		}
		seen[action] = true
		actions = append(actions, action)
	}
	return actions
}

func (r ValidationResult) ViolationCodes() []string {
	if len(r.Violations) == 0 {
		return nil
	}
	codes := make([]string, 0, len(r.Violations))
	for _, v := range r.Violations {
		if code := strings.TrimSpace(v.Code); code != "" {
			codes = append(codes, code)
		}
	}
	return codes
}

type Validator interface {
	Validate(stage ValidationStage, contract TaskContract, evidence EvidenceLedger, candidate Candidate) []Violation
}

type ValidatorFunc func(stage ValidationStage, contract TaskContract, evidence EvidenceLedger, candidate Candidate) []Violation

func (fn ValidatorFunc) Validate(stage ValidationStage, contract TaskContract, evidence EvidenceLedger, candidate Candidate) []Violation {
	return fn(stage, contract, evidence, candidate)
}

func DefaultValidators() []Validator {
	return []Validator{
		ValidatorFunc(validateToolPolicy),
		ValidatorFunc(validateNonEmptyFinal),
		ValidatorFunc(validateFinalAnswerCompletion),
		ValidatorFunc(validatePlanTerminal),
		ValidatorFunc(validateHarnessInitialEvidence),
		ValidatorFunc(validateFailedToolEvidence),
		ValidatorFunc(validateRequiredArtifacts),
		ValidatorFunc(validateJSONOutput),
	}
}

func validateToolPolicy(stage ValidationStage, contract TaskContract, _ EvidenceLedger, candidate Candidate) []Violation {
	if stage != StagePreTool {
		return nil
	}
	toolName := strings.TrimSpace(candidate.ToolName)
	if toolName == "" {
		return nil
	}
	if stringInSlice(toolName, contract.ToolPolicy.Blocked) {
		return []Violation{{
			Code:           "tool_blocked_by_contract",
			Message:        "工具被 Harness 契约禁止调用: " + toolName,
			Severity:       SeverityCritical,
			RequiredAction: "选择契约允许的工具，或停止执行并说明权限/风险限制",
		}}
	}
	if len(contract.ToolPolicy.Allowed) > 0 && !stringInSlice(toolName, contract.ToolPolicy.Allowed) {
		return []Violation{{
			Code:           "tool_not_allowed_by_contract",
			Message:        "工具不在 Harness 契约允许列表中: " + toolName,
			Severity:       SeverityCritical,
			RequiredAction: "选择契约允许的工具，或停止执行并说明权限/风险限制",
		}}
	}
	return nil
}

func validateNonEmptyFinal(stage ValidationStage, contract TaskContract, _ EvidenceLedger, candidate Candidate) []Violation {
	if !isFinalStage(stage) || !contract.RequireFinalAnswer || strings.TrimSpace(candidate.Content) != "" {
		return nil
	}
	return []Violation{{
		Code:           "final_answer_empty",
		Message:        "最终答复为空",
		Severity:       SeverityCritical,
		RequiredAction: "基于已完成的工具结果直接给出面向用户的最终答复",
	}}
}

func validateFinalAnswerCompletion(stage ValidationStage, contract TaskContract, _ EvidenceLedger, candidate Candidate) []Violation {
	if !isFinalStage(stage) || !contract.RequireFinalAnswer || !candidateLooksLikeProgressOnly(candidate.Content) {
		return nil
	}
	return []Violation{{
		Code:           "final_answer_incomplete_progress",
		Message:        "最终答复仍是执行进度说明，没有交付结果或明确阻塞原因",
		Severity:       SeverityCritical,
		RequiredAction: "继续完成承诺的查询、整理、生成或校验步骤；完成后输出真实结果、文件链接，或明确说明阻塞原因",
	}}
}

func validatePlanTerminal(stage ValidationStage, contract TaskContract, evidence EvidenceLedger, _ Candidate) []Violation {
	if !isFinalStage(stage) || !contract.RequirePlanTerminal || PlanReadyForFinalAnswer(evidence.Plan) {
		return nil
	}
	return []Violation{{
		Code:           "plan_not_terminal",
		Message:        "执行计划仍有未完成项",
		Severity:       SeverityCritical,
		RequiredAction: "继续执行未完成任务，或调用 plan 将任务标记为 completed/skipped/blocked/failed",
	}}
}

func validateHarnessInitialEvidence(stage ValidationStage, contract TaskContract, evidence EvidenceLedger, _ Candidate) []Violation {
	if !isFinalStage(stage) || !contract.RequirePlanTerminal {
		return nil
	}
	if evidence.PlanSource != PlanSourceHarness || PlanTerminal(evidence.Plan) {
		return nil
	}
	if evidence.HasSuccessfulEvidence() {
		return nil
	}
	return []Violation{{
		Code:           "harness_plan_unproven",
		Message:        "计划仍停留在初始状态，且缺少业务工具或产物证据",
		Severity:       SeverityCritical,
		RequiredAction: "调用必要业务工具获取证据，或用 plan 明确推进执行计划",
	}}
}

func validateFailedToolEvidence(stage ValidationStage, contract TaskContract, evidence EvidenceLedger, _ Candidate) []Violation {
	failed := evidence.FailedToolEvents()
	if len(failed) == 0 {
		return nil
	}
	if stage == StagePostTool {
		return []Violation{{
			Code:           "tool_failed",
			Message:        fmt.Sprintf("本轮有 %d 个工具失败", len(failed)),
			Severity:       SeverityWarning,
			RequiredAction: "后续最终答复前需要补齐失败工具对应的证据，或明确说明无法完成的原因",
		}}
	}
	if !isFinalStage(stage) || !contract.RequireEvidence || evidence.HasSuccessfulEvidence() {
		return nil
	}
	return []Violation{{
		Code:           "no_successful_evidence_after_tool_failure",
		Message:        "工具调用失败后没有可用的成功证据",
		Severity:       SeverityCritical,
		RequiredAction: "重新调用可用工具获取证据；如果确实无法继续，说明阻塞原因而不是声称已完成",
	}}
}

func validateRequiredArtifacts(stage ValidationStage, contract TaskContract, evidence EvidenceLedger, candidate Candidate) []Violation {
	if !isFinalStage(stage) {
		return nil
	}
	required := contract.RequiredArtifactCount
	if required == 0 && candidateClaimsFile(candidate.Content) {
		required = 1
	}
	if required > 0 && evidence.ActualArtifactCount() < required {
		return []Violation{{
			Code:           "required_artifact_missing",
			Message:        "最终答复声明了文件交付，但执行证据中没有可交付文件",
			Severity:       SeverityCritical,
			RequiredAction: "调用文件生成工具产出文件，或修正最终答复不要声称文件已生成",
		}}
	}
	if missing := evidence.ArtifactsMissingReference(); len(missing) > 0 {
		return []Violation{{
			Code:           "artifact_reference_missing",
			Message:        fmt.Sprintf("%d 个文件产物缺少可引用标识", len(missing)),
			Severity:       SeverityCritical,
			RequiredAction: "确保文件产物登记到 workspace 或文件存储，并带有 UUID、路径或 URL",
		}}
	}
	if stage == StagePreSave && required > 0 && !candidateContainsAllArtifactRefs(candidate.Content, evidence.Artifacts) {
		return []Violation{{
			Code:           "file_output_ref_missing",
			Message:        "文件交付任务的最终答复未包含文件引用",
			Severity:       SeverityCritical,
			RequiredAction: "最终答复必须包含生成文件的可访问链接或文件引用",
		}}
	}
	return nil
}

func validateJSONOutput(stage ValidationStage, contract TaskContract, _ EvidenceLedger, candidate Candidate) []Violation {
	if stage != StagePreSave || contract.OutputMode != OutputModeJSON {
		return nil
	}
	content := strings.TrimSpace(candidate.Content)
	if content == "" {
		return nil
	}
	var v any
	dec := json.NewDecoder(strings.NewReader(content))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return []Violation{{
			Code:           "json_output_invalid",
			Message:        "结构化输出不是合法 JSON",
			Severity:       SeverityCritical,
			RequiredAction: "重新整理最终答复，只输出合法 JSON",
		}}
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return []Violation{{
			Code:           "json_output_multiple_values",
			Message:        "结构化输出包含多个 JSON 值",
			Severity:       SeverityCritical,
			RequiredAction: "重新整理最终答复，只输出一个 JSON 值",
		}}
	}
	return nil
}

func PlanTerminal(plan *PlanSnapshot) bool {
	if plan == nil || len(plan.Items) == 0 {
		return false
	}
	for _, item := range plan.Items {
		if planItemTerminal(item.Status) {
			continue
		}
		return false
	}
	return true
}

func PlanReadyForFinalAnswer(plan *PlanSnapshot) bool {
	if plan == nil || len(plan.Items) == 0 {
		return false
	}
	for i, item := range plan.Items {
		if planItemTerminal(item.Status) {
			continue
		}
		return i == len(plan.Items)-1 &&
			planItemOpen(item.Status) &&
			IsFinalDeliveryPlanItem(item.ID, item.Title)
	}
	return true
}

func IsFinalDeliveryPlanItem(id, title string) bool {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "deliver" || id == "final" || id == "final_answer" || id == "final-answer" {
		return true
	}
	title = strings.ToLower(strings.TrimSpace(title))
	for _, marker := range []string{
		"交付", "最终答复", "最终回答", "最终回复", "汇总结果", "输出结果", "输出最终",
		"deliver", "final answer", "final response",
	} {
		if strings.Contains(title, marker) {
			return true
		}
	}
	return false
}

func planItemTerminal(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "skipped", "blocked", "failed":
		return true
	default:
		return false
	}
}

func planItemOpen(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pending", "running", "in_progress":
		return true
	default:
		return false
	}
}

func isFinalStage(stage ValidationStage) bool {
	return stage == StagePreFinal || stage == StagePreSave
}

func hasCriticalViolation(violations []Violation) bool {
	for _, v := range violations {
		if v.Severity == SeverityCritical || v.Severity == "" {
			return true
		}
	}
	return false
}

func candidateClaimsFile(content string) bool {
	s := strings.ToLower(strings.TrimSpace(content))
	if s == "" {
		return false
	}
	if strings.Contains(s, "已保存") || strings.Contains(s, "已生成") || strings.Contains(s, "download") {
		if strings.Contains(s, "文件") {
			return true
		}
		for _, ext := range []string{".md", ".html", ".xlsx", ".csv", ".docx", ".pdf", ".txt"} {
			if strings.Contains(s, ext) {
				return true
			}
		}
	}
	return false
}

func candidateContainsAllArtifactRefs(content string, artifacts []ArtifactEvidence) bool {
	content = strings.TrimSpace(content)
	if content == "" || len(artifacts) == 0 {
		return false
	}
	for _, art := range artifacts {
		if artifactRefInContent(content, art) {
			continue
		}
		return false
	}
	return true
}

func artifactRefInContent(content string, art ArtifactEvidence) bool {
	for _, ref := range []string{art.URL, art.WorkspacePath, art.StoragePath, art.UUID, art.Filename} {
		ref = strings.TrimSpace(ref)
		if ref != "" && (strings.Contains(content, ref) || strings.Contains(content, escapeMarkdownURLForValidation(ref))) {
			return true
		}
	}
	return false
}

func escapeMarkdownURLForValidation(s string) string {
	return strings.NewReplacer(" ", "%20", "(", "%28", ")", "%29").Replace(s)
}

func candidateLooksLikeProgressOnly(content string) bool {
	s := strings.ToLower(strings.TrimSpace(content))
	if s == "" {
		return false
	}
	if json.Valid([]byte(s)) || containsAnySubstring(s, []string{
		"无法继续", "无法完成", "不能继续", "不能完成", "权限不足", "缺少权限", "缺少必要", "被拒绝",
		"blocked", "failed", "error",
	}) {
		return false
	}
	if containsAnySubstring(s, []string{
		"请稍等", "稍等", "稍后", "马上",
		"让我直接", "让我继续", "我来继续", "我会继续", "我将继续",
		"继续执行", "继续生成", "继续整理", "继续查询", "继续分析",
		"现在正在", "正在生成报告", "正在生成文件", "正在生成 html", "正在生成html",
		"正在编写", "正在整理", "正在处理", "正在执行", "正在调用", "正在查询", "正在分析", "正在导出", "正在保存",
		"生成中", "编写中", "整理中", "处理中", "执行中",
	}) {
		return true
	}
	if !candidateHasResultCue(s) && containsAnySubstring(s, []string{
		"现在用", "现在使用", "现在开始", "现在整理", "现在生成", "现在编写", "现在处理",
		"现在执行", "现在查询", "现在分析", "现在运行", "现在导出", "现在保存", "现在汇总",
		"接下来我会", "接下来我将", "接下来会", "接下来将", "下一步我会", "下一步我将", "下一步会", "下一步将",
	}) {
		return true
	}
	return false
}

func candidateHasResultCue(s string) bool {
	return containsAnySubstring(s, []string{
		"如下", "结果如下", "结论", "概览", "摘要", "明细", "清单", "排名", "列表", "报告内容",
	})
}

func containsAnySubstring(s string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

func stringInSlice(needle string, haystack []string) bool {
	needle = strings.TrimSpace(needle)
	for _, item := range haystack {
		if strings.TrimSpace(item) == needle {
			return true
		}
	}
	return false
}
