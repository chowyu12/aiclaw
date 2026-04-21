package shellexec

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// 这是 best-effort 的黑名单，并非安全沙箱。生产环境请结合容器/seccomp/不可写 rootfs 使用。
// 与旧版 substring 匹配不同，这里采用「词法拆分 + 正则 + 命令名精确匹配」的组合：
//   1. 将整条命令串按 shell 操作符（;, &&, ||, |, `)）切成多个 segment；
//   2. 每个 segment 去掉变量赋值前缀（KEY=value）与包装命令（sudo/env/nohup/time/exec/command/builtin）；
//   3. 取剩余首 token 的 basename，与 cmdNameBlocklist 精确比较；
//   4. 再对整条原始命令串（保留空白）跑 dangerRules 正则，拦截 fork bomb、危险重定向、
//      rm -rf 到根路径、chmod -R 777 根路径、curl|sh 等跨 token 模式。
// 优势：
//   - 能区分 "cat /etc/passwd"（通过）与 "passwd"（拦截，修改当前用户密码）；
//   - 能拦截带别名包装 `sudo rm -rf /`、`env X=1 shutdown`；
//   - 误报/漏报更平衡，规则通过 id 可定位。

type dangerRule struct {
	id string
	rx *regexp.Regexp
}

// pathTail 是各路径匹配规则的结尾 terminator：空格、行尾、引号、命令操作符、`*`、`/` 均视作结束。
const pathTail = `(?:$|[\s"';|&*/])`

var (
	// cmdNameBlocklist 命令 basename 的精确黑名单（小写）。凡是命令链中任一 segment 的
	// 首命令等于这里的 name，直接拦截。
	cmdNameBlocklist = map[string]string{
		"shutdown":   "system shutdown",
		"reboot":     "system reboot",
		"halt":       "system halt",
		"poweroff":   "system poweroff",
		"init":       "init runlevel change",
		"telinit":    "runlevel change",
		"systemctl":  "systemctl (use read-only commands only)",
		"service":    "service control",
		"killall":    "killall (dangerous scope)",
		"pkill":      "pkill (dangerous scope)",
		"mkfs":       "filesystem formatting",
		"wipefs":     "partition wipe",
		"fdisk":      "partition edit",
		"parted":     "partition edit",
		"mount":      "filesystem mount",
		"umount":     "filesystem unmount",
		"useradd":    "account management",
		"userdel":    "account management",
		"usermod":    "account management",
		"groupadd":   "account management",
		"groupdel":   "account management",
		"passwd":     "password change",
		"visudo":     "sudoers edit",
		"ssh-keygen": "key generation",
		"crontab":    "cron modification",
	}

	// 命令链开头经常出现但并非真正要执行的命令，解析首命令时跳过。
	commandPrefixes = map[string]bool{
		"sudo":    true,
		"env":     true,
		"nohup":   true,
		"time":    true,
		"command": true,
		"exec":    true,
		"builtin": true,
		"stdbuf":  true,
		"ionice":  true,
		"nice":    true,
	}

	// 适用于整条命令串的正则规则；命中即拦截。
	dangerRules = []dangerRule{
		// fork bomb 及其常见变体（兼容 :(){:|:&};: 和 :(){ :|: & };:）
		{"fork_bomb", regexp.MustCompile(`:\s*\(\s*\)\s*\{[^}]*\|\s*:\s*&?\s*\}\s*;\s*:`)},
		// rm -rf / | /* | ~ | $HOME | 关键目录
		{"rm_rf_root", regexp.MustCompile(
			`(?i)\brm\s+(?:-[a-zA-Z]*[rRdf][a-zA-Z]*\s+)+["']?(?:/(?:etc|var|usr|bin|sbin|boot|root|home|lib\w*|opt)?|~|\$HOME|\$\{HOME\})` + pathTail)},
		// dd 写入块设备
		{"dd_block_device", regexp.MustCompile(`(?i)\bdd\b[^;|&]*\bof=\s*/dev/(?:sd|nvme|mmcblk|hd|disk|vd)\w*`)},
		// 危险文件重定向
		{"redirect_system_path", regexp.MustCompile(`(?i)(?:>>?|\btee\b\s+(?:-a\s+)?)\s*(?:/etc/|/boot/|/dev/sd|/dev/nvme|/dev/mmcblk|/dev/disk|/dev/hd)`)},
		// chmod -R 到关键路径
		{"chmod_recursive_root", regexp.MustCompile(
			`(?i)\bchmod\s+(?:-R\s+|--recursive\s+)?0?7{3,4}\s+["']?(?:/(?:etc|usr|var|boot)?|~|\$HOME)` + pathTail)},
		// chown -R 到关键路径
		{"chown_recursive_root", regexp.MustCompile(
			`(?i)\bchown\s+(?:-R\s+|--recursive\s+)\S+\s+["']?(?:/(?:etc|usr|var|boot)?)` + pathTail)},
		// curl|wget 直接管道到 sh/bash/zsh（远程执行）
		{"remote_pipe_shell", regexp.MustCompile(`(?i)\b(?:curl|wget|fetch)\b[^|]*\|\s*(?:/\S+/)?(?:ba|z|k|da|a)?sh\b`)},
		// kill -9 1 / killall -9 -1 等杀 PID 1
		{"kill_pid1", regexp.MustCompile(`(?i)\bkill\s+(?:-\w+\s+)*-?\s*1\b`)},
		// 清空 iptables / nft
		{"iptables_flush", regexp.MustCompile(`(?i)\b(?:ip6?tables|nft)\b[^;|&]*(?:\s|^)(?:-F|-X|--flush|flush)\b`)},
	}

	// 命令链切分操作符（保留顺序长→短，避免 && 被当成两个 &）。
	commandSeparators = []string{"&&", "||", ";;", ";", "|", "&"}
)

