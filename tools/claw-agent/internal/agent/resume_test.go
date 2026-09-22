package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

// 恢复会话时必须按**当前**配置重挂 MCP 与技能。
//
// 存档里的配置是建会话那一刻的，而应用一启动就接着上次的会话——用存档的话，
// 用户后来加的 MCP server、刚同步到的内部平台能力永远挂不上，表现是「配了没用」，
// 而且他没有任何线索知道要去开个新会话。实际踩过。

// fakeMCPOverHTTP 起一个最小的 Streamable HTTP MCP server，只认
// initialize / tools/list，够验挂载。
func fakeMCPOverHTTP(t *testing.T, toolName string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var message struct {
			ID     *int64 `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&message)
		if message.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		result := `{}`
		switch message.Method {
		case "initialize":
			result = `{"protocolVersion":"2025-03-26","capabilities":{},"serverInfo":{"name":"fake","version":"1"}}`
		case "tools/list":
			result = fmt.Sprintf(
				`{"tools":[{"name":%q,"description":"假的","inputSchema":{"type":"object"},"annotations":{"readOnlyHint":true}}]}`,
				toolName,
			)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":%s}`, *message.ID, result)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func TestResumeMountsMCPServersFromCurrentConfig(t *testing.T) {
	model := &fakeModel{script: []string{sseText("好。")}}
	session := newTestSession(t, model, protocol.ApprovalOnWrite)
	session.RunTurn(context.Background(), "t1", "第一句", nil, &recordingEmitter{approve: true})

	ctx := context.Background()
	db := newTestStore(t)
	if err := session.Save(ctx, db); err != nil {
		t.Fatal(err)
	}

	// 存档里一个 MCP server 都没有。恢复时宿主带上当前配置。
	url := fakeMCPOverHTTP(t, "hot_topics")
	loaded, err := Load(ctx, db, "test", StaticKey("sk-test"), &protocol.SessionRefresh{
		MCPServers:     map[string]protocol.MCPServerConfig{"aihot": {URL: url}},
		ApprovalPolicy: protocol.ApprovalOnWrite,
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	t.Cleanup(loaded.Close)

	var found bool
	for _, name := range loaded.Tools() {
		if name == "aihot__hot_topics" {
			found = true
		}
	}
	if !found {
		t.Errorf("恢复后应当挂上当前配置里的 MCP 工具，实际有：%v", loaded.Tools())
	}
	if status := loaded.MCPStatus()["aihot"]; !strings.Contains(status, "已挂载") {
		t.Errorf("挂载状态 = %q", status)
	}
}

func TestResumeRefreshesSystemPromptSoItListsTheNewTools(t *testing.T) {
	// 提示词里列的是「你有哪些工具、哪些技能」。留着存档里那份，模型会不知道
	// 刚挂上的工具能用，或者去调一个已经不在的技能。
	model := &fakeModel{script: []string{sseText("好。")}}
	session := newTestSession(t, model, protocol.ApprovalOnWrite)
	session.RunTurn(context.Background(), "t1", "第一句", nil, &recordingEmitter{approve: true})

	ctx := context.Background()
	db := newTestStore(t)
	if err := session.Save(ctx, db); err != nil {
		t.Fatal(err)
	}

	url := fakeMCPOverHTTP(t, "hot_topics")
	loaded, err := Load(ctx, db, "test", StaticKey("sk-test"), &protocol.SessionRefresh{
		MCPServers: map[string]protocol.MCPServerConfig{"aihot": {URL: url}},
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	t.Cleanup(loaded.Close)

	if loaded.messages[0].Role != "system" {
		t.Fatalf("第一条应当仍是系统提示词，实际是 %s", loaded.messages[0].Role)
	}
	if !strings.Contains(loaded.messages[0].Content, "aihot__hot_topics") {
		t.Errorf("系统提示词里没有新挂上的工具：%s", loaded.messages[0].Content)
	}
	// 历史本身不能被动：换的只有第一条。
	if len(loaded.messages) != 3 {
		t.Errorf("历史应当还是 3 条，实际 %d", len(loaded.messages))
	}
	if loaded.messages[1].Content != "第一句" {
		t.Errorf("用户消息被动了：%q", loaded.messages[1].Content)
	}
}

func TestResumeWithoutRefreshKeepsTheArchivedConfig(t *testing.T) {
	// 不给 refresh 就是「按存档恢复」。宿主以外的调用方（比如以后的 CLI）
	// 不该因为没传这个参数就把会话的配置清空。
	model := &fakeModel{script: []string{sseText("好。")}}
	session := newTestSession(t, model, protocol.ApprovalAlways)
	session.RunTurn(context.Background(), "t1", "第一句", nil, &recordingEmitter{approve: true})

	ctx := context.Background()
	db := newTestStore(t)
	if err := session.Save(ctx, db); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(ctx, db, "test", StaticKey("sk-test"), nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	t.Cleanup(loaded.Close)
	if loaded.config.ApprovalPolicy != protocol.ApprovalAlways {
		t.Errorf("审批档位 = %q", loaded.config.ApprovalPolicy)
	}
}

func TestMountsMatchDetectsAddedServer(t *testing.T) {
	// 内核会把会话留在内存里。用户在这期间加了一个 MCP server，
	// 再切回这个会话时要能看出来「挂的和现在配置的不是一回事」。
	session := newTestSession(t, &fakeModel{}, protocol.ApprovalOnWrite)
	same := refreshOf(session.config)
	if !session.MountsMatch(same) {
		t.Error("同一份配置应当判为一致")
	}
	changed := same
	changed.MCPServers = map[string]protocol.MCPServerConfig{"aihot": {URL: "http://x"}}
	if session.MountsMatch(changed) {
		t.Error("加了一个 server 应当判为不一致")
	}
}

func TestChangingTheWorkspaceUpdatesTheSystemPrompt(t *testing.T) {
	/*
		实测踩到的：用户在会话开起来之后才指定工作区，而系统提示词是建会话
		那一刻生成的，里面还写着「这个会话没有设置工作区」。模型照着它说
		「在主目录里建目录会被沙箱拦住」，然后发现东西其实建到了工作区里，
		自己都愣了一下——它看到的环境说明和实际环境对不上。
	*/
	session := newTestSession(t, &fakeModel{}, protocol.ApprovalOnWrite)
	session.config.Workdir = ""
	session.messages[0].Content = buildSystemPrompt(session.config, session.registry, session.skills, "")
	if !strings.Contains(session.messages[0].Content, "没有设置工作区") {
		t.Fatalf("前提不成立：%s", session.messages[0].Content)
	}

	workspace := t.TempDir()
	if err := session.SetWorkspace(workspace); err != nil {
		t.Fatalf("设置工作区：%v", err)
	}

	prompt := session.messages[0].Content
	if !strings.Contains(prompt, workspace) {
		t.Errorf("提示词里应当是新的工作区：%s", prompt)
	}
	if strings.Contains(prompt, "没有设置工作区") {
		t.Errorf("旧的那句话还在：%s", prompt)
	}
	// 只换第一条，对话历史不能被动。
	if len(session.messages) != 1 {
		t.Errorf("不该动历史，实际 %d 条", len(session.messages))
	}
}
