package tools

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

/*
常驻 shell 会话。

每条命令都开新 shell 的话，cd / export / source 都不留痕——模型只能把它们和
真正要跑的命令拼成一长串，而它经常忘了拼，于是「明明 cd 过去了怎么还找不到
文件」。这一组钉的就是「状态真的留着」，以及几条会把它变成隐患的边界。
*/

func shellEnv(t *testing.T) (*Env, *Registry) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上不支持常驻会话")
	}
	env, _ := newEnv(t, protocol.ApprovalOnWrite, true)
	env.Shells = NewShellPool()
	t.Cleanup(env.Shells.Close)
	return env, fullRegistry(t)
}

// sessionID 从返回文案里抠出会话 id。
func sessionID(t *testing.T, out string) string {
	t.Helper()
	marker := `session="`
	at := strings.Index(out, marker)
	if at < 0 {
		t.Fatalf("返回里没有会话 id：%q", out)
	}
	rest := out[at+len(marker):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		t.Fatalf("会话 id 没有收尾：%q", out)
	}
	return rest[:end]
}

func TestShellSessionKeepsDirectoryAndVariables(t *testing.T) {
	env, registry := shellEnv(t)
	if err := os.MkdirAll(filepath.Join(env.Workspace, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	first, err := call(t, registry, "run_command", `{"command":"cd sub && export TAG=hello","session":"new"}`, env)
	if err != nil {
		t.Fatalf("第一条：%v", err)
	}
	id := sessionID(t, first)

	second, err := call(t, registry, "run_command", `{"command":"pwd; echo $TAG","session":"`+id+`"}`, env)
	if err != nil {
		t.Fatalf("第二条：%v", err)
	}
	if !strings.Contains(second, "/sub") {
		t.Errorf("cd 应当留着：%q", second)
	}
	if !strings.Contains(second, "hello") {
		t.Errorf("环境变量应当留着：%q", second)
	}
}

func TestOneShotCommandsStayIndependent(t *testing.T) {
	// 不传 session 时还是老样子：每条都是全新的 shell。
	env, registry := shellEnv(t)
	if err := os.MkdirAll(filepath.Join(env.Workspace, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, registry, "run_command", `{"command":"cd sub"}`, env); err != nil {
		t.Fatal(err)
	}
	out, err := call(t, registry, "run_command", `{"command":"pwd"}`, env)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "/sub") {
		t.Errorf("一次性命令不该继承上一条的 cd：%q", out)
	}
}

func TestShellSessionReportsExitCodeAndKeepsGoing(t *testing.T) {
	// 失败的命令也要打印哨兵，否则这次读取会一直等到超时——
	// 所以命令与哨兵之间是 `;` 而不是 `&&`。
	env, registry := shellEnv(t)
	first, err := call(t, registry, "run_command",
		`{"command":"ls /definitely-not-here","session":"new"}`, env)
	if err != nil {
		t.Fatalf("失败的命令不该是工具故障：%v", err)
	}
	if !strings.Contains(first, "退出码") {
		t.Errorf("应当把退出码给模型：%q", first)
	}
	id := sessionID(t, first)
	// 失败之后会话还能继续用——这正是「常驻」的意义。
	out, err := call(t, registry, "run_command", `{"command":"echo still-here","session":"`+id+`"}`, env)
	if err != nil {
		t.Fatalf("失败之后会话应当还在：%v", err)
	}
	if !strings.Contains(out, "still-here") {
		t.Errorf("会话应当继续可用：%q", out)
	}
}

func TestExitEndsTheSessionWithAClearMessage(t *testing.T) {
	// `exit` 结束的是这个 shell 自己。这时候只能如实说会话没了，
	// 别让模型对着一个已经死掉的 id 反复重试。
	env, registry := shellEnv(t)
	first, err := call(t, registry, "run_command", `{"command":"echo alive","session":"new"}`, env)
	if err != nil {
		t.Fatal(err)
	}
	id := sessionID(t, first)

	if _, err := call(t, registry, "run_command", `{"command":"exit 3","session":"`+id+`"}`, env); err == nil ||
		!strings.Contains(err.Error(), "结束") {
		t.Errorf("shell 自己退出时要说清楚，得到 %v", err)
	}
	// 而且这个 id 要立刻作废，不能留着让下一次调用再撞一次。
	if _, err := call(t, registry, "run_command", `{"command":"pwd","session":"`+id+`"}`, env); err == nil ||
		!strings.Contains(err.Error(), "new") {
		t.Errorf("死掉的会话应当提示重开，得到 %v", err)
	}
}

func TestUnknownSessionTellsTheModelToOpenANewOne(t *testing.T) {
	// 说清楚是「这个会话没了」，模型才知道重开一个，而不是反复重试。
	env, registry := shellEnv(t)
	_, err := call(t, registry, "run_command", `{"command":"pwd","session":"sh_deadbeef"}`, env)
	if err == nil || !strings.Contains(err.Error(), "new") {
		t.Errorf("应当提示重开一个会话，得到 %v", err)
	}
}

func TestShellSessionsAreCapped(t *testing.T) {
	// 模型每次都传 "new" 的话，进程会一直堆下去。
	env, registry := shellEnv(t)
	for i := 0; i < maxShellSessions; i++ {
		if _, err := call(t, registry, "run_command", `{"command":"true","session":"new"}`, env); err != nil {
			t.Fatalf("第 %d 个会话：%v", i, err)
		}
	}
	_, err := call(t, registry, "run_command", `{"command":"true","session":"new"}`, env)
	if err == nil || !strings.Contains(err.Error(), "上限") {
		t.Errorf("超过上限应当明确拒绝，得到 %v", err)
	}
}

func TestShellSessionIsSandboxedToo(t *testing.T) {
	// 不套沙箱的话，模型只要改用常驻会话就能绕开它——那等于这层防护不存在。
	if !SandboxAvailable() {
		t.Skip("这台机器没有 sandbox-exec")
	}
	env, registry, outside := sandboxEnv(t)
	env.Shells = NewShellPool()
	t.Cleanup(env.Shells.Close)

	target := filepath.Join(outside, "via-session.txt")
	out, _ := call(t, registry, "run_command", `{"command":"echo x > `+target+`","session":"new"}`, env)
	if _, err := os.Stat(target); err == nil {
		t.Errorf("常驻会话同样不该能写工作区外面。输出：%q", out)
	}
}

func TestClosingThePoolStopsTheShells(t *testing.T) {
	// 用户关了对话，几个 shell 还在后台跑着——那是会被人发现的那种泄漏。
	env, registry := shellEnv(t)
	out, err := call(t, registry, "run_command", `{"command":"echo hi","session":"new"}`, env)
	if err != nil {
		t.Fatal(err)
	}
	id := sessionID(t, out)
	env.Shells.Close()
	if _, err := call(t, registry, "run_command", `{"command":"pwd","session":"`+id+`"}`, env); err == nil {
		t.Error("池关掉之后旧会话不该还能用")
	}
}
