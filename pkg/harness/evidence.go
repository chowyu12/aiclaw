package harness

import "strings"

type ToolStatus string

const (
	ToolStatusSuccess ToolStatus = "success"
	ToolStatusError   ToolStatus = "error"
	ToolStatusSkipped ToolStatus = "skipped"
	ToolStatusBlocked ToolStatus = "blocked"
)

type PlanItemSnapshot struct {
	ID     string `json:"id,omitempty"`
	Title  string `json:"title,omitempty"`
	Status string `json:"status,omitempty"`
}

type PlanSnapshot struct {
	Items []PlanItemSnapshot `json:"items,omitempty"`
}

type ArtifactEvidence struct {
	UUID          string `json:"uuid,omitempty"`
	Filename      string `json:"filename,omitempty"`
	WorkspacePath string `json:"workspace_path,omitempty"`
	StoragePath   string `json:"storage_path,omitempty"`
	URL           string `json:"url,omitempty"`
	MimeType      string `json:"mime_type,omitempty"`
	Size          int64  `json:"size,omitempty"`
	SourceTool    string `json:"source_tool,omitempty"`
}

type ToolEvent struct {
	CallID            string             `json:"call_id,omitempty"`
	ToolName          string             `json:"tool_name"`
	ArgsSummary       string             `json:"args_summary,omitempty"`
	OutputSummary     string             `json:"output_summary,omitempty"`
	Status            ToolStatus         `json:"status"`
	Error             string             `json:"error,omitempty"`
	Files             []ArtifactEvidence `json:"files,omitempty"`
	DurationMs        int                `json:"duration_ms,omitempty"`
	RelatedPlanItemID string             `json:"related_plan_item_id,omitempty"`
}

type ValidationEvent struct {
	Stage      ValidationStage `json:"stage"`
	Allowed    bool            `json:"allowed"`
	Violations []Violation     `json:"violations,omitempty"`
}

type CorrectionEvent struct {
	Attempt         int               `json:"attempt"`
	Outcome         CorrectionOutcome `json:"outcome"`
	ViolationCodes  []string          `json:"violation_codes,omitempty"`
	RequiredActions []string          `json:"required_actions,omitempty"`
}

type EvidenceLedger struct {
	ExecutionTools   []string           `json:"execution_tools,omitempty"`
	ArtifactCount    int                `json:"artifact_count,omitempty"`
	Plan             *PlanSnapshot      `json:"plan,omitempty"`
	PlanSource       string             `json:"plan_source,omitempty"`
	ToolEvents       []ToolEvent        `json:"tool_events,omitempty"`
	Artifacts        []ArtifactEvidence `json:"artifacts,omitempty"`
	ValidationEvents []ValidationEvent  `json:"validation_events,omitempty"`
	Corrections      []CorrectionEvent  `json:"corrections,omitempty"`
}

func (e EvidenceLedger) ActualArtifactCount() int {
	if len(e.Artifacts) > 0 {
		return len(e.Artifacts)
	}
	return e.ArtifactCount
}

func (e EvidenceLedger) SuccessfulToolEvents() []ToolEvent {
	if len(e.ToolEvents) == 0 {
		return nil
	}
	out := make([]ToolEvent, 0, len(e.ToolEvents))
	for _, ev := range e.ToolEvents {
		if ev.Status == ToolStatusSuccess {
			out = append(out, ev)
		}
	}
	return out
}

func (e EvidenceLedger) FailedToolEvents() []ToolEvent {
	if len(e.ToolEvents) == 0 {
		return nil
	}
	out := make([]ToolEvent, 0, len(e.ToolEvents))
	for _, ev := range e.ToolEvents {
		if ev.Status == ToolStatusError || ev.Status == ToolStatusBlocked {
			out = append(out, ev)
		}
	}
	return out
}

func (e EvidenceLedger) HasSuccessfulEvidence() bool {
	return len(e.SuccessfulToolEvents()) > 0 || e.ActualArtifactCount() > 0
}

func (e EvidenceLedger) ArtifactsMissingReference() []ArtifactEvidence {
	if len(e.Artifacts) == 0 {
		return nil
	}
	var missing []ArtifactEvidence
	for _, art := range e.Artifacts {
		if strings.TrimSpace(art.URL) == "" &&
			strings.TrimSpace(art.WorkspacePath) == "" &&
			strings.TrimSpace(art.StoragePath) == "" &&
			strings.TrimSpace(art.UUID) == "" {
			missing = append(missing, art)
		}
	}
	return missing
}
