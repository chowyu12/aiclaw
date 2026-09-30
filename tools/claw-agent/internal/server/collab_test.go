package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/agent"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

// 子 agent 端到端：真的内核会话、真的工具循环，模型换成本机的假服务。
// 同一个假服务同时扮演根 agent 与子 agent，按请求里有没有「You are a sub-agent」分辨。

func sseToolCall(id, name, args string) string {
	chunk, _ := json.Marshal(map[string]any{
		"choices": []any{map[string]any{
			"delta": map[string]any{"tool_calls": []any{map[string]any{
				"index": 0, "id": id, "type": "function",
				"function": map[string]any{"name": name, "arguments": args},
			}}},
			"finish_reason": "tool_calls",
		}},
	})
	return "data: " + string(chunk) + "\n\ndata: [DONE]\n\n"
}

func sseText(text string) string {
	chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": text}}}})
	return "data: " + string(chunk) + "\n\ndata: [DONE]\n\n"
}

type wireRequest struct {
	Messages []struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	} `json:"messages"`
}

func (r wireRequest) text() string {
	var builder strings.Builder
	for _, message := range r.Messages {
		if text, ok := message.Content.(string); ok {
			builder.WriteString(text)
			builder.WriteString("\n")
		}
	}
	return builder.String()
}

func (r wireRequest) toolResults() int {
	count := 0
	for _, message := range r.Messages {
		if message.Role == "tool" {
			count++
		}
	}
	return count
}

// scriptedModel 按「谁在问、问到第几步」回答。
type scriptedModel struct {
	mu       sync.Mutex
	root     func(step int, request wireRequest) string
	child    func(request wireRequest) string
	requests []wireRequest
}

func (m *scriptedModel) handler(w http.ResponseWriter, r *http.Request) {
	var request wireRequest
	body, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(body, &request)
	m.mu.Lock()
	m.requests = append(m.requests, request)
	m.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	if strings.Contains(request.text(), "You are a sub-agent") {
		_, _ = io.WriteString(w, m.child(request))
		return
	}
	_, _ = io.WriteString(w, m.root(request.toolResults(), request))
}

func (m *scriptedModel) lastRootRequest() wireRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.requests) - 1; i >= 0; i-- {
		if !strings.Contains(m.requests[i].text(), "You are a sub-agent") {
			return m.requests[i]
		}
	}
	return wireRequest{}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func collabServer(t *testing.T, model *scriptedModel) (*Server, *agent.Session) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(model.handler))
	t.Cleanup(upstream.Close)
	server, err := New(Options{DataHome: t.TempDir(), APIKey: "sk-test"}, discard{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		// 先停下所有还在跑的轮次，等它们收尾（存档）再关库。
		cancel()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			busy := false
			server.sessMu.Lock()
			for _, session := range server.sessions {
				busy = busy || session.Busy()
			}
			server.sessMu.Unlock()
			if !busy {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		time.Sleep(50 * time.Millisecond)
		server.closeAll()
		_ = server.db.Close()
	})
	server.collab.setContext(ctx)
	id := "s_root"
	root, err := agent.New(ctx, id, protocol.SessionStartParams{
		Model:          protocol.ModelConfig{BaseURL: upstream.URL, Model: "fake"},
		Workdir:        t.TempDir(),
		ApprovalPolicy: protocol.ApprovalBypass,
	}, server.keyFor, server.sessionOptions(id)...)
	if err != nil {
		t.Fatal(err)
	}
	server.sessions[id] = root
	return server, root
}

func eventually(t *testing.T, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("等不到：%s", what)
}

