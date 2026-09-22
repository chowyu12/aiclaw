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
命令沙箱。

在它之前，「模型不能乱动这台电脑」全靠两张字符串名单加工具里的路径判断——
防得住「顺手写出这条命令」，防不住有意规避，也防不住注入。而普通命令默认不再
逐条审批之后，`echo x >> ~/.zshrc` 和 write_file 已经不是同一道门了。

所以这一组测试**真的去跑 sandbox-exec**，不是检查我们生成的字符串长什么样：
策略写得对不对，只有内核说了算。
*/

// sandboxEnv 造一个**不在临时目录下**的工作区。
//
// t.TempDir() 不行：它在 TMPDIR 里，而临时目录是策略里明确放开可写的
// （go build / npm / git 都要写临时文件）。拿它当「工作区之外」来验拦截，
// 测的其实是临时目录规则——第一版就是这么写的，结果「越界写」用例
// 报了假绿。/private/var/tmp 是另一块用户可写区域，不在放行名单里。
func sandboxEnv(t *testing.T) (*Env, *Registry, string) {
	t.Helper()
	if !SandboxAvailable() {
		t.Skip("这台机器没有 sandbox-exec（只有 macOS 有）")
	}
	base, err := os.MkdirTemp("/private/var/tmp", "aiclaw-sandbox-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })

	workspace := filepath.Join(base, "ws")
	home := filepath.Join(base, "home")
	outside := filepath.Join(base, "outside")
	for _, dir := range []string{workspace, home, outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	env, _ := newEnv(t, protocol.ApprovalOnWrite, true)
	env.Workspace = workspace
	env.Home = home
	env.Sandbox = true
	return env, fullRegistry(t), outside
}

func TestSandboxAllowsWritingInsideTheWorkspace(t *testing.T) {
	// 先确认沙箱没有把正常干活挡住——挡住了的话，用户看到的是
	// 「某个命令莫名其妙跑不了」，而那种失败没人能从错误信息里看懂。
	env, registry, _ := sandboxEnv(t)
	out, err := call(t, registry, "run_command", `{"command":"echo hi > inside.txt && cat inside.txt"}`, env)
	if err != nil {
		t.Fatalf("工作区内的写不该被拦：%v（输出 %q）", err, out)
	}
	if _, statErr := os.Stat(filepath.Join(env.Workspace, "inside.txt")); statErr != nil {
		t.Errorf("文件没写成：%v", statErr)
	}
}

func TestSandboxBlocksWritingOutsideTheWorkspace(t *testing.T) {
	env, registry, outsideDir := sandboxEnv(t)
	outside := filepath.Join(outsideDir, "escape.txt")
	// 直接用 shell 重定向：这正是绕过 write_file 那套路径判断的方式。
	out, _ := call(t, registry, "run_command", `{"command":"echo x > `+outside+`"}`, env)
	if _, err := os.Stat(outside); err == nil {
		t.Errorf("工作区之外的写应当被内核拦下，实际写成了。输出：%q", out)
	}
	if !strings.Contains(out, "[沙箱]") {
		t.Errorf("被拦时要说清是谁拦的、怎么办，实际输出：%q", out)
	}
}

func TestSandboxBlocksReadingCredentials(t *testing.T) {
	env, registry, _ := sandboxEnv(t)
	secrets := filepath.Join(env.Home, ".ssh")
	if err := os.MkdirAll(secrets, 0o700); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(secrets, "id_rsa")
	if err := os.WriteFile(key, []byte("PRIVATE-KEY-CONTENT"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _ := call(t, registry, "run_command", `{"command":"cat `+key+`"}`, env)
	if strings.Contains(out, "PRIVATE-KEY-CONTENT") {
		t.Errorf("凭据不该读得到，实际输出：%q", out)
	}
}

func TestSandboxLeavesOrdinaryWorkAlone(t *testing.T) {
	// 临时目录要可写：go build / npm / git 全都写临时文件，
	// 不放开的话「正常干活」这条就不成立了。
	env, registry, _ := sandboxEnv(t)
	if _, err := call(t, registry, "run_command", `{"command":"t=$(mktemp) && echo ok > \"$t\" && cat \"$t\""}`, env); err != nil {
		t.Errorf("写临时文件不该被拦：%v", err)
	}
	// 读系统目录照常：编译工具链要读一大堆 /usr、/Library 下的东西。
	if _, err := call(t, registry, "run_command", `{"command":"ls /usr/bin > /dev/null"}`, env); err != nil {
		t.Errorf("读系统目录不该被拦：%v", err)
	}
}

func TestApprovedCommandsRunOutsideTheSandbox(t *testing.T) {
	// 用户点过「允许」的不套沙箱：那正是审批的含义——他看着这条命令批准了它。
	// 这里用一条会触发审批的命令（rm -r 在风险名单上）去写工作区外面。
	env, registry, target := sandboxEnv(t)
	outside := filepath.Join(target, "approved.txt")
	if _, err := call(t, registry, "run_command",
		`{"command":"chmod 755 `+target+` && echo x > `+outside+`"}`, env); err != nil {
		t.Fatalf("审批通过的命令不该被沙箱拦：%v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("审批通过的命令应当照常生效：%v", err)
	}
}

func TestProfileEscapesQuotesInPaths(t *testing.T) {
	// 路径里带引号能把策略截断，后面的 deny 全部失效——而且**不会报错**，
	// 沙箱看起来还在跑。
	profile := buildSandboxProfile(`/tmp/a"b`, "/Users/x", nil, nil)
	if !strings.Contains(profile, `"/tmp/a\"b"`) {
		t.Errorf("路径里的引号没转义：%s", profile)
	}
	if strings.Count(profile, "(deny file-write*)") != 1 {
		t.Errorf("策略结构不对：%s", profile)
	}
}

func TestSandboxOffOnOtherPlatforms(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("这条是给非 macOS 看的")
	}
	if SandboxAvailable() {
		t.Error("只有 macOS 有 sandbox-exec")
	}
}

func TestSandboxHonoursSessionGrants(t *testing.T) {
	// 用户点过「本次会话这个目录都允许」之后，命令也要能写进去——
	// 不同步进策略的话，文件工具能写、命令却被内核拦，那是最让人
	// 摸不着头脑的一种不一致。
	env, registry, outside := sandboxEnv(t)
	env.Grants = func() []string { return []string{outside} }

	target := filepath.Join(outside, "granted.txt")
	if _, err := call(t, registry, "run_command", `{"command":"echo x > `+target+`"}`, env); err != nil {
		t.Fatalf("授权过的目录应当可写：%v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("文件没写成：%v", err)
	}
}

func TestSandboxLetsToolchainCachesThrough(t *testing.T) {
	// 实测踩到的：第一版只放开工作区与临时目录，`go build` 直接跑不了——
	// ~/go/pkg/mod 与 ~/Library/Caches 写不进去，模型只好自己造一套临时
	// GOCACHE 重新下载整个依赖树。那不是挡住了危险操作，是挡住了干活，
	// 而代价全落在用户身上。
	env, registry, _ := sandboxEnv(t)
	for _, relative := range []string{
		filepath.Join("go", "pkg", "mod"),
		filepath.Join("Library", "Caches", "go-build"),
		".cache",
	} {
		dir := filepath.Join(env.Home, relative)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(dir, "probe.txt")
		if _, err := call(t, registry, "run_command", `{"command":"echo x > `+target+`"}`, env); err != nil {
			t.Errorf("%s 应当可写：%v", relative, err)
			continue
		}
		if _, err := os.Stat(target); err != nil {
			t.Errorf("%s 没写成：%v", relative, err)
		}
	}
}

func TestSandboxAllowsKnownHostsButNotKeys(t *testing.T) {
	// ~/.ssh 整个禁读的代价是 git over ssh 直接不可用（实测里模型被迫绕成
	// ssh -o UserKnownHostsFile=/tmp/...）。known_hosts 不是秘密，私钥是。
	env, registry, _ := sandboxEnv(t)
	secrets := filepath.Join(env.Home, ".ssh")
	if err := os.MkdirAll(secrets, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secrets, "known_hosts"), []byte("HOSTKEY-LINE"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secrets, "id_rsa"), []byte("PRIVATE-KEY"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, _ := call(t, registry, "run_command", `{"command":"cat `+filepath.Join(secrets, "known_hosts")+`"}`, env)
	if !strings.Contains(out, "HOSTKEY-LINE") {
		t.Errorf("known_hosts 应当读得到：%q", out)
	}
	out, _ = call(t, registry, "run_command", `{"command":"cat `+filepath.Join(secrets, "id_rsa")+`"}`, env)
	if strings.Contains(out, "PRIVATE-KEY") {
		t.Errorf("私钥仍然不该读得到：%q", out)
	}
}

func TestOnlySSHItselfCanReadPrivateKeys(t *testing.T) {
	/*
		整份策略里最值的一笔。没有它只有两个坏选择：禁读（公司里仓库都是
		git@ 开头的，SSH 的 git 操作全废），或者放开（模型一句 cat 就把私钥
		读进上下文）。seatbelt 的 process-path 过滤让两件事能同时成立。

		这条用例只验「cat 读不到」那一半——另一半（ssh 真的能认证）要连私服，
		不适合放进单测；它是手工验过的：`ssh -T git@github.com` 在同一份
		策略下返回 Welcome。
	*/
	env, registry, _ := sandboxEnv(t)
	secrets := filepath.Join(env.Home, ".ssh")
	if err := os.MkdirAll(secrets, 0o700); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(secrets, "id_ed25519")
	if err := os.WriteFile(key, []byte("PRIVATE-KEY-BODY"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, _ := call(t, registry, "run_command", `{"command":"cat `+key+`"}`, env)
	if strings.Contains(out, "PRIVATE-KEY-BODY") {
		t.Errorf("cat 不该读得到私钥：%q", out)
	}

	// 策略里要按**可执行文件路径**放行，不能按名字——工作区里放一个叫 ssh
	// 的脚本就能拿到私钥。
	profile := buildSandboxProfile(env.Workspace, env.Home, nil, nil)
	if !strings.Contains(profile, `(process-path "/usr/bin/ssh")`) {
		t.Errorf("策略里应当按绝对路径放行 ssh：%s", profile)
	}
}
