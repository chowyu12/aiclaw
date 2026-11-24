package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/tools"
)

func browserWorkflowTools() []browserToolSpec {
	conditionFields := map[string]any{
		"kind":       map[string]any{"type": "string", "enum": []string{"visible", "enabled", "text_contains", "url_contains"}},
		"selector":   map[string]any{"type": "string", "maxLength": 300, "description": "CSS selector, required for visible/enabled and optional for text_contains"},
		"value":      map[string]any{"type": "string", "maxLength": 500, "description": "Required literal substring for text_contains or url_contains"},
		"timeout_ms": map[string]any{"type": "integer", "minimum": 100, "maximum": 8000, "description": "Maximum wait in milliseconds (1000 for standalone checks, 3000 for batch expect). Extend only when a known update takes longer."},
	}
	conditionSchema := map[string]any{"type": "object", "properties": conditionFields, "required": []string{"kind"}, "additionalProperties": false}
	steps := map[string]any{
		"type": "array", "minItems": 1, "maxItems": 8,
		"items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"action", "index"}, "properties": map[string]any{
			"action": map[string]any{"type": "string", "enum": []string{"type", "select", "click"}},
			"index":  map[string]any{"type": "integer", "minimum": 1, "maximum": 150},
			"text":   map[string]any{"type": "string", "maxLength": 2048, "description": "Replacement text for type"},
			"value":  map[string]any{"type": "string", "maxLength": 256, "description": "Option label or value for select"},
		}},
	}
	return []browserToolSpec{
		{
			name: "browser_batch", effect: tools.EffectWrite,
			description: "Group two or more known type/select/click steps on the same snapshot (up to eight), or use one action with an expected result. Put the final submit or navigation click last. The batch waits for the original targets and stops if they change. Prefer expect for a known result and use the returned snapshot, visible text, and matched evidence directly. Once Verification matched and the result answers the task, reply without another wait, verify, snapshot, extract, or screenshot. Never retry a partially completed batch blindly.",
			schema:      schemaOf(map[string]any{"steps": steps, "expect": conditionSchema}, "steps"),
			build:       buildBrowserBatch,
		},
		{
			name: "browser_wait", effect: tools.EffectRead,
			description: "Wait only for a known asynchronous change still missing from the last result. Match a known CSS selector, visible text, or URL substring; avoid guessed selectors and URLs. Navigation actions and batches already wait for their own results. Use the returned snapshot when the condition matches; on timeout inspect the returned state instead of repeating the wait.",
			schema:      schemaOf(conditionFields, "kind"),
			build:       buildBrowserCondition(protocol.BrowserWait),
		},
		{
			name: "browser_verify", effect: tools.EffectRead,
			description: "Check a specific observable result that is still missing from prior tool output. If a batch already matched expect or the latest tool result visibly answers the task, use that evidence and reply. Match known visible text, a visible/enabled CSS selector, or a URL substring; returns the observed evidence and snapshot.",
			schema:      schemaOf(conditionFields, "kind"),
			build:       buildBrowserCondition(protocol.BrowserVerify),
		},
	}
}

func buildBrowserBatch(raw json.RawMessage) (protocol.BrowserRequestParams, error) {
	var args struct {
		Steps  []protocol.BrowserStep     `json:"steps"`
		Expect *protocol.BrowserCondition `json:"expect"`
	}
	if err := decodeBrowserWorkflow(raw, &args); err != nil {
		return protocol.BrowserRequestParams{}, err
	}
	if len(args.Steps) < 1 || len(args.Steps) > 8 {
		return protocol.BrowserRequestParams{}, errors.New("browser_batch requires 1–8 steps")
	}
	for _, step := range args.Steps {
		if step.Index < 1 || step.Index > 150 {
			return protocol.BrowserRequestParams{}, errors.New("batch step index must be an element number from the latest snapshot")
		}
		switch step.Action {
		case protocol.BrowserClick:
			if step.Text != nil || step.Value != "" {
				return protocol.BrowserRequestParams{}, errors.New("click takes only an index")
			}
		case protocol.BrowserType:
			if step.Text == nil || len([]rune(*step.Text)) > 2048 || step.Value != "" {
				return protocol.BrowserRequestParams{}, errors.New("type requires text of at most 2048 characters")
			}
		case protocol.BrowserSelect:
			if strings.TrimSpace(step.Value) == "" || len([]rune(step.Value)) > 256 || step.Text != nil {
				return protocol.BrowserRequestParams{}, errors.New("select requires a value of at most 256 characters")
			}
		default:
			return protocol.BrowserRequestParams{}, errors.New("batch only supports click, type, and select")
		}
	}
	if args.Expect != nil {
		if err := validateBrowserCondition(*args.Expect); err != nil {
			return protocol.BrowserRequestParams{}, err
		}
	}
	return protocol.BrowserRequestParams{Action: protocol.BrowserBatch, Steps: args.Steps, Condition: args.Expect}, nil
}

func buildBrowserCondition(action protocol.BrowserAction) func(json.RawMessage) (protocol.BrowserRequestParams, error) {
	return func(raw json.RawMessage) (protocol.BrowserRequestParams, error) {
		var condition protocol.BrowserCondition
		if err := decodeBrowserWorkflow(raw, &condition); err != nil {
			return protocol.BrowserRequestParams{}, err
		}
		if err := validateBrowserCondition(condition); err != nil {
			return protocol.BrowserRequestParams{}, err
		}
		return protocol.BrowserRequestParams{Action: action, Condition: &condition}, nil
	}
}

func decodeBrowserWorkflow(raw json.RawMessage, target any) error {
	if !json.Valid(raw) {
		return errors.New("invalid browser workflow JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func validateBrowserCondition(condition protocol.BrowserCondition) error {
	if (condition.TimeoutMS != 0 && condition.TimeoutMS < 100) || condition.TimeoutMS > 8000 ||
		len([]rune(condition.Selector)) > 300 || len([]rune(condition.Value)) > 500 {
		return errors.New("browser condition exceeds its bounds")
	}
	selector, value := strings.TrimSpace(condition.Selector), strings.TrimSpace(condition.Value)
	switch condition.Kind {
	case "visible", "enabled":
		if selector != "" && value == "" {
			return nil
		}
	case "url_contains":
		if selector == "" && value != "" {
			return nil
		}
	case "text_contains":
		if value != "" {
			return nil
		}
	}
	return errors.New("browser condition requires a selector for visible/enabled or a value for text_contains/url_contains")
}
