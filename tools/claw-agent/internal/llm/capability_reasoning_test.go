package llm

import (
	"github.com/chowyu12/aiclaw/internal/model"
	"testing"
)

func TestReasoningEffortDependsOnResolvedRoute(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		protocol             model.APIProtocol
		caps                 model.ModelCapabilities
		effort               string
		preference, accepted bool
		want                 string
	}{
		{"known extended", model.APIResponses, model.ModelCapabilities{Reasoning: "supported", ReasoningEfforts: "low,high,ultra"}, "ultra", false, true, "ultra"},
		{"unknown extended", model.APIResponses, model.ModelCapabilities{}, "ultra", false, false, ""},
		{"unsupported level", model.APIResponses, model.ModelCapabilities{Reasoning: "supported", ReasoningEfforts: "low,high,ultra"}, "none", false, false, ""},
		{"foreign adapter", model.APIGemini, model.ModelCapabilities{Reasoning: "supported", ReasoningEfforts: "ultra"}, "ultra", false, false, ""},
		{"Claude max", model.APIAnthropic, model.ModelCapabilities{Reasoning: "supported", ReasoningEfforts: "high,max"}, "max", false, true, "max"},
		{"Claude xhigh", model.APIAnthropic, model.ModelCapabilities{Reasoning: "supported", ReasoningEfforts: "high,xhigh"}, "xhigh", false, true, "xhigh"},
		{"Claude unknown max", model.APIAnthropic, model.ModelCapabilities{Reasoning: "supported"}, "max", false, false, ""},
		{"Gemini thinking alone is not a level", model.APIGemini, model.ModelCapabilities{Reasoning: "supported"}, "high", true, true, ""},
		{"Gemini minimal", model.APIGemini, model.ModelCapabilities{Reasoning: "supported", ReasoningEfforts: "minimal,high"}, "minimal", false, true, "minimal"},
		{"ordinary preference", model.APIChatCompletions, model.ModelCapabilities{Reasoning: "unsupported"}, "medium", true, true, ""},
		{"ordinary explicit", model.APIChatCompletions, model.ModelCapabilities{Reasoning: "unsupported"}, "medium", false, false, ""},
		{"legacy unknown", model.APIChatCompletions, model.ModelCapabilities{}, "high", false, true, "high"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{protocol: tc.protocol, capabilities: tc.caps}
			value, err := c.reasoningEffort(tc.effort, tc.preference)
			if (err == nil) != tc.accepted || value != tc.want {
				t.Fatal(value, err)
			}
		})
	}
}
func TestTerminalMachineCodesOverrideGatewayRetryStatus(t *testing.T) {
	for _, code := range []string{"invalid_workspace", "workspace_access_denied", "access_program_denied", "inference_hook_denied", "permission_denied", "content_filter", "response_incomplete"} {
		for _, status := range []int{0, 429, 500, 503} {
			if (&Error{Status: status, Code: code, Message: "upstream unavailable"}).Retryable() {
				t.Fatal("terminal failure retried", code, status)
			}
		}
	}
}
