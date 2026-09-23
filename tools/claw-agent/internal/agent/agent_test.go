package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/store"
)

// fakeModel 是一个脚本化的 Chat Completions 服务端：按调用次数依次吐出预设回应。
// 用它把整条循环（模型 → 工具 → 回填 → 模型）跑起来，不碰真实模型。
type fakeModel struct {
	mu       sync.Mutex
	calls    int
	requests []map[string]any
	script   []string // 每次调用返回的 SSE 正文
}

func (f *fakeModel) handler(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	f.requests = append(f.requests, body)
	index := f.calls
	f.calls++
	f.mu.Unlock()

	if index < len(f.script) {
		// 「!<状态码> <正文>」表示这一次按 HTTP 错误返回，用来测重试与压缩分支。
		if status, body, ok := scriptedFailure(f.script[index]); ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = fmt.Fprintf(w, `{"error":{"message":%q}}`, body)
			return
		}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	if index >= len(f.script) {
		_, _ = fmt.Fprint(w, sseText("（脚本已用完）"))
		return
	}
	_, _ = fmt.Fprint(w, f.script[index])
}

func scriptedFailure(entry string) (int, string, bool) {
	if !strings.HasPrefix(entry, "!") {
		return 0, "", false
	}
	rest := entry[1:]
	space := strings.IndexByte(rest, ' ')
	if space < 0 {
		return 0, "", false
	}
	status, err := strconv.Atoi(rest[:space])
	if err != nil {
		return 0, "", false
	}
	return status, rest[space+1:], true
}

// sseToolCalls 生成一条带多个并行工具调用的回应。
func sseToolCalls(calls ...[3]string) string {
	var fragments []any
	for index, call := range calls {
		fragments = append(fragments, map[string]any{
			"index": index, "id": call[0], "type": "function",
			"function": map[string]any{"name": call[1], "arguments": call[2]},
		})
	}
	chunk, _ := json.Marshal(map[string]any{
		"choices": []any{map[string]any{
			"delta":         map[string]any{"tool_calls": fragments},
			"finish_reason": "tool_calls",
		}},
	})
	return "data: " + string(chunk) + "\n\ndata: [DONE]\n\n"
}

func sseText(text string) string {
	chunk, _ := json.Marshal(map[string]any{
		"choices": []any{map[string]any{"delta": map[string]any{"content": text}}},
	})
	usage := `{"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}`
	return "data: " + string(chunk) + "\n\ndata: " + usage + "\n\ndata: [DONE]\n\n"
}

func sseToolCall(id, name, args string) string {
	chunk, _ := json.Marshal(map[string]any{
		"choices": []any{map[string]any{
			"delta": map[string]any{
				"tool_calls": []any{map[string]any{
					"index": 0, "id": id, "type": "function",
					"function": map[string]any{"name": name, "arguments": args},
				}},
			},
			"finish_reason": "tool_calls",
		}},
	})
	return "data: " + string(chunk) + "\n\ndata: [DONE]\n\n"
}

// recordingEmitter 收下全部事件，审批按预设决定。
type recordingEmitter struct {
	mu        sync.Mutex
	events    []string
	approvals []protocol.ApprovalRequestParams
	approve   bool
	// computer 记下每次屏幕操作请求；computerResult 是预置的返回。
	computer       []protocol.ComputerRequestParams
	computerResult protocol.ComputerResult
	computerOK     bool
	// scope 让用例模拟「本次会话都允许」那一档。
	scope         protocol.ApprovalScope
	browser       []protocol.BrowserRequestParams
	browserResult protocol.BrowserResult
	browserOK     bool
}

func (e *recordingEmitter) Notify(method string, params any) {
	raw, _ := json.Marshal(params)
	e.mu.Lock()
	e.events = append(e.events, method+" "+string(raw))
	e.mu.Unlock()
}

