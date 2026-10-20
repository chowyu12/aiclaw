package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

func TestCodeModeOperationSummary(t *testing.T) {
	for _, tc := range []struct{ name, args, want string }{
		{"title", `{"title":"确认当前时间","code":"const qs = []; text(qs)"}`, "确认当前时间"},
		{"multiline", `{"title":"  联网检索 GDP 数据\n其他说明","code":"text(1)"}`, "联网检索 GDP 数据"},
		{"legacy search", `{"code":"const qs = ['电池参数']; text(await tools.web_search({query: qs[0]}));"}`, "联网搜索"},
		{"search query", `{"code":"text(await tools.web_search__web_search({query: '2026年前三季度GDP数据'}));"}`, "联网搜索：2026年前三季度GDP数据"},
		{"command reason", `{"code":"const cmd = 'date'; text(await tools.run_command({command: cmd, reason: '确认当前时间'}));"}`, "确认当前时间"},
		{"command", `{"code":"text(await tools.run_command({command: 'git status --short'}));"}`, "执行命令：git status --short"},
		{"dynamic command", `{"code":"text(await tools.run_command({command: cmd}));"}`, "执行命令"},
		{"multiple", `{"title":"  ","code":"await tools.read_file({path: 'a'}); await tools.read_file({path: 'b'}); await tools.write_file({path: 'c', content: ''})"}`, "读取文件：a；写入文件：c"},
		{"unrelated argument", `{"code":"await tools.run_command({command: cmd}); await tools.web_search({query: 'GDP'});"}`, "执行命令；联网搜索：GDP"},
		{"unrelated object", `{"code":"await tools.run_command({command: cmd}); const note = {reason: '其他操作'};"}`, "执行命令"},
		{"dynamic prefix", `{"code":"await tools.web_search({query: 'GDP ' + year});"}`, "联网搜索"},
		{"quoted key", `{"code":"await tools.read_file({\"path\": \"report.md\"});"}`, "读取文件：report.md"},
		{"escaped query", `{"code":"await tools.web_search({query: 'GDP \\'growth\\' \\u6570\\u636e'});"}`, "联网搜索：GDP 'growth' 数据"},
		{"template", "{\"code\":\"await tools.web_search({query: `GDP data`});\"}", "联网搜索：GDP data"},
		{"dynamic template", "{\"code\":\"await tools.web_search({query: `GDP ${year}`});\"}", "联网搜索"},
		{"unknown tool", `{"code":"await tools.custom_query({keyword: query});"}`, "调用 custom_query"},
		{"invalid title", `{"title":42,"code":"text(1)"}`, "运行代码脚本"},
		{"no calls", `{"code":"const P = '/bin'; text(P)"}`, "运行代码脚本"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := summarizeCall(llm.ToolCall{Name: "exec", Arguments: tc.args}); got != tc.want {
				t.Fatalf("summary = %q, want %q", got, tc.want)
			}
		})
	}
	args, _ := json.Marshal(map[string]string{"title": strings.Repeat("查", 120), "code": "text(1)"})
	if got := summarizeCall(llm.ToolCall{Name: "exec", Arguments: string(args)}); got != strings.Repeat("查", 100)+"…" {
		t.Fatalf("long title not shortened: %q", got)
	}
}

func TestCodeModeOperationTitleLiveAndRestored(t *testing.T) {
	for _, title := range []string{"读取 SQL 并检查字段", ""} {
		t.Run(title, func(t *testing.T) {
			input := map[string]string{"code": "const setup = 'review.sql'; text(await tools.read_file({path: 'review.sql'}));"}
			if title != "" {
				input["title"] = title
			}
			args, _ := json.Marshal(input)
			model := &fakeModel{script: []string{sseToolCall("exec1", "exec", string(args)), sseText("检查完成")}}
			s := newTestSession(t, model, protocol.ApprovalOnWrite)
			if err := os.WriteFile(filepath.Join(s.config.Workdir, "review.sql"), []byte("select id from users"), 0600); err != nil {
				t.Fatal(err)
			}
			s.config.CodeMode = true
			s.installCodeMode()
			tool, _ := s.registry.Get("exec")
			var schema struct {
				Properties map[string]json.RawMessage `json:"properties"`
				Required   []string                   `json:"required"`
			}
			if err := json.Unmarshal(tool.Schema, &schema); err != nil || schema.Properties["title"] == nil || !slices.Contains(schema.Required, "title") {
				t.Fatalf("exec must require an operation title: %s", tool.Schema)
			}
			want := title
			if want == "" {
				want = "读取文件：review.sql"
			}
			ctx := context.Background()
			emitter := &recordingEmitter{}
			s.RunTurn(ctx, "turn", "检查 SQL", nil, nil, emitter)
			seen := 0
			for _, event := range emitter.events {
				method, raw, _ := strings.Cut(event, " ")
				if method != protocol.NotifyItemStarted && method != protocol.NotifyItemCompleted {
					continue
				}
				var notification protocol.ItemNotification
				if err := json.Unmarshal([]byte(raw), &notification); err != nil {
					t.Fatal(err)
				}
				item := notification.Item
				if item.ToolName != "exec" {
					continue
				}
				seen++
				if item.Summary != want || item.ToolArgs != string(args) {
					t.Fatalf("live item lost title or script: %+v", item)
				}
				if method == protocol.NotifyItemCompleted && (item.ToolFailed || !strings.Contains(item.ToolResult, "select id from users")) {
					t.Fatalf("script did not run normally: %+v", item)
				}
			}
			if seen != 2 || model.calls != 2 {
				t.Fatalf("events = %d, model calls = %d", seen, model.calls)
			}
			db := newTestStore(t)
			if err := s.Save(ctx, db); err != nil {
				t.Fatal(err)
			}
			loaded, err := Load(ctx, db, s.ID, StaticKey("sk-test"), nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(loaded.Close)
			for _, item := range loaded.History() {
				if item.ToolName == "exec" {
					if item.Summary != want || item.ToolArgs != string(args) || !strings.Contains(item.ToolResult, "select id from users") {
						t.Fatalf("restored item lost title or details: %+v", item)
					}
					return
				}
			}
			t.Fatal("missing restored exec step")
		})
	}
}
