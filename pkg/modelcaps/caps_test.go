package modelcaps

import (
	"fmt"
	"testing"
)

func TestGetModelCaps(t *testing.T) {
	cases := []struct {
		model          string
		wantTemp       bool
		wantAlways     bool
		wantVision     bool
		wantWebSearch  bool
		wantCtxWindow  int
	}{
		// OpenAI reasoning
		{"o1", false, true, false, false, 200000},
		{"o1-mini", false, true, false, false, 200000},
		{"o3-mini", false, true, false, false, 200000},
		{"o4-mini", false, true, true, false, 200000},
		// GPT-5
		{"gpt-5", false, true, true, false, 400000},
		// GPT-4.1
		{"gpt-4.1", true, false, true, false, 1047576},
		{"gpt-4.1-mini", true, false, true, false, 1047576},
		// GPT-4o
		{"gpt-4o", true, false, true, false, 128000},
		{"gpt-4o-mini", true, false, true, false, 128000},
		// GPT-3.5
		{"gpt-3.5-turbo", true, false, false, false, 16384},
		// DeepSeek
		{"deepseek-reasoner", false, true, false, false, 128000},
		{"deepseek-chat", true, false, false, false, 128000},
		// 百炼 / Qwen
		{"qwq-32b", false, true, false, false, 131072},
		{"qwq-plus", false, true, false, false, 131072},
		{"qvq-max-latest", false, true, true, false, 131072},
		{"qwen3-235b-a22b", true, false, false, false, 131072},
		{"qwen3.6-plus", true, false, true, true, 1000000},
		{"qwen3-max", true, false, false, true, 262144},
		{"qwen3-coder-plus", true, false, false, false, 1000000},
		{"qwen3-vl-235b-a22b-instruct", true, false, true, false, 131072},
		{"qwen-plus-latest", true, false, false, true, 1000000},
		{"qwen-max-latest", true, false, false, true, 1000000},
		{"qwen-turbo-latest", true, false, false, true, 131072},
		{"qwen-long", true, false, false, false, 10000000},
		{"qwen-vl-max-latest", true, false, true, false, 131072},
		{"qwen-coder-turbo-latest", true, false, false, false, 131072},
		{"qwen2.5-vl-72b-instruct", true, false, true, false, 131072},
		{"deepseek-r1", false, true, false, false, 128000},
		{"kimi-k2-thinking", true, false, false, false, 131072},
		{"glm-5", true, false, false, false, 128000},
		{"farui-plus", true, false, false, false, 131072},
		// Claude
		{"claude-sonnet-4-20250514", true, false, true, false, 1000000},
		{"claude-3-5-sonnet-20241022", true, false, true, false, 200000},
		// Gemini
		{"gemini-2.5-pro-preview-06-05", true, false, true, true, 1048576},
		{"gemini-2.0-flash", true, false, true, true, 1048576},
		// Moonshot
		{"moonshot-v1-128k", true, false, false, false, 128000},
		// Unknown → defaults
		{"unknown-model", true, false, false, false, 128000},
	}

	for _, tc := range cases {
		caps := GetModelCaps(tc.model)
		hasTemp := !caps.NoTemperature

		ok := true
		if hasTemp != tc.wantTemp {
			t.Errorf("%s: temperature got %v want %v", tc.model, hasTemp, tc.wantTemp)
			ok = false
		}
		if caps.AlwaysThinking != tc.wantAlways {
			t.Errorf("%s: always_thinking got %v want %v", tc.model, caps.AlwaysThinking, tc.wantAlways)
			ok = false
		}
		if caps.Vision != tc.wantVision {
			t.Errorf("%s: vision got %v want %v", tc.model, caps.Vision, tc.wantVision)
			ok = false
		}
		if caps.WebSearch != tc.wantWebSearch {
			t.Errorf("%s: web_search got %v want %v", tc.model, caps.WebSearch, tc.wantWebSearch)
			ok = false
		}
		if caps.ContextWindow != tc.wantCtxWindow {
			t.Errorf("%s: context_window got %d want %d", tc.model, caps.ContextWindow, tc.wantCtxWindow)
			ok = false
		}
		if ok {
			fmt.Printf("  ✓ %-40s temp=%-5v think_always=%-5v vision=%-5v search=%-5v ctx=%d\n",
				tc.model, hasTemp, caps.AlwaysThinking, caps.Vision, caps.WebSearch, caps.ContextWindow)
		}
	}
}
