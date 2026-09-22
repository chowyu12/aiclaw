package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

const (
	// waitDelay 是「进程已经退了，但还有人攥着输出管道」时再等多久。
	//
	// 不设这个的话 Wait 会一直等下去——踩过：git 挂着等凭据，超时杀掉了
	// 直接子进程，作为孙进程的 git 活下来并继续占着管道，那一步就永远
	// 停在「执行中」。见 process_unix.go 的说明。
	waitDelay = 3 * time.Second

	defaultExecTimeout = 60 * time.Second
	maxExecTimeout     = 10 * time.Minute
	maxExecOutput      = 64 * 1024
)

// dangerousCommands 是硬拒绝名单：命中即失败，**没有「仍然执行」的选项**。
//
// 这份名单沿用 upstream-agent 的 workspacetools，那边跑在 Sandbox 里都拦，
// 这里没有沙箱更得拦。名单里的命令没有任何合法的 Agent 使用场景。
// 它是应用层字符串匹配，绕过并不难——它防的是模型「顺手」写出这些命令，
// 不防有意规避。有意规避这一层挡不住，只能靠审批。
var dangerousCommands = []string{
	"rm -rf /", "rm -rf /*", "rm -rf ~", "rm -rf $home", "mkfs", "dd if=",
	":(){:|:&};:", "> /dev/sda", "chmod -r 777 /", "chown -r", "shutdown", "reboot",
	"halt", "poweroff", "init 0", "init 6", "kill -9 1", "useradd", "userdel",
	"usermod", "passwd", "visudo", "iptables -f", "iptables -x", "nft flush",
	"crontab -r", "systemctl disable", "> /etc/", "tee /etc/", "mount ", "umount ",
	"fdisk ", "parted ", "wipefs", "diskutil erase", "format c:", "del /f /s /q c:\\",
	"rd /s /q c:\\", "remove-item -recurse -force c:\\",
}

// riskyCommands 是「要先问一句」的名单。
//
// 默认档位下普通命令不再逐条审批——每次 `ls`、`go test` 都弹框，只会把用户
// 训练成闭眼点「允许」，那比少问一次危险。但有一类命令即使不在硬拒绝名单上，
// 做错了也很难收回：删东西、提权、改系统设置、把网上的脚本直接喂给 shell、
// 往外推代码。这些问一句。
//
// 和硬拒绝名单一样，它是字符串匹配，**防的是「顺手写出来」，不防有意规避**。
// 有意规避这一层挡不住——挡得住的只有沙箱，而这一版没有。
var riskyCommands = []string{
	"rm -r", "rm -f", "rmdir", "sudo ", "su ", "doas ",
	"chmod ", "chown ", "chgrp ",
	"curl ", "wget ", "nc ", "ssh ", "scp ", "rsync ",
	"git push", "git reset --hard", "git clean", "git checkout --",
	"npm publish", "npm i -g", "npm install -g", "pip install", "brew install",
	"docker ", "kubectl ", "launchctl", "defaults write", "killall", "pkill",
	"truncate ", "shred ", "mv /", "cp -r /",
}

// riskyReason 返回这条命令为什么要问；不需要问时返回空串。
func riskyReason(command string) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(command), " "))
	// 管道进 shell 是最典型的一条：curl 本身已经在名单里，但
	// `xxx | sh` 这种把任意内容当脚本执行的形状要单独认出来。
	for _, pipe := range []string{"| sh", "| bash", "| zsh", "|sh", "|bash"} {
		if strings.Contains(normalized, pipe) {
			return "把下载的内容直接交给 shell 执行"
		}
	}
	for _, pattern := range riskyCommands {
		if strings.Contains(normalized, pattern) {
			return fmt.Sprintf("命令里有 %s", strings.TrimSpace(pattern))
		}
	}
	return ""
}

func checkDangerous(command string) error {
	normalized := strings.ToLower(strings.Join(strings.Fields(command), " "))
	for _, pattern := range dangerousCommands {
		if strings.Contains(normalized, pattern) {
			return fmt.Errorf("命令包含被禁止的操作（%s），已拒绝执行", strings.TrimSpace(pattern))
		}
	}
	return nil
}

