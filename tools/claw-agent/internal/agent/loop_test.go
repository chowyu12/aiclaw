package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/mcpclient"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/tools"
)

// userTexts 取出请求里全部用户消息的正文，用来断言历史被怎么改过。
func userTexts(t *testing.T, request map[string]any) []string {
	t.Helper()
	raw, err := json.Marshal(request["messages"])
	if err != nil {
		t.Fatal(err)
	}
	var messages []struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	}
	if err := json.Unmarshal(raw, &messages); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, message := range messages {
		if message.Role != "user" {
			continue
		}
		if text, ok := message.Content.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

func TestTransientFailureIsRetried(t *testing.T) {
	model := &fakeModel{script: []string{
		"!500 upstream exploded",
		"!429 slow down",
		sseText("总算好了。"),
	}}
	session := newTestSession(t, model, protocol.ApprovalOnWrite)
	emitter := &recordingEmitter{approve: true}

	session.RunTurn(context.Background(), "t1", "你好", nil, nil, emitter)

	if model.calls != 3 {
		t.Fatalf("5xx 与 429 都该重试，期望打 3 次模型，实际 %d 次", model.calls)
	}
	if !emitter.find(protocol.NotifyItemCompleted, "总算好了。") {
		t.Errorf("重试成功后应当给出回答：%v", emitter.events)
	}
	if !emitter.find(protocol.NotifyItemCompleted, `"kind":"notice"`) {
		t.Errorf("重试要让用户看见，期望有 notice 条目：%v", emitter.events)
	}
	if emitter.find(protocol.NotifyTurnCompleted, `"error"`) {
		t.Errorf("重试成功的轮次不该带 error：%v", emitter.events)
	}
}

func TestNonRetryableFailureStopsImmediately(t *testing.T) {
	// 401 是配置问题，重试只是把同一个错再收四遍，还要多花时间。
	model := &fakeModel{script: []string{"!401 invalid api key", sseText("不该走到这里")}}
	session := newTestSession(t, model, protocol.ApprovalOnWrite)
	emitter := &recordingEmitter{approve: true}

	session.RunTurn(context.Background(), "t1", "你好", nil, nil, emitter)

	if model.calls != 1 {
		t.Fatalf("鉴权失败不该重试，期望打 1 次模型，实际 %d 次", model.calls)
	}
	if !emitter.find(protocol.NotifyTurnCompleted, "invalid api key") {
		t.Errorf("失败原因应当原样回给宿主：%v", emitter.events)
	}
}

func TestContextWindowExceededTriggersCompaction(t *testing.T) {
	// 第一次采样报超窗 → 压缩（第二次调用，模型给摘要）→ 重试成功（第三次）。
	model := &fakeModel{script: []string{
		"!400 This model's maximum context length is 8192 tokens",
		sseText("摘要：用户想要 42。"),
		sseText("好的。"),
	}}
	session := newTestSession(t, model, protocol.ApprovalOnWrite)
	// 先垫一段历史，压缩才有东西可保留。
	session.appendMessage(llm.Message{Role: llm.RoleUser, Content: "早先说过的要求"})
	session.appendMessage(llm.Message{Role: llm.RoleAssistant, Content: "收到"})
	emitter := &recordingEmitter{approve: true}

	session.RunTurn(context.Background(), "t1", "继续", nil, nil, emitter)

	if model.calls != 3 {
		t.Fatalf("期望「超窗 → 压缩 → 重试」共 3 次调用，实际 %d 次", model.calls)
	}
	if !emitter.find(protocol.NotifyItemCompleted, "好的。") {
		t.Errorf("压缩后应当拿到回答：%v", emitter.events)
	}

	// 第三次请求跑在压缩后的历史上：助手的过程消息没了，摘要在。
	final := userTexts(t, model.requests[2])
	joined := strings.Join(final, "\n")
	if !strings.Contains(joined, summaryPrefix) {
		t.Errorf("压缩后的历史应当带上摘要前缀，实际用户消息：%v", final)
	}
	if !strings.Contains(joined, "早先说过的要求") {
		t.Errorf("压缩应当保留用户消息，实际：%v", final)
	}
	var messages []any
	raw, _ := json.Marshal(model.requests[2]["messages"])
	_ = json.Unmarshal(raw, &messages)
	if len(messages) >= len(model.requests[0]["messages"].([]any))+3 {
		t.Errorf("压缩后的历史没有变短：%d 条", len(messages))
	}
}

func TestBuildCompactedHistoryDropsToolTraffic(t *testing.T) {
	history := []llm.Message{
		{Role: llm.RoleSystem, Content: "系统提示词"},
		{Role: llm.RoleUser, Content: "第一个要求"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "read_file"}}},
		{Role: llm.RoleTool, ToolCallID: "c1", Content: "文件内容"},
		{Role: llm.RoleAssistant, Content: "读完了"},
		{Role: llm.RoleUser, Content: "第二个要求"},
	}

	compacted := buildCompactedHistory(history, "做了 A 和 B")

	if compacted[0].Role != llm.RoleSystem {
		t.Fatalf("系统提示词必须留在第一条，实际 %q", compacted[0].Role)
	}
	for _, message := range compacted {
		if len(message.ToolCalls) > 0 || message.Role == llm.RoleTool {
			// 留下任何一条 tool_calls 而没有配套结果，下一次请求就是 400。
			t.Fatalf("压缩后的历史不该含工具流量：%+v", message)
		}
	}
	var texts []string
	for _, message := range compacted {
		texts = append(texts, message.Content)
	}
	joined := strings.Join(texts, "|")
	if !strings.Contains(joined, "第一个要求") || !strings.Contains(joined, "第二个要求") {
		t.Errorf("用户消息应当按时间顺序全部保留：%v", texts)
	}
	if index1, index2 := strings.Index(joined, "第一个要求"), strings.Index(joined, "第二个要求"); index1 > index2 {
		t.Errorf("保留的用户消息顺序反了：%v", texts)
	}
	if !strings.Contains(compacted[len(compacted)-1].Content, "做了 A 和 B") {
		t.Errorf("摘要应当排在最后：%+v", compacted[len(compacted)-1])
	}
}

