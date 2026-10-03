package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/chowyu12/aiclaw/internal/i18n"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/tools"
)

// 子 agent（多 agent 协作）。
//
// **对照 Codex 的 multi_agents_v2 做**（codex-rs/core/src/tools/handlers/multi_agents_v2/、
// multi_agents_spec.rs），工具名、参数、返回形状都与它一致，以后它改了照着跟：
//
//   - spawn_agent(task_name, message, fork_turns?)：开一个子 agent 去做一件事，立刻返回，
//     子 agent 在后台跑。它有和你一样的工具，也能再开自己的子 agent（层数有上限）。
//   - send_message(target, message)：给某个 agent 发消息，只投递、不叫醒它。
//   - followup_task(target, message)：给某个 agent 追加任务，它闲着就开一轮。
//   - wait_agent(timeout_ms?)：等邮箱里来东西（子 agent 的消息或完成通知），
//     **不返回内容**——内容作为消息插进你的上下文，下一步就看得到。
//   - list_agents(path_prefix?)：列出这棵树上活着的 agent 与状态。
//   - interrupt_agent(target)：打断某个 agent 当前这一轮，它还在，还能接着派活。
//
// 名字按路径编：根会话是 /root，它开的 research 就是 /root/research；在自己的子树里
// 可以只写 research。子 agent 结束时，它的最终回答连同状态投进父 agent 的邮箱；
// 父 agent 那时闲着的话自动开一轮来接收（否则结果就搁在那儿没人看）。
//
// 与 Codex 一样，所有 agent 共用同一个工作区：一个改的文件别的立刻看得到，
// 并行改代码时要让各自的写入范围不重叠。
//
// 不同于 Codex 的地方：模型与推理强度跟父会话走，不开放覆盖（AIClaw 的模型绑在
// 模型服务上，按名字换模型说不清换到哪个服务）；agent_type（角色）暂不支持。

// Collaboration 是协作的控制面，由 server 实现：开子会话、投递消息、跑轮次都要
// 碰会话表与事件出口，那是 server 的东西。
type Collaboration interface {
	Spawn(ctx context.Context, parent *Session, request SpawnRequest) (SpawnResult, error)
	// Send 投递一条消息。trigger 为 true 时目标闲着就开一轮（followup_task）。
	Send(ctx context.Context, from *Session, target, message string, trigger bool) error
	List(self *Session, prefix string) ([]AgentInfo, error)
	Interrupt(self *Session, target string) (string, error)
}

// SpawnRequest 是 spawn_agent 的参数。
type SpawnRequest struct {
	TaskName string
	Message  string
	// ForkTurns：-1 = 全部历史，0 = 不带，N = 最近 N 轮。
	ForkTurns int
}

// SpawnResult 是 spawn_agent 的返回。与 Codex 一致：task_name 是规范名（完整路径）。
type SpawnResult struct {
	TaskName  string            `json:"task_name"`
	Nickname  string            `json:"nickname,omitempty"`
	MCPStatus map[string]string `json:"mcp_status,omitempty"`
}

// AgentInfo 是 list_agents 的一行。
type AgentInfo struct {
	AgentName   string `json:"agent_name"`
	AgentStatus string `json:"agent_status"`
}

// Agent 的状态。与 Codex 的 AgentStatus 对应（小写蛇形）。
const (
	AgentPreparing   = "preparing"
	AgentRunning     = "running"
	AgentCompleted   = "completed"
	AgentErrored     = "errored"
	AgentInterrupted = "interrupted"
	AgentIdle        = "idle"
)

// WithCollaboration 接上协作控制面。没接的话不挂这组工具（通道会话、测试）。
func WithCollaboration(collab Collaboration) Option {
	return func(s *Session) { s.collab = collab }
}

// wait_agent 的超时范围（毫秒）。Codex 的这三个值是可配的，默认让模型「等久一点」，
// 别反复短轮询。
const (
	minWaitMS     = 10_000
	defaultWaitMS = 120_000
	maxWaitMS     = 30 * 60_000
)

var taskNamePattern = regexp.MustCompile(`^[a-z0-9_]{1,40}$`)

