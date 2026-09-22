package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/tools"
)

/*
代码模式接进会话之后的几条。

最要紧的是**审批没有被绕过**：脚本里的每一次工具调用走的还是同一个 handler、
同一个 Env，该弹的框照弹。如果这条不成立，代码模式就成了一条绕过审批的路
——而审批在没有沙箱的平台上是仅剩的闸。
*/

func codeModeSession(t *testing.T, emitter *recordingEmitter) *Session {
	t.Helper()
	session := newTestSession(t, &fakeModel{}, protocol.ApprovalOnWrite)
	session.config.CodeMode = true
	if count := session.installCodeMode(); count == 0 {
		t.Fatal("没有工具被收进 exec")
	}
	return session
}

func runScript(t *testing.T, session *Session, emitter *recordingEmitter, code string) (string, error) {
	t.Helper()
	tool, ok := session.registry.Get("exec")
	if !ok {
		t.Fatal("exec 工具不存在")
	}
	env := &tools.Env{
		Workspace: session.config.Workdir,
		Policy:    session.config.ApprovalPolicy,
		Approve:   emitter.RequestApproval,
		SessionID: session.ID,
		TurnID:    "t1",
	}
	arguments, err := json.Marshal(map[string]string{"code": code})
	if err != nil {
		t.Fatal(err)
	}
	return tool.Handler(context.Background(), arguments, env)
}

func TestCodeModeReplacesTheWholeToolList(t *testing.T) {
	// 这是它存在的理由：工具清单每次请求整份重发、压缩碰不到它。
	session := codeModeSession(t, &recordingEmitter{approve: true})
	names := session.registry.Names()
	if len(names) != 1 || names[0] != "exec" {
		t.Fatalf("应当只剩 exec，实际 %v", names)
	}
	// 而且原来那些工具要在描述里能找到，否则模型不知道能调什么。
	tool, _ := session.registry.Get("exec")
	for _, want := range []string{"read_file", "run_command", "await tools."} {
		if !strings.Contains(tool.Description, want) {
			t.Errorf("描述里缺 %q", want)
		}
	}
}

func TestScriptCanUseTheRealTools(t *testing.T) {
	emitter := &recordingEmitter{approve: true}
	session := codeModeSession(t, emitter)

	out, err := runScript(t, session, emitter, `
		await tools.write_file({ path: "note.txt", content: "hello" });
		const back = await tools.read_file({ path: "note.txt" });
		return "读回来：" + back;
	`)
	if err != nil {
		t.Fatalf("脚本执行失败：%v", err)
	}
	if !strings.Contains(out, "读回来：hello") {
		t.Errorf("输出 = %q", out)
	}
}

func TestScriptStillGoesThroughApproval(t *testing.T) {
	// 脚本里跑危险命令，审批照样要弹——代码模式不能成为绕过审批的路。
	emitter := &recordingEmitter{approve: false}
	session := codeModeSession(t, emitter)

	out, err := runScript(t, session, emitter, `
		try {
			await tools.run_command({ command: "rm -rf build" });
			return "居然执行了";
		} catch (error) {
			return "被拦下了：" + error;
		}
	`)
	if err != nil {
		t.Fatalf("脚本本身不该失败：%v", err)
	}
	if len(emitter.approvals) != 1 {
		t.Fatalf("应当弹一次审批，实际 %d 次", len(emitter.approvals))
	}
	if !strings.Contains(out, "被拦下了") {
		t.Errorf("拒绝之后脚本应当拿到异常：%q", out)
	}
}