func TestWriteToolsDoNotRunInParallel(t *testing.T) {
	model := &fakeModel{script: []string{
		sseToolCalls(
			[3]string{"c1", "slow_write", `{}`},
			[3]string{"c2", "slow_write", `{}`},
		),
		sseText("写完了。"),
	}}
	session := newTestSession(t, model, protocol.ApprovalBypass)

	var inFlight, maxInFlight int32
	register(t, session, tools.Tool{
		Name: "slow_write", Effect: tools.EffectWrite,
		Handler: func(ctx context.Context, _ json.RawMessage, _ *tools.Env) (string, error) {
			current := atomic.AddInt32(&inFlight, 1)
			for {
				peak := atomic.LoadInt32(&maxInFlight)
				if current <= peak || atomic.CompareAndSwapInt32(&maxInFlight, peak, current) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			atomic.AddInt32(&inFlight, -1)
			return "ok", nil
		},
	})

	session.RunTurn(context.Background(), "t1", "写两个文件", nil, nil, &recordingEmitter{approve: true})

	if got := atomic.LoadInt32(&maxInFlight); got != 1 {
		t.Fatalf("有副作用的工具必须串行，实际同时在跑 %d 个", got)
	}
}

func TestReadToolsRunInParallel(t *testing.T) {
	model := &fakeModel{script: []string{
		sseToolCalls(
			[3]string{"c1", "slow_read", `{}`},
			[3]string{"c2", "slow_read", `{}`},
		),
		sseText("读完了。"),
	}}
	session := newTestSession(t, model, protocol.ApprovalBypass)

	// 两个只读调用应当能撞在一起。用栅栏断言而不是比总耗时：
	// 后者在慢机器上会假阳性。
	gate := make(chan struct{}, 2)
	both := make(chan struct{})
	var once sync.Once
	register(t, session, tools.Tool{
		Name: "slow_read", Effect: tools.EffectRead,
		Handler: func(ctx context.Context, _ json.RawMessage, _ *tools.Env) (string, error) {
			gate <- struct{}{}
			if len(gate) == 2 {
				once.Do(func() { close(both) })
			}
			select {
			case <-both:
			case <-time.After(2 * time.Second):
				return "", context.DeadlineExceeded
			}
			<-gate
			return "ok", nil
		},
	})

	session.RunTurn(context.Background(), "t1", "读两个文件", nil, nil, &recordingEmitter{approve: true})

	select {
	case <-both:
	default:
		t.Fatal("两个只读工具没有并发执行")
	}
}

func TestSteeringInputJoinsRunningTurn(t *testing.T) {
	model := &fakeModel{script: []string{
		sseToolCalls([3]string{"c1", "park", `{}`}),
		sseText("按新要求办。"),
	}}
	session := newTestSession(t, model, protocol.ApprovalBypass)

	// 第一个工具跑起来之后再插话，模拟用户看见跑偏了立刻纠正。
	entered := make(chan struct{})
	register(t, session, tools.Tool{
		Name: "park", Effect: tools.EffectRead,
		Handler: func(ctx context.Context, _ json.RawMessage, _ *tools.Env) (string, error) {
			close(entered)
			return "ok", nil
		},
	})

	emitter := &recordingEmitter{approve: true}
	done := make(chan struct{})
	go func() {
		session.RunTurn(context.Background(), "t1", "原来的要求", nil, nil, emitter)
		close(done)
	}()

	<-entered
	turn, queued := session.Enqueue("改一下：用另一种方式", nil)
	<-done

	if !queued {
		t.Fatal("轮次进行中的输入应当排队，而不是被拒绝")
	}
	if turn != "t1" {
		t.Errorf("排队的输入应当归到进行中那一轮，实际 %q", turn)
	}
	if model.calls < 2 {
		t.Fatalf("期望至少两次采样，实际 %d 次", model.calls)
	}
	last := userTexts(t, model.requests[model.calls-1])
	if !strings.Contains(strings.Join(last, "\n"), "改一下") {
		t.Errorf("下一次采样应当看到插进来的输入，实际用户消息：%v", last)
	}
}

func TestInterruptLeavesHistoryReusable(t *testing.T) {
	session := newTestSession(t, &fakeModel{script: []string{sseText("不会跑到")}}, protocol.ApprovalBypass)
	// 造出「模型发了工具调用，结果还没回来」的中断现场。
	session.appendMessage(llm.Message{Role: llm.RoleUser, Content: "干活"})
	session.appendMessage(llm.Message{
		Role:      llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{{ID: "c1", Name: "run_command"}, {ID: "c2", Name: "run_command"}},
	})
	session.appendMessage(llm.Message{Role: llm.RoleTool, ToolCallID: "c1", Content: "done"})

	session.recordInterruption()

	answered := map[string]bool{}
	for _, message := range session.messages {
		if message.Role == llm.RoleTool {
			answered[message.ToolCallID] = true
		}
	}
	// 缺一条结果，下一轮开口就是 400，会话直接废掉。
	if !answered["c1"] || !answered["c2"] {
		t.Fatalf("每个 tool_call 都要有结果，实际：%v", answered)
	}
	last := session.messages[len(session.messages)-1]
	if last.Role != llm.RoleUser || !strings.Contains(last.Content, "用户主动中断") {
		t.Errorf("中断说明应当排在历史最后：%+v", last)
	}
}

func TestDropOldestKeepsHistoryValid(t *testing.T) {
	session := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	session.messages = []llm.Message{
		{Role: llm.RoleSystem, Content: "系统"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c1"}}},
		{Role: llm.RoleTool, ToolCallID: "c1", Content: "结果"},
		{Role: llm.RoleUser, Content: "留下来的"},
	}

	if !session.dropOldest() {
		t.Fatal("还有可丢的消息")
	}

	if session.messages[0].Role != llm.RoleSystem {
		t.Errorf("系统提示词不该被丢：%+v", session.messages[0])
	}
	for _, message := range session.messages {
		if message.Role == llm.RoleTool {
			// 丢掉 assistant 却留下它的结果，就成了孤儿，上游会 400。
			t.Fatalf("孤儿 tool 结果应当一并丢掉：%+v", message)
		}
	}
}

func TestToolOutputIsTruncatedInHistoryOnly(t *testing.T) {
	big := strings.Repeat("行内容——中文也要切在字符边界上\n", 4000)
	model := &fakeModel{script: []string{
		sseToolCalls([3]string{"c1", "flood", `{}`}),
		sseText("看完了。"),
	}}
	session := newTestSession(t, model, protocol.ApprovalBypass)
	register(t, session, tools.Tool{
		Name: "flood", Effect: tools.EffectRead,
		Handler: func(context.Context, json.RawMessage, *tools.Env) (string, error) { return big, nil },
	})

	emitter := &recordingEmitter{approve: true}
	session.RunTurn(context.Background(), "t1", "读", nil, nil, emitter)

	var recorded string
	for _, message := range session.messages {
		if message.Role == llm.RoleTool {
			recorded = message.Content
		}
	}
	if len(recorded) >= len(big) {
		t.Fatalf("进历史的工具结果应当被截断，实际 %d 字节", len(recorded))
	}
	if !strings.Contains(recorded, "已截断") {
		t.Errorf("截断要留下痕迹，否则模型会把残缺内容当完整的：%q", recorded[:80])
	}
	// 界面上拿到的是工具的原始输出，截断只对模型生效。
	if !emitter.find(protocol.NotifyItemCompleted, `"toolName":"flood"`) {
		t.Errorf("工具条目应当照常发给宿主：%v", emitter.methods())
	}
}

func TestTruncateForHistoryKeepsBothEnds(t *testing.T) {
	text := "开头标记" + strings.Repeat("填充内容 padding ", 8000) + "结尾标记"

	got := truncateForHistory(text)

	if !strings.HasPrefix(got, "开头标记") {
		t.Errorf("头部要保留——命令的开头说明它在做什么：%q", got[:40])
	}
	if !strings.HasSuffix(got, "结尾标记") {
		t.Errorf("尾部要保留——结果和报错都在那儿：%q", got[len(got)-40:])
	}
	if len(got) > maxToolOutputBytes+256 {
		t.Errorf("截断后仍然超出预算：%d 字节", len(got))
	}
	if !strings.ContainsRune(got, '…') {
		t.Errorf("没有截断标记：%q", got[:80])
	}
}

// register 把一个测试用工具挂进会话。
func register(t *testing.T, session *Session, tool tools.Tool) {
	t.Helper()
	if tool.Schema == nil {
		tool.Schema = json.RawMessage(`{"type":"object","properties":{}}`)
	}
	if err := session.registry.Register(tool); err != nil {
		t.Fatal(err)
	}
}

func TestConfigureSwitchesModelForNextSampling(t *testing.T) {
	model := &fakeModel{script: []string{sseText("第一个模型"), sseText("第二个模型")}}
	session := newTestSession(t, model, protocol.ApprovalBypass)

	session.RunTurn(context.Background(), "t1", "你好", nil, nil, &recordingEmitter{approve: true})
	if err := session.Configure(protocol.ModelConfig{Model: "another-model"}); err != nil {
		t.Fatalf("configure: %v", err)
	}
	session.RunTurn(context.Background(), "t2", "再说一次", nil, nil, &recordingEmitter{approve: true})

	if got := model.requests[0]["model"]; got != "fake" {
		t.Errorf("第一轮应当用原模型，实际 %v", got)
	}
	if got := model.requests[1]["model"]; got != "another-model" {
		t.Errorf("切换后应当用新模型，实际 %v", got)
	}
	// 只给模型名时端点要沿用，否则宿主每次都得把端点再带一遍。
	if session.Model().BaseURL == "" {
		t.Error("BaseURL 应当沿用原会话的，不该被清空")
	}
}

func TestConfigureRejectsEmptyModel(t *testing.T) {
	session := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	before := session.Model().Model

	if err := session.Configure(protocol.ModelConfig{Model: "  "}); err == nil {
		t.Fatal("空模型名应当被拒绝")
	}
	if session.Model().Model != before {
		t.Errorf("拒绝之后配置不该被改动：%q", session.Model().Model)
	}
}

func TestHistoryRebuildsTimeline(t *testing.T) {
	model := &fakeModel{script: []string{
		sseToolCalls([3]string{"c1", "read_file", `{"path":"a.txt"}`}),
		sseText("读完了。"),
	}}
	session := newTestSession(t, model, protocol.ApprovalBypass)
	register(t, session, tools.Tool{
		Name: "read_file_stub", Effect: tools.EffectRead,
		Handler: func(context.Context, json.RawMessage, *tools.Env) (string, error) { return "内容", nil },
	})

	session.RunTurn(context.Background(), "t1", "看看 a.txt", nil, nil, &recordingEmitter{approve: true})

	items := session.History()

	// 系统提示词是内核拼的，不是对话的一部分，显示出来只会让用户困惑。
	for _, item := range items {
		if strings.Contains(item.Text, "你运行在用户的本机电脑上") {
			t.Fatalf("系统提示词不该出现在时间线里：%+v", item)
		}
	}

	var kinds []string
	for _, item := range items {
		kinds = append(kinds, string(item.Kind))
	}
	// 每条 assistant 消息还原成一个采样步骤，排在它引发的工具之前——
	// 执行过程的形状是「采样 → 它决定调的那几个工具 → 再采样」。
	want := []string{"userMessage", "llm", "toolCall", "llm", "agentMessage"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("时间线顺序应当是 %v，实际 %v", want, kinds)
	}
	if items[2].ToolName != "read_file" {
		t.Errorf("工具步骤应当带工具名，实际 %q", items[2].ToolName)
	}
	if items[2].ToolResult == "" {
		t.Error("工具步骤应当回填结果")
	}

	// id 必须稳定：宿主按 id 去重，每次还原换一批会导致切走再切回来条目翻倍。
	again := session.History()
	for i := range items {
		if items[i].ID != again[i].ID {
			t.Fatalf("同一份历史两次还原的 id 应当一致：%q vs %q", items[i].ID, again[i].ID)
		}
	}
}

func TestHistoryMarksUnansweredToolCallAsFailed(t *testing.T) {
	session := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	session.appendMessage(llm.Message{Role: llm.RoleUser, Content: "干活"})
	session.appendMessage(llm.Message{
		Role:      llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{{ID: "c1", Name: "run_command"}},
	})

	items := session.History()

	if len(items) != 3 {
		t.Fatalf("期望用户消息 + 采样步骤 + 工具步骤三条，实际 %d 条", len(items))
	}
	// 没有结果说明那次调用没跑完。留空会看起来像"执行成功但没返回"。
	if !items[2].ToolFailed {
		t.Error("没有结果的工具调用应当标成失败")
	}
}

func TestSkillsAreListedInPromptAndLoadableOnDemand(t *testing.T) {
	skillsDir := t.TempDir()
	dir := filepath.Join(skillsDir, "daily-report")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: 销售日报\ndescription: 需要生成或核对每日销售流水报表时用\n---\n\n先拉昨日流水，再比对上月同期。"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	model := &fakeModel{script: []string{
		sseToolCalls([3]string{"c1", "load_skill", `{"name":"销售日报"}`}),
		sseText("照技能说明办。"),
	}}
	server := httptest.NewServer(http.HandlerFunc(model.handler))
	t.Cleanup(server.Close)
	session, err := New(context.Background(), "test", protocol.SessionStartParams{
		Model:          protocol.ModelConfig{BaseURL: server.URL, Model: "fake"},
		Workdir:        t.TempDir(),
		ApprovalPolicy: protocol.ApprovalBypass,
		SkillDirs:      []string{dir},
	}, StaticKey("sk-test"))
	if err != nil {
		t.Fatal(err)
	}
	session.backoff = func(int) time.Duration { return 0 }
	t.Cleanup(session.Close)

	if got := session.Skills(); len(got) != 1 || got[0] != "销售日报" {
		t.Fatalf("会话应当挂上技能，实际 %v", got)
	}

	emitter := &recordingEmitter{approve: true}
	session.RunTurn(context.Background(), "t1", "帮我做日报", nil, nil, emitter)

	// 提示词里只放名字与说明，正文不能随轮次一直带着走——十几个技能的正文
	// 加起来能有几万 token。
	system := session.messages[0].Content
	if !strings.Contains(system, "销售日报") ||
		!strings.Contains(system, "需要生成或核对每日销售流水报表时用") {
		t.Errorf("系统提示词应当列出技能名与用途：%q", system)
	}
	if strings.Contains(system, "先拉昨日流水") {
		t.Errorf("技能正文不该进系统提示词：%q", system)
	}

	// 正文通过 load_skill 拿到。
	var toolResult string
	for _, message := range session.messages {
		if message.Role == llm.RoleTool {
			toolResult = message.Content
		}
	}
	if !strings.Contains(toolResult, "先拉昨日流水") {
		t.Errorf("load_skill 应当返回技能正文，实际 %q", toolResult)
	}
	// 带上目录：技能正文常引用同目录下的脚本或模板。
	if !strings.Contains(toolResult, dir) {
		t.Errorf("load_skill 的结果应当带上技能目录，实际 %q", toolResult)
	}
}