// registerCollabTools 挂上协作工具。放在代码模式之外：Codex 同样不让这几个工具进
// exec——在脚本里开 agent、等 agent 没有意义，而且等待会把整段脚本卡住。
func (s *Session) registerCollabTools() error {
	if s.collab == nil {
		return nil
	}
	register := func(tool tools.Tool) error { return s.registry.Register(tool) }

	if err := register(tools.Tool{
		Name: "spawn_agent",
		Description: "Spawn a sub-agent to handle a well-scoped task. Returns immediately; the agent runs in the background. " +
			"Its canonical name is your path plus task_name (if you are /root, a sub-agent with task_name research is /root/research); " +
			"within your subtree you can refer to it simply as research. It has the same tools as you, can message you and other agents, and its final answer is delivered to your mailbox when it finishes. " +
			"fork_turns defaults to all (carry the full context); none carries nothing, in which case the task message must spell out all the background.\n" +
			"When to use: only spawn when the user or a skill explicitly asks for delegation or parallel work; being asked to be \"thorough\" or to \"investigate in depth\" is not by itself permission. " +
			"Think through the critical path first: do the work whose result you need right away yourself, and hand off only side tasks that can run in parallel without blocking your next step. " +
			"Tasks must be concrete, self-contained and non-overlapping; when changing code, give each sub-agent a non-overlapping write scope (everyone shares the same workspace), " +
			"and have it list the files it changed in its final answer. After handing off, work on something that doesn't overlap — don't redo the task yourself, and don't wait on it repeatedly.",
		Schema: schemaOf(map[string]any{
			"task_name":  map[string]any{"type": "string", "description": "Task name for the sub-agent: lowercase letters, digits and underscores"},
			"message":    map[string]any{"type": "string", "description": "The task description for it (plain text)"},
			"fork_turns": map[string]any{"type": "string", "description": "How much context to carry: all (default), none, or a positive integer string (e.g. \"3\" carries only the last few turns)"},
		}, "task_name", "message"),
		Effect: tools.EffectRead,
		Handler: func(ctx context.Context, raw json.RawMessage, _ *tools.Env) (string, error) {
			var args struct {
				TaskName  string `json:"task_name"`
				Message   string `json:"message"`
				ForkTurns string `json:"fork_turns"`
			}
			if err := decodeEmailArgs(raw, &args); err != nil {
				return "", err
			}
			name := strings.TrimSpace(args.TaskName)
			if !taskNamePattern.MatchString(name) {
				return "", i18n.E("task_name 只能用小写字母、数字、下划线，最长 40 个字符")
			}
			if strings.TrimSpace(args.Message) == "" {
				return "", errors.New("Empty message can't be sent to an agent")
			}
			fork, err := parseForkTurns(args.ForkTurns)
			if err != nil {
				return "", err
			}
			result, err := s.collab.Spawn(ctx, s, SpawnRequest{TaskName: name, Message: args.Message, ForkTurns: fork})
			if err != nil {
				return "", err
			}
			return jsonText(result), nil
		},
	}); err != nil {
		return err
	}

	messageSchema := schemaOf(map[string]any{
		"target":  map[string]any{"type": "string", "description": "Target agent: canonical name (/root/research) or a task name within your subtree (research); /root is the root"},
		"message": map[string]any{"type": "string", "description": "Message content (plain text)"},
	}, "target", "message")
	sendHandler := func(trigger bool) tools.Handler {
		return func(ctx context.Context, raw json.RawMessage, _ *tools.Env) (string, error) {
			var args struct {
				Target  string `json:"target"`
				Message string `json:"message"`
			}
			if err := decodeEmailArgs(raw, &args); err != nil {
				return "", err
			}
			if strings.TrimSpace(args.Message) == "" {
				return "", errors.New("Empty message can't be sent to an agent")
			}
			if err := s.collab.Send(ctx, s, strings.TrimSpace(args.Target), args.Message, trigger); err != nil {
				return "", err
			}
			return "", nil
		}
	}
	if err := register(tools.Tool{
		Name:        "send_message",
		Description: "Send a message to an existing agent. The message is delivered as soon as possible but does not start a new turn for it.",
		Schema:      messageSchema,
		Effect:      tools.EffectRead,
		Handler:     sendHandler(false),
	}); err != nil {
		return err
	}
	if err := register(tools.Tool{
		Name: "followup_task",
		Description: "Give an existing non-root agent a follow-up task: if it is idle, it starts a new turn; if it is running, the task is delivered before its next sampling step " +
			"(or after the tool call in progress finishes).",
		Schema:  messageSchema,
		Effect:  tools.EffectRead,
		Handler: sendHandler(true),
	}); err != nil {
		return err
	}

	if err := register(tools.Tool{
		Name: "wait_agent",
		Description: "Wait until any live agent puts something in your mailbox (a queued message or a completion notification); new input from the user also ends the wait early. " +
			"Returns no content: whatever arrives shows up as messages in your context. Only wait when your next step really needs the result and there is nothing else you can do, " +
			"and wait long (minutes), rather than polling repeatedly with short timeouts.",
		Schema: schemaOf(map[string]any{
			"timeout_ms": map[string]any{"type": "integer", "description": fmt.Sprintf("Maximum wait in milliseconds; default %d, range %d–%d", defaultWaitMS, minWaitMS, maxWaitMS)},
		}),
		Effect: tools.EffectRead,
		Handler: func(ctx context.Context, raw json.RawMessage, _ *tools.Env) (string, error) {
			var args struct {
				TimeoutMS *int64 `json:"timeout_ms"`
			}
			if err := decodeEmailArgs(raw, &args); err != nil {
				return "", err
			}
			timeout := int64(defaultWaitMS)
			clamped := ""
			if args.TimeoutMS != nil {
				if *args.TimeoutMS > maxWaitMS {
					return "", fmt.Errorf("timeout_ms must be at most %d", maxWaitMS)
				}
				timeout = *args.TimeoutMS
				if timeout < minWaitMS {
					clamped = fmt.Sprintf("\n\nRequested timeout of %dms was clamped to the minimum of %dms.", timeout, minWaitMS)
					timeout = minWaitMS
				}
			}
			outcome := s.waitPending(ctx, time.Duration(timeout)*time.Millisecond)
			message := map[string]string{
				"mailbox": "Wait completed.",
				"steered": "Wait interrupted by new input.",
				"timeout": "Wait timed out.",
			}[outcome]
			if outcome == "canceled" {
				return "", ctx.Err()
			}
			return jsonText(map[string]any{"message": message + clamped, "timed_out": outcome == "timeout"}), nil
		},
	}); err != nil {
		return err
	}

	if err := register(tools.Tool{
		Name:        "list_agents",
		Description: "List the live agents in the current tree, optionally filtered by path prefix.",
		Schema: schemaOf(map[string]any{
			"path_prefix": map[string]any{"type": "string", "description": "Path prefix, without a trailing slash; omit to list all"},
		}),
		Effect: tools.EffectRead,
		Handler: func(_ context.Context, raw json.RawMessage, _ *tools.Env) (string, error) {
			var args struct {
				PathPrefix string `json:"path_prefix"`
			}
			if err := decodeEmailArgs(raw, &args); err != nil {
				return "", err
			}
			agents, err := s.collab.List(s, strings.TrimSpace(args.PathPrefix))
			if err != nil {
				return "", err
			}
			if agents == nil {
				agents = []AgentInfo{}
			}
			return jsonText(map[string]any{"agents": agents}), nil
		},
	}); err != nil {
		return err
	}

	return register(tools.Tool{
		Name:        "interrupt_agent",
		Description: "Interrupt an agent's current turn (if it is running) and return its previous status. The agent stays alive; you can keep messaging it and giving it tasks afterwards.",
		Schema: schemaOf(map[string]any{
			"target": map[string]any{"type": "string", "description": "Canonical name or task name of the target agent"},
		}, "target"),
		Effect: tools.EffectRead,
		Handler: func(_ context.Context, raw json.RawMessage, _ *tools.Env) (string, error) {
			var args struct {
				Target string `json:"target"`
			}
			if err := decodeEmailArgs(raw, &args); err != nil {
				return "", err
			}
			previous, err := s.collab.Interrupt(s, strings.TrimSpace(args.Target))
			if err != nil {
				return "", err
			}
			return jsonText(map[string]string{"previous_status": previous}), nil
		},
	})
}