func TestLoopInsideOneScriptIsOneToolCallForTheModel(t *testing.T) {
	// 十几次查询写成一段循环，模型只看到最后那几行——这是省上下文的大头。
	emitter := &recordingEmitter{approve: true}
	session := codeModeSession(t, emitter)

	out, err := runScript(t, session, emitter, `
		const names = [];
		for (let i = 0; i < 5; i++) {
			await tools.write_file({ path: "f" + i + ".txt", content: "x".repeat(500) });
			names.push("f" + i);
		}
		return "写了 " + names.length + " 个文件";
	`)
	if err != nil {
		t.Fatalf("脚本执行失败：%v", err)
	}
	if out != "写了 5 个文件" {
		t.Errorf("输出 = %q", out)
	}
	if len(out) > 40 {
		t.Errorf("进上下文的应当只有这一行，实际 %d 字节", len(out))
	}
}

func TestSkillsAndComputerToolsStayOutsideCodeMode(t *testing.T) {
	// 代码模式在技能与 computer use 之前装：读技能、点屏幕不需要写脚本，
	// 把它们也收进去只会让最常用的两件事多绕一层。
	session := newTestSession(t, &fakeModel{}, protocol.ApprovalOnWrite)
	session.config.CodeMode = true
	session.installCodeMode()
	if err := session.registry.Register(tools.Tool{
		Name: "load_skill", Effect: tools.EffectRead,
		Schema:  json.RawMessage(`{"type":"object"}`),
		Handler: func(context.Context, json.RawMessage, *tools.Env) (string, error) { return "", nil },
	}); err != nil {
		t.Fatal(err)
	}
	names := session.registry.Names()
	if len(names) != 2 {
		t.Fatalf("exec 之外还应当能再挂工具，实际 %v", names)
	}
}

func TestStatusTellsTheTruthAboutContextCost(t *testing.T) {
	// 挂载状态那一栏是**唯一**能让用户看见「工具占了多少上下文」的地方。
	// 代码模式下各个 server 那几行「已挂载 N 个工具（约占 X token）」已经
	// 不成立了——那些工具不再作为工具发给模型。不说清楚，那一栏就在说谎。
	session := newTestSession(t, &fakeModel{}, protocol.ApprovalOnWrite)
	session.config.CodeMode = true
	before := session.registry.List()
	count := session.installCodeMode()
	if count == 0 {
		t.Fatal("没有工具被收进去")
	}
	full, compact := codeModeSavings(before, session.registry.List()[0].Description)
	if compact >= full {
		t.Errorf("代码模式应当更省：原来 %d token，现在 %d token", full, compact)
	}
	t.Logf("内置工具：%d token → %d token", full, compact)
}

func TestCodeModeRewritesThePerServerTokenClaim(t *testing.T) {
	// 「已挂载 5 个工具（约占 1.1K token 上下文）」在代码模式下后半句是假的：
	// 那些工具已经不作为工具发给模型了。状态栏是一行一行读的，
	// 只在「代码模式」那一行写一句脚注，用户不会回头把它按到这一行上。
	session := &Session{
		mcpStatus: map[string]string{
			"aihot":   "已挂载 5 个工具（约占 1.1K token 上下文）",
			"broken":  "挂载失败：连不上",
			"partial": "部分工具重名被跳过：search",
		},
		mcpMounted: map[string]int{"aihot": 5, "broken": 0, "partial": 2},
	}
	session.foldStatusIntoExec()

	if strings.Contains(session.mcpStatus["aihot"], "token") {
		t.Fatalf("代码模式下不该再说占多少 token：%q", session.mcpStatus["aihot"])
	}
	if !strings.Contains(session.mcpStatus["aihot"], "5 个工具") {
		t.Fatalf("挂了几个工具仍然要说：%q", session.mcpStatus["aihot"])
	}
	// 失败原因是用户真正要看的，不能被这次重写抹掉。
	if session.mcpStatus["broken"] != "挂载失败：连不上" {
		t.Fatalf("挂载失败的那行被改了：%q", session.mcpStatus["broken"])
	}
	if session.mcpStatus["partial"] != "部分工具重名被跳过：search" {
		t.Fatalf("重名跳过的那行被改了：%q", session.mcpStatus["partial"])
	}
}