// CheckDangerousCommand 对单条 shell 命令做启发式安全检查。通过返回 nil，拦截返回 error。
func CheckDangerousCommand(cmd string) error {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return nil
	}

	for _, r := range dangerRules {
		if r.rx.MatchString(cmd) {
			return fmt.Errorf("dangerous command blocked: matched rule %q", r.id)
		}
	}

	for _, seg := range splitTopLevel(cmd) {
		name := firstCommandName(seg)
		if name == "" {
			continue
		}
		if reason, ok := cmdNameBlocklist[name]; ok {
			return fmt.Errorf("dangerous command blocked: %s (%q)", reason, name)
		}
		// 某些命令带变体后缀，例如 mkfs.ext4 / mkfs.vfat / fsck.ext2；
		// 取点号前的前缀再查一次，避免仅靠正则时覆盖不全。
		if dot := strings.IndexByte(name, '.'); dot > 0 {
			if reason, ok := cmdNameBlocklist[name[:dot]]; ok {
				return fmt.Errorf("dangerous command blocked: %s (%q)", reason, name)
			}
		}
	}

	return nil
}

// splitTopLevel 将命令按 shell 操作符切分，并保留引号/反引号/$()/${} 中的内容。
// 出于安全检查目的，这里只需近似解析——即便未处理所有 shell 语法，误差也是倾向拦截更多。
func splitTopLevel(cmd string) []string {
	var segments []string
	var buf strings.Builder

	i, n := 0, len(cmd)
	var quote byte
	parenDepth := 0
	braceDepth := 0

	flush := func() {
		s := strings.TrimSpace(buf.String())
		if s != "" {
			segments = append(segments, s)
		}
		buf.Reset()
	}

	for i < n {
		c := cmd[i]

		// 在引号内：追加直到匹配的结束引号（转义下一个字符）。
		if quote != 0 {
			if c == '\\' && i+1 < n {
				buf.WriteByte(c)
				buf.WriteByte(cmd[i+1])
				i += 2
				continue
			}
			if c == quote {
				quote = 0
			}
			buf.WriteByte(c)
			i++
			continue
		}

		switch c {
		case '\'', '"', '`':
			quote = c
			buf.WriteByte(c)
			i++
			continue
		case '\\':
			if i+1 < n {
				buf.WriteByte(c)
				buf.WriteByte(cmd[i+1])
				i += 2
				continue
			}
		case '(':
			parenDepth++
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case '{':
			braceDepth++
		case '}':
			if braceDepth > 0 {
				braceDepth--
			}
		}

		// 顶层才考虑切分。
		if parenDepth == 0 && braceDepth == 0 {
			matched := ""
			for _, sep := range commandSeparators {
				if i+len(sep) <= n && cmd[i:i+len(sep)] == sep {
					matched = sep
					break
				}
			}
			if matched != "" {
				flush()
				i += len(matched)
				continue
			}
		}

		buf.WriteByte(c)
		i++
	}
	flush()
	return segments
}

// firstCommandName 从 segment 中提取真正要执行的命令名（小写 basename）。
// 会跳过 KEY=value 形式的变量赋值前缀，以及 sudo/env/nohup/time 等包装命令。
func firstCommandName(segment string) string {
	tokens := tokenize(segment)
	for _, tok := range tokens {
		if tok == "" {
			continue
		}
		if i := strings.IndexByte(tok, '='); i > 0 && isShellIdent(tok[:i]) {
			continue
		}
		lower := strings.ToLower(filepath.Base(tok))
		if commandPrefixes[lower] {
			continue
		}
		return strings.TrimSpace(lower)
	}
	return ""
}

// tokenize 近似 shell token 化：按空白切分，但保留成对引号内的空白。
func tokenize(s string) []string {
	var tokens []string
	var buf strings.Builder
	var quote byte

	flush := func() {
		if buf.Len() > 0 {
			tokens = append(tokens, buf.String())
			buf.Reset()
		}
	}

	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' && i+1 < len(s) {
				buf.WriteByte(s[i+1])
				i++
				continue
			}
			if c == quote {
				quote = 0
				continue
			}
			buf.WriteByte(c)
			continue
		}
		switch c {
		case ' ', '\t', '\n', '\r':
			flush()
		case '\'', '"':
			quote = c
		case '\\':
			if i+1 < len(s) {
				buf.WriteByte(s[i+1])
				i++
			}
		default:
			buf.WriteByte(c)
		}
	}
	flush()
	return tokens
}

func isShellIdent(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			continue
		}
		if i > 0 && c >= '0' && c <= '9' {
			continue
		}
		return false
	}
	return true
}
