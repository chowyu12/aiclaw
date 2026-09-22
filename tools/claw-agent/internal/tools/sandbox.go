package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

/*
macOS 沙箱：把命令包进 `sandbox-exec` 里跑。

**为什么需要它。** 在这之前，「模型不能乱动这台电脑」全靠两张字符串名单
（危险命令硬拒、风险命令问一句）和工具里的路径判断。名单防的是「模型顺手写出
这条命令」，防不住有意规避，更防不住注入——而且普通命令默认不再逐条审批之后，
`echo x >> ~/.zshrc` 和 `write_file` 已经不是同一道门了。sandbox-exec 是内核级
的，命令怎么绕都绕不过去。

**为什么是这个形状的策略。** 从 `(allow default)` 出发，只关两样：
工作区之外不许写、凭据目录不许读。不从 `(deny default)` 出发，是因为那要把
git / npm / go 需要的每一条路径都列全，列漏一条的表现是「某个命令莫名其妙跑不了」
——而那种失败没人能从错误信息里看懂。现在这个形状挡住的是**破坏与外泄**，
放过的是「正常干活」，与我们已有的审批规则是同一套语义，只是换成内核来执行。

**用户点过「允许」的命令不进沙箱。** 那正是审批的含义：他看着这条命令批准了它。
再用沙箱拦一道，只会让「我明明同意了它却失败」，而用户无从判断是不是自己的问题。

**不可用就不用**（非 macOS、或者 /usr/bin/sandbox-exec 不在）：这一层是额外的
护栏，不是运行的前提；缺了它其余约束照旧。
*/

// sandboxExecPath 写死 /usr/bin，不走 PATH。
//
// 走 PATH 的话，一个能改 PATH 的人就能把 sandbox-exec 换成一个什么都不做的
// 同名脚本——这层防护就静默没了。（这条是照 codex 的做法：能改 /usr/bin 的人
// 本来就已经是 root，再防没有意义。）
const sandboxExecPath = "/usr/bin/sandbox-exec"

// SandboxAvailable 报告这台机器能不能用命令沙箱。
func SandboxAvailable() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	info, err := os.Stat(sandboxExecPath)
	return err == nil && !info.IsDir()
}

/*
buildSandboxProfile 生成 sbpl 策略。

可写的白名单里除了工作区还有临时目录：几乎所有工具链都要写临时文件
（go build、npm、git 都是），不放开的话「正常干活」这条就不成立了。
*/
func buildSandboxProfile(workspace, home string, protected, granted []string) string {
	var builder strings.Builder
	builder.WriteString("(version 1)\n")
	builder.WriteString("(allow default)\n\n")

	builder.WriteString(";; 写：默认不许，只放开工作区与临时目录\n")
	builder.WriteString("(deny file-write*)\n")

	// 临时目录只放开真正的那几个，不放开整个 /var/folders——那下面还有
	// 各种缓存与容器数据，不是「临时文件」。os.TempDir() 认 TMPDIR，
	// macOS 上是 /var/folders/<...>/T 这种每用户目录。
	writable := append([]string{"/tmp", "/private/tmp", realpath(os.TempDir())}, cacheDirs(home)...)
	if trimmed := strings.TrimSpace(workspace); trimmed != "" {
		writable = append(writable, realpath(trimmed))
	}
	// 会话里批准过的目录同样可写：不放进策略的话，用户点了「本次会话都允许」
	// 之后文件工具能写、命令却仍然被内核拦——那是最让人摸不着头脑的一种不一致。
	for _, dir := range granted {
		if trimmed := strings.TrimSpace(dir); trimmed != "" {
			writable = append(writable, realpath(trimmed))
		}
	}
	builder.WriteString("(allow file-write*\n")
	for _, path := range writable {
		fmt.Fprintf(&builder, "  (subpath %s)\n", sbplString(path))
	}
	// /dev/null 与终端设备：命令里 `2>/dev/null` 太常见，挡了会让人以为命令坏了。
	builder.WriteString("  (literal \"/dev/null\")\n")
	builder.WriteString("  (regex #\"^/dev/tty\")\n")
	builder.WriteString("  (literal \"/dev/stdout\")\n")
	builder.WriteString("  (literal \"/dev/stderr\")\n")
	builder.WriteString(")\n\n")

	builder.WriteString(";; 读：只关凭据\n")
	builder.WriteString("(deny file-read*\n")
	for _, path := range protectedSandboxPaths(home, protected) {
		fmt.Fprintf(&builder, "  (subpath %s)\n", sbplString(path))
	}
	builder.WriteString(")\n")

	// 放行的两段必须排在上面那个 deny 之后：sbpl 里后面的规则覆盖前面的。
	if home != "" {
		builder.WriteString("\n;; known_hosts / ssh config 不是秘密，缺了 git 就连不上\n")
		builder.WriteString("(allow file-read*\n")
		for _, relative := range readableInProtected {
			fmt.Fprintf(&builder, "  (literal %s)\n", sbplString(filepath.Join(home, relative)))
		}
		builder.WriteString(")\n")

		// ssh 自己可以读私钥去认证，别的程序不行——`cat ~/.ssh/id_rsa` 仍然被拒。
		builder.WriteString("\n;; 只有 ssh 本体能读私钥：它要拿去认证，而 cat 只会把它读给模型\n")
		for _, binary := range sshBinaries {
			fmt.Fprintf(&builder,
				"(allow file-read* (require-all (subpath %s) (process-path %s)))\n",
				sbplString(filepath.Join(home, ".ssh")), sbplString(binary),
			)
		}
	}
	return builder.String()
}