func parseForkTurns(value string) (int, error) {
	value = strings.TrimSpace(value)
	switch strings.ToLower(value) {
	case "", "all":
		return -1, nil
	case "none":
		return 0, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return 0, errors.New("fork_turns must be `none`, `all`, or a positive integer string")
	}
	return n, nil
}

func jsonText(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}

// ---------- 会话这一侧：邮箱、等待、分叉历史 ----------

// DeliverAgentMail 把一条 agent 间的消息投进这个会话的邮箱，返回它此刻有没有在跑。
//
// 与用户插话走同一个队列（pending）：在跑就在下一次采样前插进历史，闲着就等下一轮
// 开头插进去。区别在于它进历史时不显示成用户的话，时间线上只留一行提示。
func (s *Session) DeliverAgentMail(from, text, notice string) bool {
	s.mu.Lock()
	s.pending = append(s.pending, userInput{text: text, mail: true, mailFrom: from, notice: notice})
	running := s.cancelTurn != nil && !s.finishing
	s.mu.Unlock()
	s.signalPending()
	return running
}

// signalPending 叫醒 wait_agent。
func (s *Session) signalPending() {
	select {
	case s.pendingSignal <- struct{}{}:
	default:
	}
}

// waitPending 等邮箱来东西：mailbox / steered / timeout / canceled。
// 调用前就已经排着的，立刻返回（Codex 的 pending_activity）。
func (s *Session) waitPending(ctx context.Context, timeout time.Duration) string {
	if kind := s.pendingKind(); kind != "" {
		return kind
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return "canceled"
		case <-timer.C:
			return "timeout"
		case <-s.pendingSignal:
			if kind := s.pendingKind(); kind != "" {
				return kind
			}
		}
	}
}

