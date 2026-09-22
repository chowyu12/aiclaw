package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

/*
路径解析。

**这一层不再是围墙，是护栏。** 早先的规则是「一切收敛到工作目录内」，
而 run_command 每次都要审批——那时候路径约束确实是一道闸。现在不是了：
用户要的是「能读工作区外面的东西，普通命令不要每次都问」，而一旦命令能
不经确认地跑，`echo x > /etc/y` 和 read_file 走的就不是同一道门。所以这里
的判断改成两件更诚实的事：

  1. **敏感文件硬拒绝**（凭据类，见 protectedPaths）。读到 ~/.ssh/id_rsa
     之后，模型可以顺着联网搜索的 query、任何第三方 MCP
     把它送出去，而那几条路都不弹框。这条名单不给「仍然读」的选项。
  2. **写到工作区外面要确认**。不是因为拦得住（命令可以绕过去），
     而是因为「模型以为自己在项目里，其实在改主目录」是最常见的意外，
     一次确认就能挡住，代价只有一下。

读则完全放开：Agent 读不到 /usr/include 或者另一个仓库的代码时，用户得
自己把文件复制进来，这件事本身比风险更烦人。
*/

// ErrProtected 是命中敏感名单时的错误。单独一个类型，便于上层认出来。
var ErrProtected = errors.New("涉及凭据的路径不允许访问")

// protectedRelative 是相对用户主目录的敏感路径。
//
// 只放**凭据存放处**，不放「重要目录」：把 ~/Documents 之类也拦上会让
// Agent 动不动就撞墙，而那不是它能造成的最大损害。
var protectedRelative = []string{
	".ssh",
	".aws",
	".gnupg",
	".netrc",
	".npmrc",
	".pypirc",
	".docker/config.json",
	".kube/config",
	".config/gcloud",
	".config/gh/hosts.yml",
	"Library/Keychains",
	"Library/Application Support/Google/Chrome/Default/Login Data",
}

// protectedAbsolute 是与主目录无关的敏感路径。
var protectedAbsolute = []string{
	"/etc/shadow",
	"/etc/sudoers",
	"/private/etc/master.passwd",
	"/Library/Keychains",
}

// Base 是相对路径的解析基准：有工作区用工作区，没有用主目录。
//
// 没设工作区时不是「哪儿都不能碰」，而是「以主目录为准」——那正是用户
// 在终端里打开一个新窗口时的处境，也是他对「没设置」的预期。
func (e *Env) Base() string {
	if strings.TrimSpace(e.Workspace) != "" {
		return e.Workspace
	}
	if strings.TrimSpace(e.Home) != "" {
		return e.Home
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return "."
}

// Protected 判断一个路径是否命中敏感名单。传进来的应当是解析过的绝对路径。
func (e *Env) Protected(path string) bool {
	home := strings.TrimSpace(e.Home)
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	var candidates []string
	for _, rel := range protectedRelative {
		if home != "" {
			candidates = append(candidates, filepath.Join(home, rel))
		}
	}
	candidates = append(candidates, protectedAbsolute...)
	candidates = append(candidates, e.ProtectedPaths...)

	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		cleaned := filepath.Clean(candidate)
		// 名单里的路径也要解析软链：传进来的 path 是解析过的真实路径，
		// 两边不在同一个形态上比，前缀判断会静默失效。macOS 上 /var 就是
		// /private/var 的软链，这条不做的话整份名单在临时目录里全不生效。
		if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
			cleaned = resolved
		}
		if within(cleaned, path) {
			return true
		}
	}
	return false
}

// resolve 把工具传入的路径变成一个绝对路径，并解析软链。
//
// 软链要解析，否则「工作区内一个指向 ~/.ssh 的软链」既躲过敏感名单，
// 也躲过工作区判断。目标还不存在时（写新文件）退一步解析最近的已存在祖先。
func (e *Env) resolve(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("路径不能为空")
	}
	base := e.Base()
	target := e.expandHome(trimmed)
	if !filepath.IsAbs(target) {
		root := base
		if resolved, err := filepath.EvalSymlinks(base); err == nil {
			root = resolved
		}
		target = filepath.Join(root, target)
	}
	target = filepath.Clean(target)

	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		return resolved, nil
	}

	// 不存在：向上找到最近的已存在祖先，按它的真实路径拼回去。
	existing := target
	var missing []string
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", fmt.Errorf("路径不可达：%s", raw)
		}
		missing = append([]string{filepath.Base(existing)}, missing...)
		existing = parent
	}
	resolvedAncestor, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", fmt.Errorf("路径不可达：%s", raw)
	}
	// 尚不存在的那截里不允许再出现 ..：那会让最终落点跳出已解析的祖先。
	for _, segment := range missing {
		if segment == ".." {
			return "", fmt.Errorf("路径里不允许出现 ..：%s", raw)
		}
	}
	return filepath.Join(append([]string{resolvedAncestor}, missing...)...), nil
}

// expandHome 展开开头的 ~。模型经常写 ~/Desktop，而 Go 不认它。
//
// 用 Env.Home 而不是直接问系统：两者必须是同一个值，否则「~/.ssh」展开到
// 系统主目录、敏感名单却按 Env.Home 拼，名单就绕过去了。
func (e *Env) expandHome(raw string) string {
	if raw != "~" && !strings.HasPrefix(raw, "~/") {
		return raw
	}
	home := strings.TrimSpace(e.Home)
	if home == "" {
		resolved, err := os.UserHomeDir()
		if err != nil {
			return raw
		}
		home = resolved
	}
	if raw == "~" {
		return home
	}
	return filepath.Join(home, raw[2:])
}

// ResolveRead 解析一个用来读的路径。工作区之外也允许，凭据类除外。
func (e *Env) ResolveRead(raw string) (string, error) {
	path, err := e.resolve(raw)
	if err != nil {
		return "", err
	}
	if e.Protected(path) {
		return "", fmt.Errorf("%w：%s", ErrProtected, raw)
	}
	return path, nil
}

// ResolveWrite 解析一个用来写的路径，并报告它在不在工作区内。
//
// 不在工作区内**不代表不能写**，代表要先问一句——判断留给调用方，
// 因为「问」这件事要走审批通道，而它在 Env 上。
func (e *Env) ResolveWrite(raw string) (path string, inside bool, err error) {
	path, err = e.resolve(raw)
	if err != nil {
		return "", false, err
	}
	if e.Protected(path) {
		return "", false, fmt.Errorf("%w：%s", ErrProtected, raw)
	}
	// 用户在本次会话里批准过的目录，与工作区同等对待——他已经明确说过
	// 「往这儿写没问题」，再问一次就是没记住他的话。
	if e.Grants != nil {
		for _, granted := range e.Grants() {
			if within(realpath(filepath.Clean(granted)), path) {
				return path, true, nil
			}
		}
	}
	workspace := strings.TrimSpace(e.Workspace)
	if workspace == "" {
		// 没设工作区就没有「里面」，一律按外面处理——写之前问一句。
		return path, false, nil
	}
	return path, within(realpath(filepath.Clean(workspace)), path), nil
}