/*
cacheDirs 是工具链的缓存目录，一律放开可写。

**实测出来的**：第一版只放开工作区和临时目录，结果 `go build` 直接跑不了——
`~/go/pkg/mod` 和 `~/Library/Caches/go-build` 写不进去，模型只好自己造一套
临时 GOCACHE / GOMODCACHE 重新下载整个依赖树。那不是「被挡住了危险操作」，
是把正常干活挡住了，而代价（重新下载几百 MB）全落在用户身上。

判断标准是**这里面有没有用户的东西**：缓存是工具自己生成、随时可以删掉重建的
派生物，写坏了最多重编译一次；而凭据、文档、代码不在这个名单里。
*/
func cacheDirs(home string) []string {
	dirs := []string{}
	// Go：两个缓存都认环境变量，取不到再按默认位置拼。
	for _, key := range []string{"GOCACHE", "GOMODCACHE"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			dirs = append(dirs, value)
		}
	}
	if home == "" {
		return dirs
	}
	dirs = append(dirs,
		filepath.Join(home, "go", "pkg", "mod"),
		filepath.Join(home, "Library", "Caches"),
		filepath.Join(home, ".cache"),
		filepath.Join(home, ".npm"),
		filepath.Join(home, ".cargo", "registry"),
		filepath.Join(home, ".gradle", "caches"),
		filepath.Join(home, ".m2", "repository"),
	)
	return dirs
}

/*
readableInProtected 是凭据目录里少数几个**不是秘密**、但缺了就干不了活的文件。

`~/.ssh` 整个禁读的代价是 git over ssh 直接不可用——实测里模型被迫绕成
`ssh -o UserKnownHostsFile=/tmp/...` 才连得上私服。known_hosts 与 config
本身不含密钥。
*/
var readableInProtected = []string{
	".ssh/known_hosts",
	".ssh/known_hosts2",
	".ssh/config",
}

/*
sshBinaries 是允许读 `~/.ssh` 私钥的程序。

这一条是整份策略里最值的一笔：**`ssh` 能读私钥去认证，`cat` 不能把私钥读出来
给模型**。seatbelt 的 `process-path` 过滤支持按可执行文件放行，实测有效：

	cat ~/.ssh/id_rsa  → Operation not permitted
	ssh -T git@…       → Welcome to GitLab, @zhouyy!

没有这一条的话只有两个坏选择：要么禁读（SSH 的 git 操作全废，而公司里仓库
都是 git@ 开头的），要么放开（模型一句 `cat` 就把私钥读进上下文）。

路径要写全几种安装位置：PATH 上先找到哪个 ssh 就是哪个，homebrew 装的在
/opt/homebrew。写死路径而不是按名字匹配——按名字的话，工作区里放一个叫 ssh
的脚本就能拿到私钥。
*/
var sshBinaries = []string{
	"/usr/bin/ssh",
	"/usr/bin/ssh-add",
	"/usr/bin/ssh-keyscan",
	"/usr/libexec/ssh-keysign",
	"/opt/homebrew/bin/ssh",
	"/usr/local/bin/ssh",
}

// protectedSandboxPaths 是沙箱里禁读的那些。与 paths.go 的名单同源——
// 两处判断的是同一件事，分开写迟早会漂。
func protectedSandboxPaths(home string, extra []string) []string {
	paths := make([]string, 0, len(protectedRelative)+len(protectedAbsolute)+len(extra))
	for _, relative := range protectedRelative {
		if home != "" {
			paths = append(paths, filepath.Join(home, relative))
		}
	}
	paths = append(paths, protectedAbsolute...)
	for _, path := range extra {
		if strings.TrimSpace(path) != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

// sbplString 把路径写成 sbpl 的字符串字面量。
//
// 不转义的话，一个路径里带引号的工作区就能把策略截断——后面的 deny 规则
// 全部失效，而且**不会报错**，沙箱看起来还在跑。
func sbplString(path string) string {
	escaped := strings.ReplaceAll(path, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}

// realpath 解析软链。沙箱按真实路径判，/tmp 就是 /private/tmp 的软链；
// 写着 /tmp 而进程访问的是 /private/tmp 时，规则不会命中。
func realpath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}
