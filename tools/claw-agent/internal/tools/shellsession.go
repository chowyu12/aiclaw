package tools

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

/*
常驻 shell 会话。

**为什么要有。** 每条命令都开一个新 shell 的话，`cd`、`export`、`source venv`
这些都不留痕——模型只能把它们和真正要跑的命令拼成一长串，每次重来一遍；
而它经常忘了拼，于是「明明 cd 过去了怎么还找不到文件」。这是用起来最别扭的
一处，也是 codex 把 exec 改成带 session 的原因。

**怎么判断一条命令跑完了。** 不用 PTY：那要额外的依赖，还要分平台处理，
而我们只想要「状态留着」。改成往 shell 的 stdin 里写命令，后面跟一句
打印哨兵与退出码的 printf，然后读到那一行为止。哨兵带一段随机串——
命令自己的输出里要是恰好有同样的字，就会被当成结束标记。

**沙箱在起 shell 的那一刻套上。** 所以后来新批准的目录对已经开着的会话不生效
（策略是进程级的），需要的话开一个新会话。不套的话，模型只要改用常驻会话
就能绕开沙箱——那等于这层防护不存在。
*/

const (
	// maxShellSessions 是一个会话里最多能开几个常驻 shell。
	// 不设上限的话，模型每次都传 "new"，进程会一直堆下去。
	maxShellSessions = 4
	// shellIdleTimeout 是闲置多久就回收。用户聊别的去了，
	// 留着几个 shell 没有意义。
	shellIdleTimeout = 30 * time.Minute
)

// ShellPool 是一个 agent 会话里的常驻 shell 集合。
type ShellPool struct {
	mu       sync.Mutex
	sessions map[string]*shellSession
}

func NewShellPool() *ShellPool { return &ShellPool{sessions: map[string]*shellSession{}} }

type shellSession struct {
	id       string
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	output   *bufio.Reader
	sentinel string
	// mu 串行化命令：两条命令同时往一个 shell 里写，输出会交织，
	// 而哨兵会被对方读走。
	mu       sync.Mutex
	lastUsed time.Time
}

// Run 在指定的常驻 shell 里跑一条命令。id 为 "new" 时新开一个。
//
// 返回输出与这次用的会话 id——模型要靠它继续用同一个 shell。
func (p *ShellPool) Run(
	ctx context.Context,
	id, command string,
	env *Env,
	timeout time.Duration,
) (output, sessionID string, err error) {
	if runtime.GOOS == "windows" {
		// powershell 也能这么玩，但我们没有 Windows 机器验证，
		// 而一个没验证过的「卡住不返回」比没有这个功能糟。
		return "", "", errors.New("Windows 上暂不支持常驻 shell 会话，请用一次性命令")
	}

	session, err := p.acquire(id, env)
	if err != nil {
		return "", "", err
	}

	session.mu.Lock()
	defer session.mu.Unlock()
	session.lastUsed = time.Now()

	out, err := session.run(ctx, command, timeout)
	if err != nil {
		// 会话已经不可用了（shell 退了、超时被杀），别留着让下一次调用再撞一次。
		p.drop(session.id)
		return out, session.id, err
	}
	return out, session.id, nil
}

// Close 关掉全部常驻 shell。会话结束时调。
func (p *ShellPool) Close() {
	p.mu.Lock()
	sessions := make([]*shellSession, 0, len(p.sessions))
	for _, session := range p.sessions {
		sessions = append(sessions, session)
	}
	p.sessions = map[string]*shellSession{}
	p.mu.Unlock()
	for _, session := range sessions {
		session.close()
	}
}

func (p *ShellPool) acquire(id string, env *Env) (*shellSession, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reapLocked()

	if id != "" && id != "new" {
		session, ok := p.sessions[id]
		if !ok {
			// 说清楚是「这个会话没了」而不是别的错，模型才知道要重开一个。
			return nil, fmt.Errorf("常驻会话 %s 不存在或已结束，传 session=\"new\" 开一个新的", id)
		}
		return session, nil
	}

	if len(p.sessions) >= maxShellSessions {
		return nil, fmt.Errorf("常驻会话已达上限 %d 个，先用现有的", maxShellSessions)
	}
	session, err := startShellSession(env)
	if err != nil {
		return nil, err
	}
	p.sessions[session.id] = session
	return session, nil
}

func (p *ShellPool) drop(id string) {
	p.mu.Lock()
	session, ok := p.sessions[id]
	delete(p.sessions, id)
	p.mu.Unlock()
	if ok {
		session.close()
	}
}

