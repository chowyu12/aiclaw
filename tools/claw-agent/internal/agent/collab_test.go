package agent

import (
	"context"
	"testing"
	"time"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

func TestParseForkTurns(t *testing.T) {
	for input, want := range map[string]int{"": -1, "all": -1, "ALL": -1, "none": 0, "3": 3} {
		got, err := parseForkTurns(input)
		if err != nil || got != want {
			t.Errorf("parseForkTurns(%q) = %d, %v；应为 %d", input, got, err, want)
		}
	}
	for _, bad := range []string{"0", "-1", "x"} {
		if _, err := parseForkTurns(bad); err == nil {
			t.Errorf("%q 应当报错", bad)
		}
	}
}

func bareSession(t *testing.T) *Session {
	t.Helper()
	session, err := New(context.Background(), "s", protocol.SessionStartParams{
		Model: protocol.ModelConfig{BaseURL: "http://127.0.0.1:1", Model: "m"},
	}, StaticKey("k"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(session.Close)
	return session
}

// 分叉只带完整的历史：父会话此刻正在调 spawn_agent，那条工具调用还没有结果，
// 带过去子会话第一次请求就是 400。按「用户说的话」数轮。
func TestForkFromDropsUnansweredCallsAndCountsTurns(t *testing.T) {
	parent := bareSession(t)
	parent.messages = append(parent.messages,
		llm.Message{Role: llm.RoleUser, Content: "第一轮"},
		llm.Message{Role: llm.RoleAssistant, Content: "答一"},
		llm.Message{Role: llm.RoleUser, Content: "第二轮"},
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "read_file"}}},
		llm.Message{Role: llm.RoleTool, ToolCallID: "c1", Content: "内容"},
		llm.Message{Role: llm.RoleUser, Content: "（上一步截屏）", Shown: &llm.Shown{Hidden: true}},
		llm.Message{Role: llm.RoleAssistant, Content: "答二"},
		llm.Message{Role: llm.RoleUser, Content: "第三轮"},
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c2", Name: "spawn_agent"}}},
	)

	all := bareSession(t)
	all.ForkFrom(parent, -1)
	last := all.messages[len(all.messages)-1]
	if last.Role != llm.RoleUser || last.Content != "第三轮" {
		t.Errorf("没结果的 spawn_agent 调用应当去掉，最后一条应是「第三轮」：%+v", last)
	}
	if all.messages[0].Role != llm.RoleSystem || countRole(all.messages, llm.RoleSystem) != 1 {
		t.Error("只留子会话自己的系统提示词")
	}

	two := bareSession(t)
	two.ForkFrom(parent, 2)
	if two.messages[1].Content != "第二轮" {
		t.Errorf("最近两轮应当从「第二轮」开始（隐藏的截屏不算一轮）：%q", two.messages[1].Content)
	}

	none := bareSession(t)
	none.ForkFrom(parent, 0)
	if len(none.messages) != 1 {
		t.Errorf("none 不带任何历史：%d 条", len(none.messages))
	}
}

func countRole(messages []llm.Message, role llm.Role) int {
	n := 0
	for _, message := range messages {
		if message.Role == role {
			n++
		}
	}
	return n
}

// wait_agent：已经排着的立刻返回；邮件与用户插话分得清；没东西就超时。
func TestWaitPending(t *testing.T) {
	session := bareSession(t)
	if got := session.waitPending(context.Background(), 30*time.Millisecond); got != "timeout" {
		t.Errorf("空邮箱应当超时：%s", got)
	}
	session.DeliverAgentMail("/root/a", "x", "")
	if got := session.waitPending(context.Background(), time.Second); got != "mailbox" {
		t.Errorf("已有邮件应当立刻返回 mailbox：%s", got)
	}
	session.mu.Lock()
	session.pending = nil
	session.mu.Unlock()
	go func() {
		time.Sleep(20 * time.Millisecond)
		session.mu.Lock()
		session.pending = append(session.pending, userInput{text: "别做了"})
		session.mu.Unlock()
		session.signalPending()
	}()
	if got := session.waitPending(context.Background(), time.Second); got != "steered" {
		t.Errorf("用户插话应当结束等待：%s", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	session.mu.Lock()
	session.pending = nil
	session.mu.Unlock()
	if got := session.waitPending(ctx, time.Second); got != "canceled" {
		t.Errorf("轮次取消时应当返回 canceled：%s", got)
	}
}

func TestCollabToolsOnlyWithController(t *testing.T) {
	if names := bareSession(t).Tools(); contains(names, "spawn_agent") {
		t.Error("没接控制面时不挂协作工具")
	}
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