func (e *recordingEmitter) RequestApproval(
	_ context.Context,
	params protocol.ApprovalRequestParams,
) (protocol.ApprovalResponse, error) {
	e.mu.Lock()
	e.approvals = append(e.approvals, params)
	scope := e.scope
	e.mu.Unlock()
	return protocol.ApprovalResponse{Approved: e.approve, Scope: scope}, nil
}

// RequestComputer 记下屏幕操作请求，按预设返回。
//
// 默认不给结果：绝大多数用例根本不该碰到屏幕操作，真碰到了要能看出来。
func (e *recordingEmitter) RequestComputer(
	_ context.Context,
	params protocol.ComputerRequestParams,
) (protocol.ComputerResult, error) {
	e.mu.Lock()
	e.computer = append(e.computer, params)
	result, ok := e.computerResult, e.computerOK
	e.mu.Unlock()
	if !ok {
		return protocol.ComputerResult{}, errors.New("测试没有预置屏幕操作结果")
	}
	return result, nil
}

// RequestBrowser 记下浏览器请求，按预设返回。
func (e *recordingEmitter) RequestBrowser(
	_ context.Context,
	params protocol.BrowserRequestParams,
) (protocol.BrowserResult, error) {
	e.mu.Lock()
	e.browser = append(e.browser, params)
	result, ok := e.browserResult, e.browserOK
	e.mu.Unlock()
	if !ok {
		return protocol.BrowserResult{}, errors.New("测试没有预置浏览器操作结果")
	}
	return result, nil
}

func (e *recordingEmitter) methods() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for _, event := range e.events {
		out = append(out, strings.SplitN(event, " ", 2)[0])
	}
	return out
}

func (e *recordingEmitter) find(method, needle string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, event := range e.events {
		if strings.HasPrefix(event, method+" ") && strings.Contains(event, needle) {
			return true
		}
	}
	return false
}

func newTestSession(t *testing.T, model *fakeModel, policy protocol.ApprovalPolicy) *Session {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(model.handler))
	t.Cleanup(server.Close)

	workdir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(workdir); err == nil {
		workdir = resolved
	}
	session, err := New(context.Background(), "test", protocol.SessionStartParams{
		Model:          protocol.ModelConfig{BaseURL: server.URL, Model: "fake"},
		Workdir:        workdir,
		Instructions:   "你是测试助手。",
		ApprovalPolicy: policy,
	}, StaticKey("sk-test"))
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	// 退避换成零等待：这里要验的是重试的次数与分支，不是它睡得准不准。
	session.backoff = func(int) time.Duration { return 0 }
	t.Cleanup(session.Close)
	return session
}

func TestTurnWithoutToolsEmitsMessageAndCompletes(t *testing.T) {
	model := &fakeModel{script: []string{sseText("你好！")}}
	session := newTestSession(t, model, protocol.ApprovalOnWrite)
	emitter := &recordingEmitter{approve: true}

	session.RunTurn(context.Background(), "t1", "hi", nil, nil, emitter)

	methods := emitter.methods()
	// 固定的事件顺序：
	//   turn/started
	//   → userMessage completed
	//   → llm started（这次采样本身也是一个执行步骤）
	//   → agentMessage started / delta
	//   → llm completed（带耗时与 token）
	//   → agentMessage completed
	//   → turn/completed
	want := []string{
		protocol.NotifyTurnStarted,
		protocol.NotifyItemCompleted, // userMessage
		protocol.NotifyItemStarted,   // llm
		protocol.NotifyItemStarted,   // agentMessage
		protocol.NotifyItemDelta,
		protocol.NotifyItemCompleted, // llm
		protocol.NotifyItemCompleted, // agentMessage
		protocol.NotifyTurnCompleted,
	}
	if strings.Join(methods, ",") != strings.Join(want, ",") {
		t.Errorf("events = %v\nwant     %v", methods, want)
	}
	// 采样步骤要带上耗时与用量，界面靠它画那张执行清单。
	if !emitter.find(protocol.NotifyItemCompleted, `"kind":"llm"`) {
		t.Error("应当有一个 llm 执行步骤")
	}
	if !emitter.find(protocol.NotifyItemCompleted, `"totalTokens":8`) {
		t.Error("llm 步骤应当带 token 用量")
	}
	if !emitter.find(protocol.NotifyItemCompleted, `"text":"你好！"`) {
		t.Error("助手消息的完整正文应当出现在 completed 里")
	}
	if !emitter.find(protocol.NotifyTurnCompleted, `"totalTokens":8`) {
		t.Error("turn/completed 应当带 usage")
	}
	if emitter.find(protocol.NotifyTurnCompleted, `"error"`) {
		t.Error("正常结束不该带 error")
	}
	// 系统提示词应当告诉模型它在哪、能做什么。
	system := model.requests[0]["messages"].([]any)[0].(map[string]any)
	if !strings.Contains(system["content"].(string), session.config.Workdir) {
		t.Error("系统提示词应当包含工作目录")
	}
	if !strings.Contains(system["content"].(string), "你是测试助手") {
		t.Error("用户的 instructions 应当拼进系统提示词")
	}
}

