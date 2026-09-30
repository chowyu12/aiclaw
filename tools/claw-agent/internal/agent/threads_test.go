package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

// 给模型的那份：引用说明在前（只有 id，不带内容），@标题 写成 thread:// 链接，
// 与 Codex 的 apply_task_references 同一个形状。
func TestWithReferences(t *testing.T) {
	text := withReferences("对比一下 @周报 和 @方案[草稿] 的结论", []protocol.ThreadRef{
		{ID: "s_1", Title: "周报"}, {ID: "s_2", Title: "方案[草稿]"},
	})
	for _, want := range []string{
		"## 引用的会话", `[{"threadId":"s_1"},{"threadId":"s_2"}]`, "必须先对每个被引用的会话调用 `read_thread`",
		"## 我的请求", "[@周报](thread://s_1)", `[@方案[草稿\]](thread://s_2)`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("缺 %q：\n%s", want, text)
		}
	}
	if withReferences("原话", nil) != "原话" {
		t.Error("没有引用时原样返回")
	}
}

func TestCleanReferences(t *testing.T) {
	session := bareSession(t)
	refs := []protocol.ThreadRef{{ID: "s_a", Title: strings.Repeat("长", 200)}, {ID: "s_a"}, {ID: session.ID}, {ID: "bad id!"}}
	for i := 0; i < 30; i++ {
		refs = append(refs, protocol.ThreadRef{ID: "s_" + strings.Repeat("x", i+1)})
	}
	cleaned := session.cleanReferences(refs)
	if len(cleaned) != maxReferences {
		t.Errorf("最多 %d 个：%d", maxReferences, len(cleaned))
	}
	if cleaned[0].ID != "s_a" || len([]rune(cleaned[0].Title)) != maxReferenceTitle {
		t.Errorf("第一个应当是 s_a 且标题截到 %d：%+v", maxReferenceTitle, cleaned[0])
	}
	for _, ref := range cleaned {
		if ref.ID == session.ID || ref.ID == "bad id!" {
			t.Errorf("不该留下自己或不合法的 id：%+v", ref)
		}
	}
}

// 模型收到的是带引用说明的那份，界面与存档还原的是原话加引用标签。
func TestReferencesReachModelButHistoryShowsOriginal(t *testing.T) {
	model := &fakeModel{script: []string{sseText("好")}}
	session := browserSession(t, model, protocol.ApprovalBypass, false)
	emitter := &recordingEmitter{approve: true}
	session.RunTurn(context.Background(), "t1", "照 @周报 的格式写", nil, nil, emitter, protocol.ThreadRef{ID: "s_weekly", Title: "周报"})

	sent := session.messages[1]
	if !strings.Contains(sent.Content, "[@周报](thread://s_weekly)") || !strings.Contains(sent.Content, "read_thread") {
		t.Errorf("模型应当收到引用说明：%q", sent.Content)
	}
	history := session.History()
	if len(history) == 0 || history[0].Text != "照 @周报 的格式写" || len(history[0].References) != 1 || history[0].References[0].ID != "s_weekly" {
		t.Errorf("历史应当还原原话与引用：%+v", history[0])
	}
	if !emitter.find(protocol.NotifyItemCompleted, `"references":[{"id":"s_weekly","title":"周报"}]`) {
		t.Error("实时的 userMessage 条目也要带上引用")
	}
}

// read_thread：按用户说的话切轮，新的在前，能翻页；工具输出默认不带。
func TestReadThreadResultPagesNewestFirst(t *testing.T) {
	var messages []llm.Message
	messages = append(messages, llm.Message{Role: llm.RoleSystem, Content: "系统"})
	for i := 1; i <= 7; i++ {
		n := string(rune('0' + i))
		messages = append(messages,
			llm.Message{Role: llm.RoleUser, Content: "带说明的" + n, Shown: &llm.Shown{Text: "第" + n + "轮"}},
			llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c" + n, Name: "read_file", Arguments: `{"path":"a.go"}`}}},
			llm.Message{Role: llm.RoleTool, ToolCallID: "c" + n, Content: "文件内容" + n},
			llm.Message{Role: llm.RoleUser, Content: "（截屏）", Shown: &llm.Shown{Hidden: true}},
			llm.Message{Role: llm.RoleAssistant, Content: "答" + n},
		)
	}
	result := readThreadResult(ThreadSnapshot{ID: "s_x", Title: "t", Messages: messages}, 0, 3, false, 100)
	encoded, _ := json.Marshal(result)
	text := string(encoded)
	turns := result["turns"].([]threadTurn)
	if len(turns) != 3 || turns[0].User != "第7轮" || turns[2].User != "第5轮" {
		t.Fatalf("应当新的在前取 3 轮：%+v", turns)
	}
	if result["nextCursor"] != "3" || strings.Contains(text, "文件内容") || strings.Contains(text, "截屏") {
		t.Errorf("翻页游标或内容不对：%s", text)
	}
	if !strings.Contains(text, `"summary":"答7"`) || !strings.Contains(text, `"name":"read_file"`) {
		t.Errorf("摘要与工具名要有：%s", text)
	}
	last := readThreadResult(ThreadSnapshot{Messages: messages}, 6, 3, true, 100)
	lastTurns := last["turns"].([]threadTurn)
	if len(lastTurns) != 1 || lastTurns[0].User != "第1轮" || last["nextCursor"] != nil {
		t.Errorf("最后一页：%+v %v", lastTurns, last["nextCursor"])
	}
	if out, _ := json.Marshal(last); !strings.Contains(string(out), "文件内容1") {
		t.Error("includeOutputs 时要带工具输出")
	}
}
