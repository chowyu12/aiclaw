package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/store"
)

// 有人能回答的档位才有 ask_user；无人值守（通道会话）没有——那里没有人能点卡片。
func TestAskUserOnlyWhereSomeoneCanAnswer(t *testing.T) {
	if !strings.Contains(strings.Join(newTestSession(t, &fakeModel{}, protocol.ApprovalOnWrite).Tools(), ","), "ask_user") {
		t.Error("桌面会话应有 ask_user")
	}
	if strings.Contains(strings.Join(newTestSession(t, &fakeModel{}, protocol.ApprovalNever).Tools(), ","), "ask_user") {
		t.Error("无人值守档位不该有 ask_user")
	}
}

// 问题与选项原样到宿主；用户的回答（选的、写的）原样回到模型，这一轮接着跑。
func TestAskUserWaitsAndHandsTheAnswerBack(t *testing.T) {
	model := &fakeModel{script: []string{
		sseToolCall("c1", "ask_user", `{"question":"删哪些？","options":[{"label":"日志"},{"label":"缓存","description":"约 2GB"},{"label":"日志"}],"multiSelect":true}`),
		sseText("好，删了日志和缓存。"),
	}}
	session := newTestSession(t, model, protocol.ApprovalOnWrite)
	emitter := &recordingEmitter{approve: true, answer: &protocol.UserInputResponse{Selected: []string{"日志", "缓存"}, Text: "别动 Downloads"}}
	session.RunTurn(context.Background(), "t1", "清理一下磁盘", nil, nil, emitter)

	if len(emitter.questions) != 1 {
		t.Fatalf("应问一次：%+v", emitter.questions)
	}
	asked := emitter.questions[0]
	if asked.Question != "删哪些？" || len(asked.Options) != 2 || !asked.MultiSelect || asked.Options[1].Description != "约 2GB" {
		t.Errorf("问题、去重后的选项、多选标记都应到宿主：%+v", asked)
	}
	if len(emitter.approvals) != 0 {
		t.Errorf("提问不需要审批：%+v", emitter.approvals)
	}
	body := string(mustJSON(model.requests[1]))
	if !strings.Contains(body, "用户选了：日志、缓存") || !strings.Contains(body, "用户写道：别动 Downloads") {
		t.Errorf("回答应作为工具结果回到模型：%s", body)
	}
	if !emitter.find(protocol.NotifyItemCompleted, "好，删了日志和缓存。") {
		t.Error("拿到回答后这一轮应接着跑完")
	}
}

func TestAskUserSkipped(t *testing.T) {
	model := &fakeModel{script: []string{
		sseToolCall("c1", "ask_user", `{"question":"用哪个方案？"}`),
		sseText("按方案 A 做了。"),
	}}
	session := newTestSession(t, model, protocol.ApprovalOnWrite)
	session.RunTurn(context.Background(), "t1", "做吧", nil, nil, &recordingEmitter{approve: true, answer: &protocol.UserInputResponse{Skipped: true}})
	if body := string(mustJSON(model.requests[1])); !strings.Contains(body, "跳过") || !strings.Contains(body, "假设") {
		t.Errorf("跳过时要让模型按自己的判断继续并说明假设：%s", body)
	}
}

// 每次打模型、每次调工具都记一笔用量，带上会话、模型、token、工具名、技能名。
func TestTurnRecordsUsage(t *testing.T) {
	model := &fakeModel{script: []string{
		sseToolCall("c1", "list_dir", `{"path":"."}`),
		sseText("看完了。"),
	}}
	session := newTestSession(t, model, protocol.ApprovalBypass)
	var mu sync.Mutex
	var events []store.UsageEvent
	session.SetUsageSink(func(event store.UsageEvent) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	})
	session.RunTurn(context.Background(), "t1", "看看目录", nil, nil, &recordingEmitter{approve: true})

	mu.Lock()
	defer mu.Unlock()
	var models, toolsUsed int
	var tokens int
	for _, event := range events {
		if event.SessionID != session.ID {
			t.Errorf("用量要带会话 id：%+v", event)
		}
		switch event.Kind {
		case store.UsageModel:
			models++
			tokens += event.Total
			if event.Model != "fake" {
				t.Errorf("模型调用要带模型名：%+v", event)
			}
		case store.UsageTool:
			toolsUsed++
			if event.Tool != "list_dir" || event.Source != "builtin" {
				t.Errorf("工具调用要带工具名与来源：%+v", event)
			}
		}
	}
	if tokens == 0 {
		t.Error("上游报了 usage 的那次调用要记下 token")
	}
	if models != 2 || toolsUsed != 1 {
		t.Errorf("应记两次模型调用、一次工具调用：%+v", events)
	}
}

func mustJSON(value any) []byte {
	data, _ := json.Marshal(value)
	return data
}