func TestTurnExecutesToolAndFeedsResultBack(t *testing.T) {
	model := &fakeModel{script: []string{
		sseToolCall("c1", "write_file", `{"path":"out.txt","content":"写入成功"}`),
		sseText("已经写好了。"),
	}}
	session := newTestSession(t, model, protocol.ApprovalOnWrite)
	emitter := &recordingEmitter{approve: true}

	session.RunTurn(context.Background(), "t1", "写个文件", nil, nil, emitter)

	// 工具真的执行了。
	content, err := os.ReadFile(filepath.Join(session.config.Workdir, "out.txt"))
	if err != nil || string(content) != "写入成功" {
		t.Fatalf("文件没有被写入：%v %q", err, content)
	}
	// 工具条目有 started 与 completed，且 completed 带结果。
	if !emitter.find(protocol.NotifyItemStarted, `"toolName":"write_file"`) {
		t.Error("缺 toolCall 的 started")
	}
	if !emitter.find(protocol.NotifyItemCompleted, `"toolResult":"已写入 out.txt`) {
		t.Error("toolCall 的 completed 应当带结果")
	}
	// 第二次调模型时，历史里应当有 assistant(tool_calls) 与 tool 两条消息。
	if len(model.requests) != 2 {
		t.Fatalf("应当调模型 2 次，实际 %d", len(model.requests))
	}
	messages := model.requests[1]["messages"].([]any)
	last := messages[len(messages)-1].(map[string]any)
	if last["role"] != "tool" || last["tool_call_id"] != "c1" {
		t.Errorf("最后一条应当是 tool 消息且关联 c1：%v", last)
	}
	if !strings.Contains(last["content"].(string), "已写入") {
		t.Errorf("工具结果应当回填给模型：%v", last["content"])
	}
	prev := messages[len(messages)-2].(map[string]any)
	if prev["role"] != "assistant" || prev["tool_calls"] == nil {
		t.Errorf("倒数第二条应当是带 tool_calls 的 assistant：%v", prev)
	}
}

func TestToolFailureIsFedBackNotFatal(t *testing.T) {
	// 读凭据目录是硬拒绝，但那只是**这个工具失败了**，不是这一轮完蛋：
	// 原因回给模型，它据此改口或换个做法。
	model := &fakeModel{script: []string{
		sseToolCall("c1", "read_file", `{"path":"~/.ssh/id_rsa"}`),
		sseText("那个路径我读不了。"),
	}}
	session := newTestSession(t, model, protocol.ApprovalOnWrite)
	emitter := &recordingEmitter{approve: true}

	session.RunTurn(context.Background(), "t1", "读一下", nil, nil, emitter)

	if !emitter.find(protocol.NotifyItemCompleted, `"toolFailed":true`) {
		t.Error("失败的工具调用应当标记 toolFailed")
	}
	if !emitter.find(protocol.NotifyItemCompleted, "那个路径我读不了") {
		t.Error("模型拿到失败原因后应当能继续回答")
	}
	if emitter.find(protocol.NotifyTurnCompleted, `"error"`) {
		t.Error("工具失败不该让整轮失败")
	}
	messages := model.requests[1]["messages"].([]any)
	last := messages[len(messages)-1].(map[string]any)
	if !strings.Contains(last["content"].(string), "凭据") {
		t.Errorf("拒绝的原因应当回给模型：%v", last["content"])
	}
}