// RegisterExecTool 登记命令执行工具。
func RegisterExecTool(registry *Registry) error {
	return registry.Register(Tool{
		Name: "run_command",
		Description: "执行一条 shell 命令并返回输出。命令在会话工作区里跑" +
			"（没设工作区时在用户主目录），直接跑在用户本机。" +
			"删除、提权、改系统设置这类命令执行前会请用户确认。" +
			"需要连着做几步（cd 进去再跑、先 source 再跑）时用 session 参数，" +
			"否则每条命令都是全新的 shell，上一条的 cd 不算数。",
		Effect: EffectExec,
		Schema: schema(map[string]any{
			"command": map[string]any{"type": "string", "description": "完整命令行"},
			"cwd": map[string]any{
				"type": "string", "description": "在哪个目录下执行；不传用会话工作区（没设则用主目录）",
			},
			"timeout_seconds": map[string]any{
				"type": "integer", "description": "超时秒数，默认 60，最大 600", "minimum": 1, "maximum": 600,
			},
			"reason": map[string]any{
				"type": "string", "description": "一句话说明为什么要执行；会展示给用户帮助其决定是否放行",
			},
			"session": map[string]any{
				"type": "string",
				"description": "常驻会话：传 \"new\" 开一个，或传上次返回的 id 继续用。" +
					"同一个会话里 cd、export、source 都留着。不传就是一次性命令。",
			},
		}, "command"),
		Handler: runCommand,
	})
}

