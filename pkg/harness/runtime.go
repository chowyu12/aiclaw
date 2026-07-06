package harness

import "strings"

type Runtime struct {
	Contract   TaskContract
	Evidence   EvidenceLedger
	Validators []Validator
}

func NewRuntime(contract TaskContract, evidence EvidenceLedger, validators ...Validator) Runtime {
	if !contract.RequireFinalAnswer {
		contract.RequireFinalAnswer = true
	}
	if contract.Complexity == "" {
		contract.Complexity = ComplexitySimple
	}
	if contract.OutputMode == "" {
		contract.OutputMode = OutputModeText
	}
	if contract.RiskLevel == "" {
		contract.RiskLevel = RiskLevelLow
	}
	if contract.MaxCorrectionAttempts <= 0 {
		contract.MaxCorrectionAttempts = 2
	}
	if len(validators) == 0 {
		validators = DefaultValidators()
	}
	return Runtime{
		Contract:   contract,
		Evidence:   evidence,
		Validators: validators,
	}
}

func (r Runtime) BeforeTool(toolName, toolArgs string) ValidationResult {
	return r.ValidateStage(StagePreTool, Candidate{
		ToolName: strings.TrimSpace(toolName),
		ToolArgs: strings.TrimSpace(toolArgs),
	})
}

func (r Runtime) AfterTool() ValidationResult {
	return r.ValidateStage(StagePostTool, Candidate{})
}

func (r Runtime) FinalGate(content string) ValidationResult {
	return r.ValidateStage(StagePreFinal, Candidate{Content: strings.TrimSpace(content)})
}

func (r Runtime) BeforeSave(content string) ValidationResult {
	return r.ValidateStage(StagePreSave, Candidate{Content: strings.TrimSpace(content)})
}

func (r Runtime) ValidateStage(stage ValidationStage, candidate Candidate) ValidationResult {
	candidate.Content = strings.TrimSpace(candidate.Content)
	var violations []Violation
	for _, validator := range r.Validators {
		if validator == nil {
			continue
		}
		violations = append(violations, validator.Validate(stage, r.Contract, r.Evidence, candidate)...)
	}
	return ValidationResult{
		Stage:      stage,
		Allowed:    !hasCriticalViolation(violations),
		Violations: violations,
		Contract:   r.Contract,
		Evidence:   r.Evidence,
	}
}