// 默认档位下普通命令不再逐条审批，但删除这类仍然要问。
func TestExecAsksApprovalAndDenialStopsCommand(t *testing.T) {
	model := &fakeModel{script: []string{
		sseToolCall("c1", "run_command", `{"command":"rm -rf danger","reason":"测试"}`),
		sseText("好的，不执行了。"),
	}}
	session := newTestSession(t, model, protocol.ApprovalOnWrite)
	emitter := &recordingEmitter{approve: false}

	session.RunTurn(context.Background(), "t1", "跑个命令", nil, nil, emitter)

	if len(emitter.approvals) != 1 {
		t.Fatalf("应当弹 1 次审批，弹了 %d", len(emitter.approvals))
	}
	req := emitter.approvals[0]
	// detail 里要有完整命令；后面还跟着执行目录，所以用 Contains。
	if req.Kind != protocol.ApprovalExec || !strings.Contains(req.Detail, "rm -rf danger") {
		t.Errorf("审批请求内容不对：%+v", req)
	}
	// 理由里既要有模型给的那句，也要有「为什么这条被挑出来问」。
	if !strings.Contains(req.Reason, "测试") || !strings.Contains(req.Reason, "rm -r") {
		t.Errorf("审批理由应当同时带上模型的说明与触发原因：%q", req.Reason)
	}
	if !emitter.find(protocol.NotifyItemCompleted, "拒绝") {
		t.Error("拒绝原因应当出现在工具条目里")
	}
}