func runCommand(ctx context.Context, raw json.RawMessage, env *Env) (string, error) {
	var args struct {
		Command        string `json:"command"`
		Cwd            string `json:"cwd"`
		TimeoutSeconds int    `json:"timeout_seconds"`
		Reason         string `json:"reason"`
		Session        string `json:"session"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return "", err
	}
	command := strings.TrimSpace(args.Command)
	if command == "" {
		return "", errors.New("command 不能为空")
	}
	if err := checkDangerous(command); err != nil {
		return "", err
	}

	cwd := env.Base()
	if strings.TrimSpace(args.Cwd) != "" {
		resolved, err := env.ResolveRead(args.Cwd)
		if err != nil {
			return "", err
		}
		cwd = resolved
	}
	// 目录不存在要**在这里**说清楚。交给 exec 的话，Go 报的是
	// 「fork/exec <解释器>: no such file or directory」——那句话指向可执行文件，
	// 而真正不存在的是工作目录。实测模型据此判断「sandbox-exec 没装」，
	// 然后朝着完全错误的方向修了好几轮。
	if info, statErr := os.Stat(cwd); statErr != nil || !info.IsDir() {
		return "", fmt.Errorf("执行目录不存在：%s", cwd)
	}

	// 只有看着危险的才问。理由：每条命令都弹框会把用户训练成闭眼点「允许」，
	// 而那比少问一次更危险。严格档位（always）仍然逐条问——需要那种约束的
	// 场景把档位调上去就行。
	//
	// detail 带完整命令与目录，不截断：用户要判断的就是这一行。
	effect := EffectExec
	reason := args.Reason
	if risk := riskyReason(command); risk == "" && env.Policy != protocol.ApprovalAlways {
		effect = EffectRead
	} else if risk != "" {
		if reason == "" {
			reason = risk
		} else {
			reason = fmt.Sprintf("%s（%s）", reason, risk)
		}
	}
	if err := env.RequestApproval(
		ctx, effect, protocol.ApprovalExec,
		"执行命令", fmt.Sprintf("%s\n（在 %s 下执行）", command, cwd), reason,
	); err != nil {
		return "", err
	}

	timeout := defaultExecTimeout
	if args.TimeoutSeconds > 0 {
		timeout = time.Duration(args.TimeoutSeconds) * time.Second
		if timeout > maxExecTimeout {
			timeout = maxExecTimeout
		}
	}
	// 常驻会话：状态留着，所以 cd / export / source 才有意义。
	// 审批与危险名单在上面都已经走过了，这里只是换一种执行方式。
	if session := strings.TrimSpace(args.Session); session != "" {
		if env.Shells == nil {
			return "", errors.New("当前环境不支持常驻 shell 会话")
		}
		output, id, err := env.Shells.Run(ctx, session, command, env, timeout)
		if err != nil {
			return output, err
		}
		return fmt.Sprintf("%s\n（常驻会话 %s；下次传 session=\"%s\" 继续用它）", output, id, id), nil
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 用户点过「允许」的不进沙箱：那正是审批的含义——他看着这条命令批准了它，
	// 再拦一道只会让「我明明同意了它却失败」。没问过的那些才套沙箱。
	sandboxed := effect == EffectRead && env.Sandbox && SandboxAvailable()
	cmd := shellCommand(execCtx, command)
	if sandboxed {
		cmd = sandboxedShellCommand(execCtx, command, env)
	}
	cmd.Dir = cwd
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{buffer: &stdout, limit: maxExecOutput}
	cmd.Stderr = &limitedWriter{buffer: &stderr, limit: maxExecOutput}

	runErr := cmd.Run()

	var builder strings.Builder
	if stdout.Len() > 0 {
		builder.Write(stdout.Bytes())
	}
	if stderr.Len() > 0 {
		if builder.Len() > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString("[stderr]\n")
		builder.Write(stderr.Bytes())
	}
	output := builder.String()

	if runErr != nil && sandboxed && strings.Contains(output, "Operation not permitted") {
		// 沙箱拒绝时系统只说 "Operation not permitted"，看起来像权限配错了。
		// 补一句说清是谁拦的、怎么办，否则模型会反复重试同一条命令。
		output += "\n[沙箱] 这条命令想写工作区之外的地方，或者读凭据目录，已被拦下。" +
			"（工具链缓存不在限制内，go/npm/cargo 照常可用。）" +
			"要往别处写，请用户把会话工作区指到那儿，或者在「配置 → 执行」里临时关掉沙箱；" +
			"**不要自己造一套临时目录绕过去**——那会让用户下次还得重来一遍。"
	}
	if errors.Is(runErr, exec.ErrWaitDelay) {
		// 命令**本身已经结束**了，只是它拉起的后台进程还攥着输出管道。
		// 这不是失败：该给的输出都在手上，接着等下去才是错的
		//（踩过：一个挂着等凭据的 git 让那一步永远停在「执行中」）。
		return output + "\n（命令已结束；它启动的后台进程还在跑，不再等它的输出）", nil
	}
	if runErr != nil {
		if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			return output, fmt.Errorf("命令超过 %s 未结束，已终止", timeout)
		}
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			// 非零退出不算工具故障：把退出码和输出一起给模型，它自己判断。
			return fmt.Sprintf("%s\n[退出码 %d]", output, exitErr.ExitCode()), nil
		}
		return output, fmt.Errorf("启动命令失败：%w", runErr)
	}
	if output == "" {
		return "（命令执行完成，无输出）", nil
	}
	return output, nil
}

// sandboxedShellCommand 把命令包进 sandbox-exec。
//
// 策略用 -p 直接传，不落临时文件：落文件就多一处要清理、要防篡改的东西，
// 而策略本身不长。
func sandboxedShellCommand(ctx context.Context, command string, env *Env) *exec.Cmd {
	granted := []string(nil)
	if env.Grants != nil {
		granted = env.Grants()
	}
	profile := buildSandboxProfile(env.Workspace, env.Home, env.ProtectedPaths, granted)
	return prepareContext(exec.CommandContext(ctx, sandboxExecPath, "-p", profile, "/bin/sh", "-c", command))
}

func shellCommand(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return prepareContext(exec.CommandContext(
			ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", command,
		))
	}
	return prepareContext(exec.CommandContext(ctx, "/bin/sh", "-c", command))
}

/*
prepare 给每条命令装上三件事：进程组、收尾时限、非交互环境。

第三件是因为**挂住的命令多半是在等输入**：git 要密码、ssh 要 passphrase。
Agent 这边没有终端，等下去不会有人回答，只会把一轮卡死到超时。让它们
直接失败，模型拿到「需要凭据」这个明确的错误，还能换个做法。
*/
func prepare(cmd *exec.Cmd) *exec.Cmd {
	configureProcessGroup(cmd)
	cmd.WaitDelay = waitDelay
	cmd.Env = append(cmd.Environ(),
		// git 不要弹凭据提示，直接失败。
		"GIT_TERMINAL_PROMPT=0",
		// ssh 同理：agent 里的密钥照常能用，只是不再等人输密码。
		"GIT_SSH_COMMAND=ssh -oBatchMode=yes",
		// 有些工具认这个来判断「现在没人盯着」。
		"CI=1",
	)
	return cmd
}

// prepareContext 是 prepare 加上「ctx 取消时按进程组杀」。
// 只用在 CommandContext 建出来的命令上。
func prepareContext(cmd *exec.Cmd) *exec.Cmd {
	prepare(cmd)
	cancelByProcessGroup(cmd)
	return cmd
}

// limitedWriter 只保留前 limit 字节，之后丢弃并记一个截断标记。
// 命令刷屏不该把模型上下文撑爆。
type limitedWriter struct {
	buffer    *bytes.Buffer
	limit     int
	truncated bool
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	remaining := w.limit - w.buffer.Len()
	if remaining <= 0 {
		if !w.truncated {
			w.buffer.WriteString("\n[输出已截断]")
			w.truncated = true
		}
		return len(p), nil
	}
	if len(p) > remaining {
		w.buffer.Write(p[:remaining])
		w.buffer.WriteString("\n[输出已截断]")
		w.truncated = true
		return len(p), nil
	}
	return w.buffer.Write(p)
}