// reapLocked 回收闲置太久的。调用方持锁。
func (p *ShellPool) reapLocked() {
	for id, session := range p.sessions {
		if time.Since(session.lastUsed) > shellIdleTimeout {
			delete(p.sessions, id)
			go session.close()
		}
	}
}

func startShellSession(env *Env) (*shellSession, error) {
	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("生成会话标记失败：%w", err)
	}
	sentinel := "__AICLAW_DONE_" + hex.EncodeToString(nonce) + "__"

	// 不带 ctx：这个 shell 要活过单条命令的生命周期。超时由 run 那边处理。
	var cmd *exec.Cmd
	if env.Sandbox && SandboxAvailable() {
		granted := []string(nil)
		if env.Grants != nil {
			granted = env.Grants()
		}
		profile := buildSandboxProfile(env.Workspace, env.Home, env.ProtectedPaths, granted)
		cmd = exec.Command(sandboxExecPath, "-p", profile, "/bin/sh")
	} else {
		cmd = exec.Command("/bin/sh")
	}
	// 与一次性命令同样的处理：自成进程组（关会话时连子孙一起收），
	// 非交互环境（挂住的命令多半在等输入，而这里没有人可以回答）。
	prepare(cmd)
	cmd.Dir = env.Base()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("建立常驻会话失败：%w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("建立常驻会话失败：%w", err)
	}
	// stderr 并进 stdout：两条管子分开读的话，哪一行先到就成了竞态，
	// 而用户看到的顺序会和真实发生的顺序对不上。
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("建立常驻会话失败：%w", err)
	}

	id := "sh_" + hex.EncodeToString(nonce[:4])
	return &shellSession{
		id:       id,
		cmd:      cmd,
		stdin:    stdin,
		output:   bufio.NewReader(stdout),
		sentinel: sentinel,
		lastUsed: time.Now(),
	}, nil
}

func (s *shellSession) run(ctx context.Context, command string, timeout time.Duration) (string, error) {
	// 命令与哨兵之间用 `;` 而不是 `&&`：命令失败了也要打印哨兵，
	// 否则一条失败的命令会让这次读取一直等到超时。
	script := fmt.Sprintf("%s\nprintf '\\n%s %%s\\n' \"$?\"\n", command, s.sentinel)
	if _, err := io.WriteString(s.stdin, script); err != nil {
		return "", fmt.Errorf("常驻会话已经结束：%w", err)
	}

	type result struct {
		text string
		code string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		var builder strings.Builder
		for {
			line, err := s.output.ReadString('\n')
			if marker := strings.Index(line, s.sentinel); marker >= 0 {
				builder.WriteString(line[:marker])
				code := strings.TrimSpace(line[marker+len(s.sentinel):])
				done <- result{text: builder.String(), code: code}
				return
			}
			builder.WriteString(line)
			if err != nil {
				done <- result{text: builder.String(), err: err}
				return
			}
			if builder.Len() > maxExecOutput {
				// 刷屏的命令不该把上下文撑爆。这里只截断给模型看的部分，
				// 剩下的仍要读完，否则哨兵永远等不到。
				builder.WriteString("\n…（输出过长已截断）\n")
				for {
					next, readErr := s.output.ReadString('\n')
					if strings.Contains(next, s.sentinel) || readErr != nil {
						done <- result{text: builder.String(), err: readErr}
						return
					}
				}
			}
		}
	}()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(timeout):
		// 超时只能连 shell 一起杀：没有 PTY 就没有进程组可以单独收，
		// 而留着一个正在跑的命令，下一次调用读到的会是它的输出。
		return "", fmt.Errorf("命令超过 %s 未结束，已终止整个常驻会话", timeout)
	case got := <-done:
		text := strings.TrimRight(got.text, "\n")
		if got.err != nil && text == "" {
			return "", fmt.Errorf("常驻会话已经结束：%w", got.err)
		}
		if got.code != "" && got.code != "0" {
			if text == "" {
				return fmt.Sprintf("（无输出，退出码 %s）", got.code), nil
			}
			return text + fmt.Sprintf("\n（退出码 %s）", got.code), nil
		}
		if text == "" {
			return "（命令执行完成，无输出）", nil
		}
		return text, nil
	}
}

func (s *shellSession) close() {
	_ = s.stdin.Close()
	// 按进程组杀：shell 里跑起来的东西（npm、git）是它的子进程，
	// 只杀 shell 的话它们会活下来，还继续占着我们的管道。
	killProcessGroup(s.cmd)
	_ = s.cmd.Wait()
}
