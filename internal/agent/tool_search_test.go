package agent

import (
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func makeDefs(tools ...struct{ name, desc string }) []openai.Tool {
	defs := make([]openai.Tool, len(tools))
	for i, t := range tools {
		defs[i] = openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        t.name,
				Description: t.desc,
			},
		}
	}
	return defs
}

func td(name, desc string) struct{ name, desc string } {
	return struct{ name, desc string }{name, desc}
}

func TestExtractKeywords(t *testing.T) {
	tests := []struct {
		query    string
		wantKeys []string
	}{
		{
			query:    "shell exec",
			wantKeys: []string{"shell", "exec"},
		},
		{
			query:    "执行命令",
			wantKeys: []string{"执行命令", "执行", "行命", "命令"},
		},
		{
			query:    "get天气",
			wantKeys: []string{"get天气", "get", "天气"},
		},
		{
			query:    "搜索",
			wantKeys: []string{"搜索"},
		},
		{
			query:    "shell 执行命令",
			wantKeys: []string{"shell", "执行命令", "执行", "行命", "命令"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			got := extractKeywords(tt.query)
			if len(got) != len(tt.wantKeys) {
				t.Fatalf("extractKeywords(%q) = %v (len %d), want %v (len %d)",
					tt.query, got, len(got), tt.wantKeys, len(tt.wantKeys))
			}
			for i := range got {
				if got[i] != tt.wantKeys[i] {
					t.Errorf("extractKeywords(%q)[%d] = %q, want %q",
						tt.query, i, got[i], tt.wantKeys[i])
				}
			}
		})
	}
}

func TestSearchTools_ChineseQuery(t *testing.T) {
	defs := makeDefs(
		td("shell_exec", "在服务器上执行Shell命令"),
		td("read_file", "读取指定文件的内容"),
		td("http_request", "发送HTTP请求到指定URL"),
		td("get_weather", "获取天气信息"),
		td("list_files", "列出目录下的文件列表"),
	)

	tests := []struct {
		name      string
		query     string
		wantNames []string
	}{
		{
			name:      "Chinese query matches description with mixed script",
			query:     "执行命令",
			wantNames: []string{"shell_exec"},
		},
		{
			name:      "Chinese query matches description exactly",
			query:     "天气",
			wantNames: []string{"get_weather"},
		},
		{
			name:      "Chinese query with shared character matches multiple tools",
			query:     "文件",
			wantNames: []string{"read_file", "list_files"},
		},
		{
			name:      "English query still works",
			query:     "shell",
			wantNames: []string{"shell_exec"},
		},
		{
			name:      "Mixed Chinese and English",
			query:     "HTTP请求",
			wantNames: []string{"http_request"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := searchTools(tt.query, defs)
			if len(results) < len(tt.wantNames) {
				t.Fatalf("searchTools(%q) returned %d results, want at least %d: %+v",
					tt.query, len(results), len(tt.wantNames), results)
			}
			gotNames := make(map[string]bool)
			for _, r := range results {
				gotNames[r.Name] = true
			}
			for _, want := range tt.wantNames {
				if !gotNames[want] {
					t.Errorf("searchTools(%q) missing expected tool %q, got %+v",
						tt.query, want, results)
				}
			}
		})
	}
}

func TestSplitCJKBoundary(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"hello", []string{"hello"}},
		{"天气", []string{"天气"}},
		{"get天气", []string{"get", "天气"}},
		{"天气info", []string{"天气", "info"}},
		{"get天气info", []string{"get", "天气", "info"}},
		{"", nil},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := splitCJKBoundary(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("splitCJKBoundary(%q) = %v, want %v", tt.input, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("splitCJKBoundary(%q)[%d] = %q, want %q",
						tt.input, i, got[i], tt.want[i])
				}
			}
		})
	}
}