// 根 agent 开一个子 agent、等它，子 agent 的最终回答作为邮箱消息回到根 agent 的上下文里。
func TestSpawnWaitDeliversChildAnswer(t *testing.T) {
	model := &scriptedModel{
		root: func(step int, _ wireRequest) string {
			switch step {
			case 0:
				return sseToolCall("c1", "spawn_agent", `{"task_name":"research","message":"查一下 X 是多少","fork_turns":"none"}`)
			case 1:
				return sseToolCall("c2", "wait_agent", `{"timeout_ms":60000}`)
			}
			return sseText("汇总：X 是 42。")
		},
		child: func(wireRequest) string { return sseText("X 是 42") },
	}
	server, root := collabServer(t, model)
	root.RunTurn(context.Background(), "t1", "帮我调研 X", nil, nil, &emitter{server: server})

	final := model.lastRootRequest().text()
	for _, want := range []string{"<subagent_notification>", `"agent_path":"/root/research"`, `"status":"completed"`, "X 是 42", `"message":"Wait completed."`} {
		if !strings.Contains(final, want) {
			t.Errorf("根 agent 最后一次请求里缺 %q", want)
		}
	}
	if root.LastAnswer() != "汇总：X 是 42。" {
		t.Errorf("根 agent 的回答不对：%q", root.LastAnswer())
	}

	// 子会话是个普通会话：存了档，记着父会话。
	eventually(t, "子会话存档", func() bool {
		list, err := agent.List(context.Background(), server.db)
		if err != nil {
			return false
		}
		for _, item := range list {
			if item.ParentID == "s_root" && item.Title == "↳ research" {
				return true
			}
		}
		return false
	})
	agents, _ := server.collab.List(root, "")
	if len(agents) != 2 || agents[1].AgentName != "/root/research" || agents[1].AgentStatus != agent.AgentCompleted {
		t.Errorf("list_agents 不对：%+v", agents)
	}
}

// 根 agent 开完就收工了：子 agent 做完时它闲着，自动开一轮来接收结果。
func TestIdleParentWakesForChildResult(t *testing.T) {
	release := make(chan struct{})
	model := &scriptedModel{
		root: func(step int, request wireRequest) string {
			if strings.Contains(request.text(), "<subagent_notification>") {
				return sseText("收到结果了")
			}
			if step == 0 {
				return sseToolCall("c1", "spawn_agent", `{"task_name":"slow","message":"慢慢做","fork_turns":"none"}`)
			}
			return sseText("已经派出去了")
		},
		child: func(wireRequest) string {
			<-release
			return sseText("做完了")
		},
	}
	server, root := collabServer(t, model)
	root.RunTurn(context.Background(), "t1", "派个活", nil, nil, &emitter{server: server})
	if root.LastAnswer() != "已经派出去了" {
		t.Fatalf("第一轮的回答不对：%q", root.LastAnswer())
	}
	// 子 agent 还在跑（卡在 release 上），会话列表里就应当看得到它。
	list, err := agent.List(context.Background(), server.db)
	found := false
	for _, item := range list {
		found = found || (item.ParentID == "s_root" && item.Title == "↳ slow")
	}
	if err != nil || !found {
		t.Errorf("还在跑的子 agent 应当已经在会话列表里：%v %+v", err, list)
	}
	close(release)
	eventually(t, "根 agent 被叫醒处理结果", func() bool { return root.LastAnswer() == "收到结果了" })
}

