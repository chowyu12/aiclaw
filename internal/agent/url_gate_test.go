package agent

import (
	"testing"

	openai "github.com/chowyu12/go-openai"
)

func TestContainsURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"plain", "please summarize the report", false},
		{"http", "see http://example.com", true},
		{"https", "fetch https://example.com/path?q=1", true},
		{"https_cn", "查看 https://baidu.com/s?wd=go 搜索", true},
		{"mixed_case", "See HTTP://Example.COM", true},
		{"no_scheme", "visit www.example.com", false},
		{"ftp_scheme", "ftp://example.com", false},
		{"bare_domain", "example.com is a site", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := containsURL(c.in); got != c.want {
				t.Errorf("containsURL(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestUserMessagesHaveURL(t *testing.T) {
	tests := []struct {
		name string
		msgs []openai.ChatCompletionMessage
		want bool
	}{
		{
			name: "nil",
			msgs: nil,
			want: false,
		},
		{
			name: "only_system_with_url",
			msgs: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleSystem, Content: "https://example.com"},
			},
			want: false,
		},
		{
			name: "user_with_url",
			msgs: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleSystem, Content: "sys"},
				{Role: openai.ChatMessageRoleUser, Content: "帮我抓一下 https://github.com/foo/bar"},
			},
			want: true,
		},
		{
			name: "user_without_url",
			msgs: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleUser, Content: "帮我查一下最新的 Go 版本"},
			},
			want: false,
		},
		{
			name: "assistant_with_url_only",
			msgs: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleUser, Content: "搜一下 Go 发布历史"},
				{Role: openai.ChatMessageRoleAssistant, Content: "结果: https://go.dev/doc/devel/release"},
			},
			want: false,
		},
		{
			name: "multi_content_url",
			msgs: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleUser, MultiContent: []openai.ChatMessagePart{
					{Type: openai.ChatMessagePartTypeText, Text: "看看这个"},
					{Type: openai.ChatMessagePartTypeText, Text: "https://example.com/post"},
				}},
			},
			want: true,
		},
		{
			name: "historical_user_url_persists",
			msgs: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleUser, Content: "读一下 https://foo.com"},
				{Role: openai.ChatMessageRoleAssistant, Content: "已读"},
				{Role: openai.ChatMessageRoleUser, Content: "再总结一下"},
			},
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := userMessagesHaveURL(tc.msgs); got != tc.want {
				t.Errorf("userMessagesHaveURL = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFilterURLGatedTools(t *testing.T) {
	mkTool := func(name string) openai.Tool {
		return openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{Name: name},
		}
	}
	tools := []openai.Tool{
		mkTool("web_fetch"),
		mkTool("session_search"),
		mkTool("todo_write"),
	}

	t.Run("has_url_keeps_all", func(t *testing.T) {
		got := filterURLGatedTools(tools, true)
		if len(got) != 3 {
			t.Fatalf("expected 3 tools, got %d", len(got))
		}
	})

	t.Run("no_url_strips_web_fetch", func(t *testing.T) {
		got := filterURLGatedTools(tools, false)
		if len(got) != 2 {
			t.Fatalf("expected 2 tools, got %d", len(got))
		}
		for _, tl := range got {
			if tl.Function != nil && tl.Function.Name == "web_fetch" {
				t.Errorf("web_fetch should be filtered out")
			}
		}
	})

	t.Run("nil_tools_safe", func(t *testing.T) {
		got := filterURLGatedTools(nil, false)
		if got != nil {
			t.Errorf("expected nil passthrough, got len=%d", len(got))
		}
	})

	t.Run("nil_function_safe", func(t *testing.T) {
		odd := []openai.Tool{{Type: openai.ToolTypeFunction, Function: nil}}
		got := filterURLGatedTools(odd, false)
		if len(got) != 1 {
			t.Errorf("expected 1 (nil Function preserved), got %d", len(got))
		}
	})

	t.Run("does_not_mutate_input", func(t *testing.T) {
		orig := []openai.Tool{mkTool("web_fetch"), mkTool("other")}
		_ = filterURLGatedTools(orig, false)
		if len(orig) != 2 || orig[0].Function.Name != "web_fetch" {
			t.Errorf("input tools were mutated")
		}
	})
}