func (s *Session) pendingKind() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	kind := ""
	for _, input := range s.pending {
		if !input.mail {
			return "steered"
		}
		kind = "mailbox"
	}
	return kind
}

// acceptAgentMail 把一条 agent 消息放进历史：给模型看全文，时间线上只留一行提示。
func (s *Session) acceptAgentMail(turnID string, input userInput, emitter Emitter) {
	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: input.text, Shown: &llm.Shown{Hidden: true}})
	if input.notice != "" {
		s.notify(emitter, turnID, input.notice)
	}
}

// Config 返回会话配置的一份拷贝。开子 agent 时照着它配。
func (s *Session) Config() protocol.SessionStartParams {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.config
}

// LastAnswer 是最近一条模型回答的正文（子 agent 结束时交给父 agent 的「最终回答」）。
func (s *Session) LastAnswer() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.messages) - 1; i >= 0; i-- {
		message := s.messages[i]
		if message.Role == llm.RoleAssistant && strings.TrimSpace(message.Content) != "" {
			return message.Content
		}
		if message.Role == llm.RoleUser && (message.Shown == nil || !message.Shown.Hidden) {
			break
		}
	}
	return ""
}

// ForkFrom 把父会话的历史接到这个（刚建的）会话后面：turns < 0 全部，0 不带，N 最近 N 轮。
//
// 只取完整的部分：父会话此刻正在执行 spawn_agent，最后一条 assistant 消息的工具调用
// 还没有结果——带过来的话子会话的第一次请求就是 400。一轮按「用户说的一句话」切，
// 与 Codex 的 fork_turns 口径一致。
func (s *Session) ForkFrom(parent *Session, turns int) {
	if turns == 0 {
		return
	}
	history := parent.snapshotMessages()
	history = completeHistory(history)
	var body []llm.Message
	for _, message := range history {
		if message.Role == llm.RoleSystem {
			continue
		}
		body = append(body, message)
	}
	if turns > 0 {
		starts := []int{}
		for i, message := range body {
			if message.Role == llm.RoleUser && (message.Shown == nil || !message.Shown.Hidden) {
				starts = append(starts, i)
			}
		}
		if len(starts) > turns {
			body = body[starts[len(starts)-turns]:]
		}
	}
	s.mu.Lock()
	s.messages = append(s.messages, body...)
	s.mu.Unlock()
}

// completeHistory 去掉末尾那段工具调用还没有结果的消息。
func completeHistory(messages []llm.Message) []llm.Message {
	for end := len(messages); end > 0; end-- {
		cut := messages[:end]
		if historyComplete(cut) {
			return cut
		}
	}
	return nil
}

func historyComplete(messages []llm.Message) bool {
	answered := map[string]bool{}
	for _, message := range messages {
		if message.Role == llm.RoleTool {
			answered[message.ToolCallID] = true
		}
	}
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			if !answered[call.ID] {
				return false
			}
		}
	}
	return true
}