func TestInterruptEndsTurnWithMarker(t *testing.T) {
	// 模型端故意挂住。除了等客户端取消，还要能被测试主动放行：
	// httptest.Server.Close 会等所有 handler 退出，而客户端取消请求后服务端的
	// r.Context() 未必立刻触发——只靠它会让 Cleanup 卡死。
	release := make(chan struct{})
	blocking := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(func() {
		close(release)
		blocking.Close()
	})

	session, err := New(context.Background(), "test", protocol.SessionStartParams{
		Model:   protocol.ModelConfig{BaseURL: blocking.URL, Model: "fake"},
		Workdir: t.TempDir(),
	}, StaticKey("sk"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(session.Close)

	emitter := &recordingEmitter{approve: true}
	done := make(chan struct{})
	go func() {
		session.RunTurn(context.Background(), "t1", "hi", nil, nil, emitter)
		close(done)
	}()

	// 等到轮次真的开始了再打断。
	for i := 0; i < 100; i++ {
		if len(emitter.methods()) > 0 {
			break
		}
		<-timeAfter(10)
	}
	session.Interrupt()
	<-done

	if !emitter.find(protocol.NotifyTurnCompleted, `"error":"已中断"`) {
		t.Errorf("中断后应当以「已中断」收尾：%v", emitter.events)
	}
}

func TestSaveAndLoadRestoresHistory(t *testing.T) {
	model := &fakeModel{script: []string{sseText("记住了。")}}
	session := newTestSession(t, model, protocol.ApprovalOnWrite)
	session.RunTurn(context.Background(), "t1", "记住 42", nil, nil, &recordingEmitter{approve: true})

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
	if loaded.Title != "记住 42" {
		t.Errorf("title = %q", loaded.Title)
	}
	// 历史里应当有 system + user + assistant 三条。
	if len(loaded.messages) != 3 {
		t.Errorf("恢复后应有 3 条消息，实际 %d", len(loaded.messages))
	}

	summaries, err := List(ctx, db)
	if err != nil || len(summaries) != 1 || summaries[0].TurnCount != 1 {
		t.Errorf("list = %+v, %v", summaries, err)
	}
	// 列表要能直接显示工作目录与模型，不必再去解析 config。
	if summaries[0].Model != "fake" {
		t.Errorf("列表应当带上模型，实际 %q", summaries[0].Model)
	}

	if err := Delete(ctx, db, "test"); err != nil {
		t.Fatal(err)
	}
	if remaining, _ := List(ctx, db); len(remaining) != 0 {
		t.Errorf("删除后列表应为空，实际 %d 条", len(remaining))
	}
}

// newTestStore 开一个临时会话库。
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestUnknownToolNameIsReported(t *testing.T) {
	model := &fakeModel{script: []string{
		sseToolCall("c1", "nonexistent_tool", `{}`),
		sseText("换个办法。"),
	}}
	session := newTestSession(t, model, protocol.ApprovalBypass)
	emitter := &recordingEmitter{approve: true}
	session.RunTurn(context.Background(), "t1", "x", nil, nil, emitter)

	messages := model.requests[1]["messages"].([]any)
	last := messages[len(messages)-1].(map[string]any)
	// 告诉模型有哪些工具，它才改得对。
	if !strings.Contains(last["content"].(string), "可用工具") {
		t.Errorf("未知工具的错误应当列出可用工具：%v", last["content"])
	}
}

func TestPromptSpellsOutTheHomeDirectoryWhenThereIsNoWorkspace(t *testing.T) {
	// 实测踩到的：提示词只说「相对路径按用户主目录解析」，没说是哪个目录，
	// 模型就自己编了一个 /Users/bytedance，然后命令在一个不存在的目录里执行。
	session := newTestSession(t, &fakeModel{}, protocol.ApprovalOnWrite)
	session.config.Workdir = ""
	prompt := buildSystemPrompt(session.config, session.registry, session.skills, "")

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("取不到主目录")
	}
	if !strings.Contains(prompt, home) {
		t.Errorf("没有工作区时，提示词里必须有主目录的真实路径：%s", prompt)
	}
}

// 代码模式下多模态工具要跟其它工具一起收进 exec：模型在脚本里写
// tools.generate_image(...) 是最自然的写法，留在外面它会得到「没有这个工具」。
func TestCodeModeFoldsMediaTools(t *testing.T) {
	model := &fakeModel{}
	server := httptest.NewServer(http.HandlerFunc(model.handler))
	t.Cleanup(server.Close)
	session, err := New(context.Background(), "test", protocol.SessionStartParams{
		Model:    protocol.ModelConfig{BaseURL: server.URL, Model: "fake"},
		Workdir:  t.TempDir(),
		CodeMode: true,
		Roles: protocol.RoleModels{
			Image: protocol.RoleModel{ProviderID: 1, Model: "qwen-image-3.0"},
			TTS:   protocol.RoleModel{ProviderID: 1, Model: "qwen3-tts-flash"},
		},
	}, StaticKey("sk-test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(session.Close)

	folded := strings.Join(session.FoldedTools(), ",")
	for _, name := range []string{"generate_image", "speak"} {
		if !strings.Contains(folded, name) {
			t.Errorf("%s 应收进 exec，实际收进去的是：%s", name, folded)
		}
	}
	for _, name := range session.Tools() {
		if name == "generate_image" || name == "speak" {
			t.Errorf("%s 不该再作为顶层工具出现：%v", name, session.Tools())
		}
	}
	if !strings.Contains(strings.Join(session.Tools(), ","), "exec") {
		t.Errorf("顶层应只剩 exec 等少数几个：%v", session.Tools())
	}
}

func TestGuardAppendsAndDeduplicates(t *testing.T) {
	session := newTestSession(t, &fakeModel{}, protocol.ApprovalOnWrite)
	session.Guard("/a/x.db", " ", "/a/x.db-wal")
	session.Guard("/a/x.db")
	got := session.config.ProtectedPaths
	if len(got) != 2 || got[0] != "/a/x.db" || got[1] != "/a/x.db-wal" {
		t.Errorf("名单应追加且去重：%v", got)
	}
}