func TestNoSkillsMeansNoLoadSkillTool(t *testing.T) {
	// 给模型一个"永远返回没有技能"的工具，只会让它反复去试。
	session := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	for _, name := range session.Tools() {
		if name == "load_skill" {
			t.Fatal("没有技能时不该注册 load_skill")
		}
	}
}

func TestMemoryGoesIntoPromptAndRememberAppends(t *testing.T) {
	memoryFile := filepath.Join(t.TempDir(), "memory.md")
	if err := os.WriteFile(memoryFile, []byte("- (2026-09-01) 用户习惯用 Go，不要给 Python 示例\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	model := &fakeModel{script: []string{
		// 没写 scope：有工作区时默认记进工作区，项目的事不该污染全局。
		sseToolCalls([3]string{"c1", "remember", `{"text":"这个项目的测试用 make check"}`}),
		sseToolCalls([3]string{"c2", "remember", `{"text":"用户偏好中文回复","scope":"global"}`}),
		sseText("记住了。"),
	}}
	server := httptest.NewServer(http.HandlerFunc(model.handler))
	t.Cleanup(server.Close)
	workdir := t.TempDir()
	session, err := New(context.Background(), "test", protocol.SessionStartParams{
		Model:          protocol.ModelConfig{BaseURL: server.URL, Model: "fake"},
		Workdir:        workdir,
		ApprovalPolicy: protocol.ApprovalBypass,
		MemoryFile:     memoryFile,
	}, StaticKey("sk-test"))
	if err != nil {
		t.Fatal(err)
	}
	session.backoff = func(int) time.Duration { return 0 }
	t.Cleanup(session.Close)

	// 已有的记忆原样进系统提示词——会话上下文压缩时压的是对话历史，不动这一段。
	system := session.messages[0].Content
	if !strings.Contains(system, "用户习惯用 Go") {
		t.Errorf("长期记忆应当进系统提示词：%q", system)
	}

	session.RunTurn(context.Background(), "t1", "记一下", nil, nil, &recordingEmitter{approve: true})

	saved, err := os.ReadFile(memoryFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "make check") {
		t.Errorf("项目的事不该进全局记忆：%q", saved)
	}
	if !strings.Contains(string(saved), "用户偏好中文回复") {
		t.Errorf("scope=global 的应落到全局文件：%q", saved)
	}
	// 旧的不能被覆盖掉。
	if !strings.Contains(string(saved), "用户习惯用 Go") {
		t.Errorf("追加不该抹掉已有记忆：%q", saved)
	}
	local, err := os.ReadFile(filepath.Join(workdir, ".aiclaw", "memory.md"))
	if err != nil {
		t.Fatalf("工作区记忆文件应当建出来：%v", err)
	}
	if !strings.Contains(string(local), "make check") {
		t.Errorf("默认应记进工作区记忆：%q", local)
	}
	// 两层都要进提示词，且分得清。
	if prompt := session.Memory(); !strings.Contains(prompt, "【本工作区的记忆】") || !strings.Contains(prompt, "make check") {
		t.Errorf("提示词里应带上工作区记忆：%q", prompt)
	}
}