// 名字重复、层数超限、followup_task 发给根：都在工具层面说清楚。
func TestCollabGuards(t *testing.T) {
	model := &scriptedModel{
		root:  func(int, wireRequest) string { return sseText("好") },
		child: func(wireRequest) string { return sseText("好") },
	}
	server, root := collabServer(t, model)
	hub := server.collab
	ctx := context.Background()
	if _, err := hub.Spawn(ctx, root, agent.SpawnRequest{TaskName: "a", Message: "x", ForkTurns: 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := hub.Spawn(ctx, root, agent.SpawnRequest{TaskName: "a", Message: "x", ForkTurns: 0}); err == nil || !strings.Contains(err.Error(), "已经有叫 /root/a") {
		t.Errorf("重名应当报错：%v", err)
	}
	if err := hub.Send(ctx, root, "/root", "hi", false); err == nil {
		t.Error("不能给自己发")
	}
	child := server.session(findNode(hub, "/root/a").sessionID)
	if err := hub.Send(ctx, child, "/root", "hi", true); err == nil || !strings.Contains(err.Error(), "只能发给子 agent") {
		t.Errorf("followup_task 不能发给根：%v", err)
	}
	if err := hub.Send(ctx, root, "nobody", "hi", false); err == nil || !strings.Contains(err.Error(), "/root/nobody") {
		t.Errorf("找不到的目标要说清楚：%v", err)
	}
	grand, err := hub.Spawn(ctx, child, agent.SpawnRequest{TaskName: "b", Message: "x"})
	if err != nil || grand.TaskName != "/root/a/b" {
		t.Fatalf("第二层应当可以：%v %+v", err, grand)
	}
	third := server.session(findNode(hub, "/root/a/b").sessionID)
	if _, err := hub.Spawn(ctx, third, agent.SpawnRequest{TaskName: "c", Message: "x"}); err == nil || !strings.Contains(err.Error(), "最多嵌套") {
		t.Errorf("第三层应当拒绝：%v", err)
	}
	// 子 agent 用相对名只能找自己的子树；找根要写规范名。
	if err := hub.Send(ctx, child, "b", "hi", false); err != nil {
		t.Errorf("子 agent 找自己的子任务：%v", err)
	}
}

// 从邮箱开的一轮里，邮件以隐藏消息进历史，时间线上只有一行提示。
func TestMailIsHiddenButVisibleToModel(t *testing.T) {
	model := &scriptedModel{
		root:  func(int, wireRequest) string { return sseText("明白") },
		child: func(wireRequest) string { return sseText("好") },
	}
	server, root := collabServer(t, model)
	root.DeliverAgentMail("/root/x", "<agent_message from=\"/root/x\">\n进度 50%\n</agent_message>", "📨 收到 /root/x 的消息")
	server.collab.runTurn(root, server.collab.node(root), "")
	if !strings.Contains(model.lastRootRequest().text(), "进度 50%") {
		t.Error("模型应当看到邮件内容")
	}
	if root.LastAnswer() != "明白" {
		t.Errorf("回答不对：%q", root.LastAnswer())
	}
}

func findNode(hub *collabHub, path string) *collabNode {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	for _, node := range hub.nodes {
		if node.path == path {
			return node
		}
	}
	return nil
}

// 删父会话，子 agent 和孙子一起删；删完名字能再用。父子关系按存档里的 parentId 找。
func TestDeleteSessionTreeCascades(t *testing.T) {
	model := &scriptedModel{
		root:  func(int, wireRequest) string { return sseText("好") },
		child: func(wireRequest) string { return sseText("好") },
	}
	server, root := collabServer(t, model)
	hub := server.collab
	ctx := context.Background()
	if err := root.Save(ctx, server.db); err != nil {
		t.Fatal(err)
	}
	if _, err := hub.Spawn(ctx, root, agent.SpawnRequest{TaskName: "a", Message: "x"}); err != nil {
		t.Fatal(err)
	}
	child := server.session(findNode(hub, "/root/a").sessionID)
	if _, err := hub.Spawn(ctx, child, agent.SpawnRequest{TaskName: "b", Message: "x"}); err != nil {
		t.Fatal(err)
	}
	grand := findNode(hub, "/root/a/b").sessionID
	// 等两个子 agent 跑完，免得删到一半它们又存了一次档。
	eventually(t, "子 agent 跑完", func() bool {
		return !child.Busy() && server.session(grand) != nil && !server.session(grand).Busy()
	})
	if _, err := hub.Spawn(ctx, root, agent.SpawnRequest{TaskName: "keep", Message: "x"}); err != nil {
		t.Fatal(err)
	}
	keepID := findNode(hub, "/root/keep").sessionID
	eventually(t, "keep 跑完", func() bool { return !server.session(keepID).Busy() })

	deleted, err := server.deleteSessionTree(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 2 || deleted[0] != grand || deleted[1] != child.ID {
		t.Errorf("应当先删孙子再删子：%v", deleted)
	}
	list, _ := agent.List(ctx, server.db)
	ids := map[string]bool{}
	for _, item := range list {
		ids[item.ID] = true
	}
	if ids[child.ID] || ids[grand] || !ids[keepID] || !ids[root.ID] {
		t.Errorf("只该删掉这一支：%v", ids)
	}
	if server.session(child.ID) != nil || findNode(hub, "/root/a") != nil {
		t.Error("内存里的会话和协作树节点也要摘掉")
	}
	if _, err := hub.Spawn(ctx, root, agent.SpawnRequest{TaskName: "a", Message: "x"}); err != nil {
		t.Errorf("删掉之后名字应当能再用：%v", err)
	}
}

// 归档父会话：子 agent 一起归档、从内存里摘掉；恢复时整支回来。
func TestArchiveSessionTree(t *testing.T) {
	model := &scriptedModel{
		root:  func(int, wireRequest) string { return sseText("好") },
		child: func(wireRequest) string { return sseText("好") },
	}
	server, root := collabServer(t, model)
	hub := server.collab
	ctx := context.Background()
	if err := root.Save(ctx, server.db); err != nil {
		t.Fatal(err)
	}
	if _, err := hub.Spawn(ctx, root, agent.SpawnRequest{TaskName: "a", Message: "x"}); err != nil {
		t.Fatal(err)
	}
	childID := findNode(hub, "/root/a").sessionID
	eventually(t, "子 agent 跑完", func() bool { return !server.session(childID).Busy() })

	changed, err := server.archiveSessionTree(ctx, root.ID, true)
	if err != nil || len(changed) != 2 || changed[1] != root.ID {
		t.Fatalf("应当归档父子两个，父的在最后：%v %v", changed, err)
	}
	if list, _ := agent.List(ctx, server.db); len(list) != 0 {
		t.Errorf("列表里不该还有：%+v", list)
	}
	archived, _ := agent.ListArchived(ctx, server.db)
	if len(archived) != 2 || archived[0].ArchivedAt == nil {
		t.Errorf("归档列表不对：%+v", archived)
	}
	if server.session(root.ID) != nil || server.session(childID) != nil {
		t.Error("归档的会话要从内存里摘掉")
	}
	if _, err := server.archiveSessionTree(ctx, root.ID, false); err != nil {
		t.Fatal(err)
	}
	list, _ := agent.List(ctx, server.db)
	if len(list) != 2 {
		t.Errorf("恢复之后整支回来：%+v", list)
	}
}

// 会话引用：B 引用 A，模型在 B 里调 read_thread 读到 A 的结论；A 在存档里时也读得到。
func TestReadThreadAcrossSessions(t *testing.T) {
	model := &scriptedModel{
		root: func(step int, request wireRequest) string {
			text := request.text()
			if !strings.Contains(text, "## Referenced chats") {
				return sseText("结论：用方案二")
			}
			if step == 0 {
				return sseToolCall("r1", "read_thread", `{"threadId":"s_root"}`)
			}
			return sseText("读到了")
		},
		child: func(wireRequest) string { return sseText("好") },
	}
	server, first := collabServer(t, model)
	ctx := context.Background()
	first.RunTurn(ctx, "t1", "定个方案", nil, nil, &emitter{server: server})
	if err := first.Save(ctx, server.db); err != nil {
		t.Fatal(err)
	}
	// 从内存里摘掉，逼 read_thread 去读存档。
	server.unload(first.ID)

	id := "s_second"
	second, err := agent.New(ctx, id, first.Config(), server.keyFor, server.sessionOptions(id)...)
	if err != nil {
		t.Fatal(err)
	}
	server.sessions[id] = second
	second.RunTurn(ctx, "t2", "照 @定个方案 的结论往下做", nil, nil, &emitter{server: server},
		protocol.ThreadRef{ID: "s_root", Title: "定个方案"})

	last := model.lastRootRequest().text()
	for _, want := range []string{"## Referenced chats", "[@定个方案](thread://s_root)", "结论：用方案二", "untrusted data"} {
		if !strings.Contains(last, want) {
			t.Errorf("B 的最后一次请求里缺 %q", want)
		}
	}
	if second.LastAnswer() != "读到了" {
		t.Errorf("回答不对：%q", second.LastAnswer())
	}
}