func TestWorkspaceMemoryFollowsTheWorkspace(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	_ = os.MkdirAll(filepath.Join(second, ".aiclaw"), 0o755)
	_ = os.WriteFile(filepath.Join(second, ".aiclaw", "memory.md"), []byte("- (2026-09-01) 这个项目用 pnpm\n"), 0o600)
	model := &fakeModel{}
	server := httptest.NewServer(http.HandlerFunc(model.handler))
	t.Cleanup(server.Close)
	session, err := New(context.Background(), "test", protocol.SessionStartParams{
		Model: protocol.ModelConfig{BaseURL: server.URL, Model: "fake"}, Workdir: first,
	}, StaticKey("sk-test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(session.Close)
	if strings.Contains(session.Memory(), "pnpm") {
		t.Error("第一个工作区没有记忆，不该看到第二个的")
	}
	if err := session.SetWorkspace(second); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(session.Memory(), "pnpm") || !strings.Contains(session.messages[0].Content, "pnpm") {
		t.Error("换到第二个工作区后，它的记忆应进提示词")
	}
}

func TestNoMemoryTargetMeansNoRememberTool(t *testing.T) {
	// 既没配全局记忆文件、也没有工作区时，不该给模型一个写不进任何地方的工具。
	model := &fakeModel{}
	server := httptest.NewServer(http.HandlerFunc(model.handler))
	t.Cleanup(server.Close)
	session, err := New(context.Background(), "test", protocol.SessionStartParams{
		Model: protocol.ModelConfig{BaseURL: server.URL, Model: "fake"},
	}, StaticKey("sk-test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(session.Close)
	for _, name := range session.Tools() {
		if name == "remember" {
			t.Fatal("没有任何记忆落点时不该注册 remember")
		}
	}
	// 只有工作区也算有落点：项目的事有地方记。
	withWorkdir := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	if !strings.Contains(strings.Join(withWorkdir.Tools(), ","), "remember") {
		t.Error("有工作区时应注册 remember")
	}
}

func TestRememberIsGatedByApproval(t *testing.T) {
	memoryFile := filepath.Join(t.TempDir(), "memory.md")
	model := &fakeModel{script: []string{
		sseToolCalls([3]string{"c1", "remember", `{"text":"不该被写进去","scope":"global"}`}),
		sseText("好的。"),
	}}
	server := httptest.NewServer(http.HandlerFunc(model.handler))
	t.Cleanup(server.Close)
	session, err := New(context.Background(), "test", protocol.SessionStartParams{
		Model:   protocol.ModelConfig{BaseURL: server.URL, Model: "fake"},
		Workdir: t.TempDir(),
		// on-write：写全局长期记忆要问。这条会一直跟着以后每个会话，
		// 不该让模型自己决定。
		ApprovalPolicy: protocol.ApprovalOnWrite,
		MemoryFile:     memoryFile,
	}, StaticKey("sk-test"))
	if err != nil {
		t.Fatal(err)
	}
	session.backoff = func(int) time.Duration { return 0 }
	t.Cleanup(session.Close)

	emitter := &recordingEmitter{approve: false}
	session.RunTurn(context.Background(), "t1", "记一下", nil, nil, emitter)

	if len(emitter.approvals) == 0 {
		t.Fatal("写长期记忆应当弹审批")
	}
	if _, err := os.Stat(memoryFile); !os.IsNotExist(err) {
		t.Error("审批被拒之后不该留下记忆文件")
	}
}

// ---------- computer use ----------

func computerSession(t *testing.T, model *fakeModel, policy protocol.ApprovalPolicy) *Session {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(model.handler))
	t.Cleanup(server.Close)
	session, err := New(context.Background(), "test", protocol.SessionStartParams{
		Model:             protocol.ModelConfig{BaseURL: server.URL, Model: "fake"},
		Workdir:           t.TempDir(),
		ApprovalPolicy:    policy,
		EnableComputerUse: true,
	}, StaticKey("sk-test"))
	if err != nil {
		t.Fatal(err)
	}
	session.backoff = func(int) time.Duration { return 0 }
	t.Cleanup(session.Close)
	return session
}

func TestComputerToolsOnlyExistWhenEnabled(t *testing.T) {
	// 默认关。这是权限最大的一组工具，不该因为忘了配就挂上去。
	off := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	for _, name := range off.Tools() {
		if strings.HasPrefix(name, "computer_") {
			t.Fatalf("没开 computer use 时不该有 %s", name)
		}
	}

	on := computerSession(t, &fakeModel{}, protocol.ApprovalBypass)
	var found int
	for _, name := range on.Tools() {
		if strings.HasPrefix(name, "computer_") {
			found++
		}
	}
	if found == 0 {
		t.Fatal("开了之后应当挂上 computer 工具")
	}
}

func TestScreenshotBecomesAnImageMessageNotAToolResult(t *testing.T) {
	// Chat Completions 的 tool 消息必须是纯字符串，图没地方放；
	// 模型看不到截图的话 computer use 只是在瞎点。所以图要作为紧随其后的
	// 一条 user 消息送进去。
	png := []byte{0x89, 0x50, 0x4e, 0x47}
	model := &fakeModel{script: []string{
		sseToolCalls([3]string{"c1", "computer_screenshot", `{}`}),
		sseText("我看到了。"),
	}}
	session := computerSession(t, model, protocol.ApprovalBypass)

	emitter := &recordingEmitter{approve: true, computerOK: true}
	emitter.computerResult = protocol.ComputerResult{
		Text:        "已截屏。屏幕逻辑尺寸 1920×1080",
		ImageBase64: base64.StdEncoding.EncodeToString(png),
		Width:       1920, Height: 1080,
	}
	session.RunTurn(context.Background(), "t1", "看看屏幕", nil, nil, emitter)

	var toolMessage, imageMessage *llm.Message
	for i := range session.messages {
		switch session.messages[i].Role {
		case llm.RoleTool:
			toolMessage = &session.messages[i]
		case llm.RoleUser:
			if len(session.messages[i].Images) > 0 {
				imageMessage = &session.messages[i]
			}
		}
	}
	if toolMessage == nil {
		t.Fatal("应当有一条工具结果消息")
	}
	if len(toolMessage.Images) != 0 {
		t.Error("图不能挂在工具结果上——那条必须是纯字符串")
	}
	if imageMessage == nil {
		t.Fatal("截屏之后应当补一条带图的 user 消息")
	}
	if len(imageMessage.Images) != 1 || string(imageMessage.Images[0]) != string(png) {
		t.Errorf("图片没有原样带过去：%v", imageMessage.Images)
	}
	// 屏幕尺寸要给模型：它得靠这个把图上的位置换算成点击坐标。
	if !strings.Contains(toolMessage.Content, "1920") {
		t.Errorf("工具结果里应当带上屏幕尺寸：%q", toolMessage.Content)
	}
}

func TestComputerActionsAreGatedByApproval(t *testing.T) {
	// 点一下鼠标可能是「点开设置」，也可能是「确认转账」，从坐标上看不出区别。
	model := &fakeModel{script: []string{
		sseToolCalls([3]string{"c1", "computer_click", `{"x":100,"y":200}`}),
		sseText("好的。"),
	}}
	session := computerSession(t, model, protocol.ApprovalOnWrite)

	emitter := &recordingEmitter{approve: false, computerOK: true}
	session.RunTurn(context.Background(), "t1", "点一下", nil, nil, emitter)

	if len(emitter.approvals) == 0 {
		t.Fatal("点击应当弹审批")
	}
	if len(emitter.computer) != 0 {
		t.Errorf("审批被拒之后不该真去点：%v", emitter.computer)
	}
}

func TestScreenshotDoesNotRequireApprovalUnderOnWrite(t *testing.T) {
	// 截屏只是看，不改变任何东西。每次看都要点一下确认会让这功能没法用。
	model := &fakeModel{script: []string{
		sseToolCalls([3]string{"c1", "computer_screenshot", `{}`}),
		sseText("看到了。"),
	}}
	session := computerSession(t, model, protocol.ApprovalOnWrite)

	emitter := &recordingEmitter{approve: false, computerOK: true}
	emitter.computerResult = protocol.ComputerResult{Text: "已截屏"}
	session.RunTurn(context.Background(), "t1", "看看", nil, nil, emitter)

	if len(emitter.approvals) != 0 {
		t.Errorf("截屏不该弹审批：%+v", emitter.approvals)
	}
	if len(emitter.computer) != 1 {
		t.Fatalf("应当真的去截了一次，实际 %d 次", len(emitter.computer))
	}
}

func TestClickRejectsMissingOrNegativeCoordinates(t *testing.T) {
	model := &fakeModel{script: []string{
		sseToolCalls(
			[3]string{"c1", "computer_click", `{"x":10}`},
			[3]string{"c2", "computer_click", `{"x":-1,"y":5}`},
		),
		sseText("参数错了。"),
	}}
	session := computerSession(t, model, protocol.ApprovalBypass)

	emitter := &recordingEmitter{approve: true, computerOK: true}
	session.RunTurn(context.Background(), "t1", "点", nil, nil, emitter)

	if len(emitter.computer) != 0 {
		t.Errorf("参数不合法时不该发出屏幕操作：%v", emitter.computer)
	}
	var failures int
	for _, message := range session.messages {
		if message.Role == llm.RoleTool && strings.HasPrefix(message.Content, "错误：") {
			failures++
		}
	}
	if failures != 2 {
		t.Errorf("两次都该以错误回给模型，实际 %d 次", failures)
	}
}

func TestComputerFailureIsFedBackNotFatal(t *testing.T) {
	// 宿主拒绝（没授权、最前面是自己）要让模型看见原因并改做法，
	// 不是让整轮失败。
	model := &fakeModel{script: []string{
		sseToolCalls([3]string{"c1", "computer_click", `{"x":10,"y":10}`}),
		sseText("那我换个办法。"),
	}}
	session := computerSession(t, model, protocol.ApprovalBypass)

	emitter := &recordingEmitter{approve: true} // computerOK=false → 宿主报错
	session.RunTurn(context.Background(), "t1", "点", nil, nil, emitter)

	if !emitter.find(protocol.NotifyItemCompleted, "那我换个办法。") {
		t.Errorf("模型应当收到失败原因后继续：%v", emitter.methods())
	}
	if emitter.find(protocol.NotifyTurnCompleted, `"error"`) {
		t.Error("屏幕操作失败不该让整轮失败")
	}
}

// ---------- MCP 工具的审批按只读与否分 ----------

// fakeMCPServer 是一个最小 MCP server 子进程的替身：用 claw-agent 自己当
// server 跑不现实，所以直接往注册表里塞工具，验的是 Effect 与审批的关系。
func mountFakeMCPTool(t *testing.T, session *Session, name string, readOnly bool) {
	t.Helper()
	effect := tools.EffectExternal
	if readOnly {
		effect = tools.EffectRead
	}
	err := session.registry.Register(tools.Tool{
		Name: name, Effect: effect,
		Schema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(ctx context.Context, args json.RawMessage, env *tools.Env) (string, error) {
			if err := env.RequestApproval(
				ctx, effect, protocol.ApprovalTool, "调用工具 "+name, string(args), "",
			); err != nil {
				return "", err
			}
			return "结果", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestReadOnlyMCPToolSkipsApproval(t *testing.T) {
	// 查一条语料库定义每次都要点确认，会把用户训练成闭眼点「允许」——
	// 那比少问一次危险。
	model := &fakeModel{script: []string{
		sseToolCalls([3]string{"c1", "corpus_lookup", `{}`}),
		sseText("查到了。"),
	}}
	session := newTestSession(t, model, protocol.ApprovalOnWrite)
	mountFakeMCPTool(t, session, "corpus_lookup", true)

	emitter := &recordingEmitter{approve: false}
	session.RunTurn(context.Background(), "t1", "查一下", nil, nil, emitter)

	if len(emitter.approvals) != 0 {
		t.Errorf("只读工具不该弹审批：%+v", emitter.approvals)
	}
	if !emitter.find(protocol.NotifyItemCompleted, "查到了。") {
		t.Errorf("应当照常拿到结果：%v", emitter.methods())
	}
}

func TestWriteMCPToolStillNeedsApproval(t *testing.T) {
	// 会改外部系统状态的要问。没声明只读的也落在这一档——
	// 不确定的时候要问。
	model := &fakeModel{script: []string{
		sseToolCalls([3]string{"c1", "create_order", `{}`}),
		sseText("被拒了。"),
	}}
	session := newTestSession(t, model, protocol.ApprovalOnWrite)
	mountFakeMCPTool(t, session, "create_order", false)

	emitter := &recordingEmitter{approve: false}
	session.RunTurn(context.Background(), "t1", "建单", nil, nil, emitter)

	if len(emitter.approvals) != 1 {
		t.Fatalf("写类工具应当弹一次审批，实际 %d 次", len(emitter.approvals))
	}
	var refused bool
	for _, message := range session.messages {
		if message.Role == llm.RoleTool && strings.Contains(message.Content, "拒绝") {
			refused = true
		}
	}
	if !refused {
		t.Error("拒绝的原因应当回给模型")
	}
}

func TestTrustedServerSkipsApprovalEvenForWriteTools(t *testing.T) {
	// 可信 server 的工具一律不问，哪怕那个工具会改外部系统状态：准入已经在服务侧
	// 按当前员工判过了。这是一条策略，不是把事实改掉——工具自己的
	// readOnlyHint 保持如实，客户端还要靠它判断第三方 server。
	model := &fakeModel{script: []string{
		sseToolCalls([3]string{"c1", "claw__create_order", `{}`}),
		sseText("建好了。"),
	}}
	session := newTestSession(t, model, protocol.ApprovalOnWrite)
	// 模拟挂载时 Trusted=true 的效果：Effect 落到 read。
	mountFakeMCPTool(t, session, "claw__create_order", true)

	emitter := &recordingEmitter{approve: false}
	session.RunTurn(context.Background(), "t1", "建单", nil, nil, emitter)

	if len(emitter.approvals) != 0 {
		t.Errorf("可信 server 的工具不该弹审批：%+v", emitter.approvals)
	}
	if !emitter.find(protocol.NotifyItemCompleted, "建好了。") {
		t.Errorf("应当照常执行：%v", emitter.methods())
	}
}

func TestMCPToolEffectDecision(t *testing.T) {
	readOnly := true
	notReadOnly := false
	cases := []struct {
		name    string
		trusted bool
		def     mcpclient.ToolDef
		want    tools.Effect
		why     string
	}{
		{
			name: "可信 server：不问", trusted: true,
			def:  mcpclient.ToolDef{Annotations: &mcpclient.ToolAnnotations{ReadOnlyHint: &notReadOnly}},
			want: tools.EffectRead,
			why:  "准入已经在服务侧按人判过，再问一遍只是噪音",
		},
		{
			name: "第三方且声明只读：不问", trusted: false,
			def:  mcpclient.ToolDef{Annotations: &mcpclient.ToolAnnotations{ReadOnlyHint: &readOnly}},
			want: tools.EffectRead,
			why:  "它不改任何东西",
		},
		{
			name: "第三方且声明会改东西：要问", trusted: false,
			def:  mcpclient.ToolDef{Annotations: &mcpclient.ToolAnnotations{ReadOnlyHint: &notReadOnly}},
			want: tools.EffectExternal,
		},
		{
			name: "第三方且什么都没声明：要问", trusted: false,
			def:  mcpclient.ToolDef{},
			want: tools.EffectExternal,
			why:  "副作用未知就按有副作用处理——这是最重要的一条默认",
		},
	}
	for _, c := range cases {
		if got := mcpToolEffect(c.trusted, c.def); got != c.want {
			t.Errorf("%s：期望 %s，实际 %s（%s）", c.name, c.want, got, c.why)
		}
	}
}

func TestTrustedServerSkipsApprovalUnderStrictProfile(t *testing.T) {
	// 严格档位（always）下「所有有副作用的操作都先确认」，但内部平台能力不在其内：
	// 知识库检索、语料库查询这类工具是内部平台按当前员工的权限授出来的，准入
	// 已经判过。换档位不该把它们又变回每次都问——严格档位是给本机那些
	// 没人替你判过的操作用的。
	//
	// 这条与 on-write 那条分开写：needsApproval 对两个档位走的是不同分支，
	// 只测一个的话另一个改坏了不会有人发现。
	model := &fakeModel{script: []string{
		sseToolCalls([3]string{"c1", "knowledge__knowledge_search", `{"query":"示例公司"}`}),
		sseText("查到了。"),
	}}
	session := newTestSession(t, model, protocol.ApprovalAlways)
	mountFakeMCPTool(t, session, "knowledge__knowledge_search", true)

	emitter := &recordingEmitter{approve: false}
	session.RunTurn(context.Background(), "t1", "查一下", nil, nil, emitter)

	if len(emitter.approvals) != 0 {
		t.Errorf("严格档位下可信 server 的工具也不该弹审批：%+v", emitter.approvals)
	}
	if !emitter.find(protocol.NotifyItemCompleted, "查到了。") {
		t.Errorf("应当照常执行：%v", emitter.methods())
	}
}

func TestHistoryRestoresSamplingStepsAroundTools(t *testing.T) {
	// 重开一个会话时，执行步骤必须还是「采样 → 那几个工具 → 采样」。
	// 早先历史里只有工具，于是同一轮对话跑着的时候有 5 步、切走再切回来
	// 只剩 3 步，而少掉的正是「谁决定调这些工具」。
	session := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	session.appendMessage(llm.Message{Role: llm.RoleUser, Content: "查一下这家公司"})
	session.appendMessage(llm.Message{
		Role: llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: "knowledge__knowledge_search"},
			{ID: "c2", Name: "corpus__corpus_1"},
			{ID: "c3", Name: "corpus__corpus_2"},
		},
	})
	for _, id := range []string{"c1", "c2", "c3"} {
		session.appendMessage(llm.Message{Role: llm.RoleTool, ToolCallID: id, Content: "命中"})
	}
	session.appendMessage(llm.Message{Role: llm.RoleAssistant, Content: "查到了。"})

	items := session.History()

	var kinds []string
	for _, item := range items {
		kinds = append(kinds, string(item.Kind))
	}
	want := "userMessage,llm,toolCall,toolCall,toolCall,llm,agentMessage"
	if strings.Join(kinds, ",") != want {
		t.Fatalf("期望 %s，实际 %s", want, strings.Join(kinds, ","))
	}

	// 序号是轮内连续的：界面上写的是「这一轮的第几步」。
	var seqs []int
	for _, item := range items {
		if item.Kind == protocol.ItemLLM || item.Kind == protocol.ItemToolCall {
			seqs = append(seqs, item.Seq)
		}
	}
	for i, seq := range seqs {
		if seq != i+1 {
			t.Fatalf("步骤序号应当是 1..5，实际 %v", seqs)
		}
	}

	// 第一次采样决定了 3 个工具调用，第二次一个都没有。
	if items[1].ToolCalls != 3 {
		t.Errorf("首次采样应当记下 3 个工具调用，实际 %d", items[1].ToolCalls)
	}
	if items[5].Round != 2 {
		t.Errorf("第二次采样应当是 R2，实际 R%d", items[5].Round)
	}

	// 耗时还原不出来（存档里只有给模型看的消息），**宁可不显示也不要编**。
	if items[1].DurationMS != 0 || items[1].StartedAt != 0 {
		t.Errorf("历史采样步骤不该带时间：%+v", items[1])
	}
}

func TestHistorySeqResetsEachTurn(t *testing.T) {
	// 序号跨轮累加的话，第二轮会从 4 开始，而界面上写的是「这一轮的第几步」。
	session := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	session.appendMessage(llm.Message{Role: llm.RoleUser, Content: "一"})
	session.appendMessage(llm.Message{
		Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "read_file"}},
	})
	session.appendMessage(llm.Message{Role: llm.RoleTool, ToolCallID: "c1", Content: "内容"})
	session.appendMessage(llm.Message{Role: llm.RoleAssistant, Content: "好了。"})
	session.appendMessage(llm.Message{Role: llm.RoleUser, Content: "二"})
	session.appendMessage(llm.Message{Role: llm.RoleAssistant, Content: "再好了。"})

	items := session.History()

	var second protocol.Item
	for _, item := range items {
		if item.Kind == protocol.ItemLLM {
			second = item // 最后一个 llm 就是第二轮那次
		}
	}
	if second.Seq != 1 || second.Round != 1 {
		t.Errorf("第二轮的第一次采样应当是 seq=1 round=1，实际 seq=%d round=%d", second.Seq, second.Round)
	}
}

func TestLongSkillDescriptionsAreTruncatedInPrompt(t *testing.T) {
	// 技能现在是从 Claude Code、Codex、npm 等处一并发现的，一台机器上二十来个
	// 很正常，而有些技能的 description 写了七八百字。原样铺进提示词就是几千
	// token，每一轮都在付——而判断「用不用得上」根本不需要那么多字。
	skillsDir := t.TempDir()
	dir := filepath.Join(skillsDir, "verbose")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("很长的说明", 300) // 1500 个字符
	body := "---\nname: 啰嗦技能\ndescription: " + long + "\n---\n\n正文"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	session := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	session.loadSkills([]string{dir})

	prompt := buildSystemPrompt(session.config, session.registry, session.skills, "")
	line := ""
	for _, candidate := range strings.Split(prompt, "\n") {
		if strings.HasPrefix(candidate, "- 啰嗦技能：") {
			line = candidate
		}
	}
	if line == "" {
		t.Fatalf("技能没进提示词：%s", prompt)
	}
	if len([]rune(line)) > 260 {
		t.Errorf("说明应当被截断，实际这一行 %d 个字符", len([]rune(line)))
	}
	if !strings.HasSuffix(line, "…") {
		t.Errorf("截断了就要有省略号，让模型知道还有下文：%q", line)
	}
}

// 输出撞上上限时工具参数会停在半句话里。错误要说「被截断」，不能只说「不是合法 JSON」——
// 后者让模型去改写法，改几轮都没用。
func TestTruncatedToolArgumentsAreCalledOut(t *testing.T) {
	cut := `{"path":"out.txt","content":"第一段写到这里就被切掉了，后面还有很多`
	model := &fakeModel{script: []string{
		sseToolCalls([3]string{"c1", "write_file", cut}),
		sseText("好。"),
	}}
	session := newTestSession(t, model, protocol.ApprovalBypass)
	emitter := &recordingEmitter{approve: true}
	session.RunTurn(context.Background(), "t1", "写文件", nil, nil, emitter)
	if !emitter.find(protocol.NotifyItemCompleted, "被截断") {
		t.Error("工具结果里应指出参数被截断")
	}
	if !emitter.find(protocol.NotifyItemCompleted, "拆成几次") {
		t.Error("应告诉模型怎么做：拆成几次调用")
	}
}

// finish_reason=length 是截断的权威信号：有工具调用时最后那条不执行并说明原因，
// 没有工具调用时给用户一条提示。
func TestLengthFinishReasonIsSurfaced(t *testing.T) {
	cutCall, _ := json.Marshal(map[string]any{
		"choices": []any{map[string]any{
			"delta": map[string]any{"tool_calls": []any{map[string]any{
				"index": 0, "id": "c1", "type": "function",
				// 恰好是合法 JSON：光看结尾判不出截断，只有 finish_reason 知道。
				"function": map[string]any{"name": "write_file", "arguments": `{"path":"a.txt","content":"半截"}`},
			}}},
			"finish_reason": "length",
		}},
	})
	cutText, _ := json.Marshal(map[string]any{
		"choices": []any{map[string]any{"delta": map[string]any{"content": "说到一半"}, "finish_reason": "length"}},
	})
	model := &fakeModel{script: []string{
		"data: " + string(cutCall) + "\n\ndata: [DONE]\n\n",
		"data: " + string(cutText) + "\n\ndata: [DONE]\n\n",
	}}
	session := newTestSession(t, model, protocol.ApprovalBypass)
	emitter := &recordingEmitter{approve: true}
	session.RunTurn(context.Background(), "t1", "写", nil, nil, emitter)

	if !emitter.find(protocol.NotifyItemCompleted, "finish_reason=length") {
		t.Error("被截断的工具调用应在结果里说明原因")
	}
	if _, err := os.Stat(filepath.Join(session.Workspace(), "a.txt")); err == nil {
		t.Error("参数不完整的调用不该执行")
	}
	if !emitter.find(protocol.NotifyItemCompleted, "长度上限处被截断") {
		t.Error("回答被截断时应给用户一条提示")
	}
}
