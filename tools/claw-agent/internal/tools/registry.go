// Package tools 提供内置工具与工具注册表。
//
// **本版本不做 OS 级沙箱**（已确认的产品决定）。因此这里的两条约束是
// 仅剩的防护，改动时要意识到后面没有兜底了：
//
//  1. 路径解析一律收敛到工作目录内，符号链接按解析后的真实路径判断；
//  2. 有副作用的工具按策略走审批，审批被拒就是失败，不静默放行。
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

// Effect 描述一个工具的副作用级别，决定是否需要审批。
type Effect string

const (
	// EffectRead 只读，不需要审批。
	EffectRead Effect = "read"
	// EffectWrite 写工作区内的文件。不问——那正是用户把工作区指过来的意思。
	EffectWrite Effect = "write"
	// EffectWriteOutside 写工作区之外的文件（或者根本没设工作区）。
	//
	// 单独一档不是因为拦得住——命令可以绕过去——而是因为「模型以为自己在
	// 项目里，其实在改主目录」是最常见的意外，一次确认就挡住了。
	EffectWriteOutside Effect = "write-outside"
	// EffectExec 执行命令。没有沙箱的情况下这是最危险的一类。
	EffectExec Effect = "exec"
	// EffectExternal 对外部系统产生副作用（MCP 工具默认归这一类）。
	EffectExternal Effect = "external"
)

// ApprovalFunc 向宿主请求一次审批。
//
// 返回完整回应而不只是 bool：用户可能选的是「本次会话这个目录都允许」，
// 那个范围信息必须传到这一层才有人记得住。
type ApprovalFunc func(
	ctx context.Context,
	request protocol.ApprovalRequestParams,
) (protocol.ApprovalResponse, error)

// Env 是工具执行时的环境。
type Env struct {
	// Workspace 是这个会话的工作区，可以为空（用户没设）。
	//
	// 它是**相对路径的基准**和「写这里不用问」的范围，不是围墙——
	// 读可以在外面，写在外面要确认。见 paths.go 顶上的说明。
	Workspace string
	// Home 是用户主目录。没有工作区时相对路径按它解析，敏感名单也按它拼。
	Home string
	// ProtectedPaths 是宿主追加的敏感路径（比如应用自己存凭据的那个目录）。
	ProtectedPaths []string
	// Sandbox 决定没问过的命令要不要包进 macOS 沙箱。见 sandbox.go。
	Sandbox bool
	// Grants 返回本次会话里用户已经批准可写的目录。
	//
	// 是函数不是切片：授权发生在会话中途（用户点了「本次会话都允许」），
	// 而 Env 是每轮新建的——存快照的话，这一轮里刚批的下一次调用就忘了。
	Grants func() []string
	// Grant 记下一条新授权。为空表示这个环境不支持会话级授权。
	Grant func(dir string)
	// Shells 是这个会话的常驻 shell 池。为空表示不支持常驻会话。
	Shells *ShellPool
	// Policy 决定哪些副作用需要审批。
	Policy protocol.ApprovalPolicy
	// Approve 向宿主要一次审批。
	Approve ApprovalFunc
	// SessionID / TurnID 只用于填审批请求，方便宿主定位。
	SessionID string
	TurnID    string
	// Attachments 收集工具产出的图片（PNG）。
	//
	// 图不能放进工具结果：Chat Completions 的 tool 消息必须是纯字符串。
	// 所以截屏这类工具把图挂在这里，由轮次循环在工具结果之后补一条带图的
	// user 消息送进去。
	Attachments [][]byte
	attachMu    sync.Mutex
}

// Attach 挂一张图。并发安全：一批工具是并行执行的。
func (e *Env) Attach(image []byte) {
	if len(image) == 0 {
		return
	}
	e.attachMu.Lock()
	e.Attachments = append(e.Attachments, image)
	e.attachMu.Unlock()
}

// TakeAttachments 取走并清空已收集的图。
func (e *Env) TakeAttachments() [][]byte {
	e.attachMu.Lock()
	defer e.attachMu.Unlock()
	taken := e.Attachments
	e.Attachments = nil
	return taken
}

// needsApproval 按策略判断某个副作用要不要确认。
//
// never 与 on-write 用同一条线判断「要不要确认」，差别在后面：on-write 去问，
// never 直接失败。bypass 什么都不问、什么都放。
func (e *Env) needsApproval(effect Effect) bool {
	switch e.Policy {
	case protocol.ApprovalBypass:
		return false
	case protocol.ApprovalAlways:
		return effect != EffectRead
	default: // on-write / never
		return effect == EffectExec || effect == EffectExternal || effect == EffectWriteOutside
	}
}

// RequestApproval 按策略要一次审批。
//
// 策略为 bypass 时不问，直接放行；为 never 时不问，直接失败——无人值守的
// 另一头没有人能点「允许」，而通道会话正靠这一条守住「只读工具之外的都不动」。
// 危险命令的硬拒绝在这之前就发生了，任何档位都绕不过。
func (e *Env) RequestApproval(
	ctx context.Context,
	effect Effect,
	kind protocol.ApprovalKind,
	title, detail, reason string,
) error {
	return e.requestApprovalScoped(ctx, effect, kind, title, detail, reason, "")
}

// requestApprovalScoped 是带作用域的审批。
//
// scopePath 非空时，宿主可以给出「本次会话这个目录都允许」；用户选了之后
// 这里把那个目录记进会话，后面同一个目录下的写就不再问。
func (e *Env) requestApprovalScoped(
	ctx context.Context,
	effect Effect,
	kind protocol.ApprovalKind,
	title, detail, reason, scopePath string,
) error {
	if !e.needsApproval(effect) {
		return nil
	}
	if e.Policy == protocol.ApprovalNever {
		return fmt.Errorf("%s 需要确认，而当前是无人值守模式，不执行", title)
	}
	if e.Approve == nil {
		return fmt.Errorf("%s 需要确认，但当前没有可用的审批通道", title)
	}
	response, err := e.Approve(ctx, protocol.ApprovalRequestParams{
		SessionID: e.SessionID,
		TurnID:    e.TurnID,
		Kind:      kind,
		Title:     title,
		Detail:    detail,
		Cwd:       e.Base(),
		Reason:    reason,
		ScopePath: scopePath,
	})
	if err != nil {
		return fmt.Errorf("请求确认失败：%w", err)
	}
	if response.Approved && response.Scope == protocol.ApprovalScopeSession &&
		scopePath != "" && e.Grant != nil {
		e.Grant(scopePath)
	}
	approved := response.Approved
	if !approved {
		return errors.New("用户拒绝了这次操作")
	}
	return nil
}

// Handler 执行一次工具调用。
type Handler func(ctx context.Context, args json.RawMessage, env *Env) (string, error)

type Tool struct {
	Name        string
	Description string
	Schema      json.RawMessage
	Effect      Effect
	Handler     Handler
}

// ParallelSafe 报告这个工具能否与别的工具并发执行。
//
// 模型一次给出多个工具调用时，只有只读的那些可以同时跑：两个 edit_file
// 落在同一个文件上会互相覆盖，两个 run_command 会同时弹出两个审批框，
// 用户看不出哪个框对应哪条命令。
//
// Codex 让每个 handler 自己声明 supports_parallel_tool_calls，因为它的
// 工具集大、分类杂（比如只读的 MCP 工具也标成可并行）。这里按副作用推导：
// 工具集小且分类明确，少一个「新增工具时忘了填」的字段。MCP 工具一律是
// external，因此一律串行——副作用未知时慢一点比错一次划算。
func (t Tool) ParallelSafe() bool { return t.Effect == EffectRead }

type Registry struct {
	order []string
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

func (r *Registry) Register(tool Tool) error {
	if tool.Name == "" {
		return errors.New("工具名为空")
	}
	if _, exists := r.tools[tool.Name]; exists {
		// 重名报错而不是覆盖：静默遮蔽会让「两个 MCP server 暴露了同名工具」
		// 变成难查的怪问题。
		return fmt.Errorf("工具 %q 重复注册", tool.Name)
	}
	r.tools[tool.Name] = tool
	r.order = append(r.order, tool.Name)
	return nil
}

func (r *Registry) Get(name string) (Tool, bool) {
	tool, ok := r.tools[name]
	return tool, ok
}

func (r *Registry) List() []Tool {
	list := make([]Tool, 0, len(r.order))
	for _, name := range r.order {
		list = append(list, r.tools[name])
	}
	return list
}

func (r *Registry) Names() []string {
	names := append([]string(nil), r.order...)
	sort.Strings(names)
	return names
}

func (r *Registry) Len() int { return len(r.order) }

func within(root, target string) bool {
	if target == root {
		return true
	}
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func outsideError(raw, workdir string) error {
	return fmt.Errorf("路径 %s 在工作目录之外；当前工作目录是 %s", raw, workdir)
}

// decodeArgs 解析工具参数。
func decodeArgs(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return errors.New(ExplainBadArguments(string(raw), err))
	}
	return nil
}

// ExplainBadArguments 把「参数不是合法 JSON」说成模型能据以行动的话。
//
// 最常见的原因不是写错格式，而是**输出撞上上限被截断**：一万五千字符的参数
// 停在一句话中间。只说「不是合法 JSON」，模型会去改 JSON 的写法，改几轮都
// 没用；说出「断在第 N 个字符」，它才知道该把这一步拆小。判断很粗：合法的
// 参数一定以 } 或 ] 收尾，不是的就当截断。
func ExplainBadArguments(raw string, err error) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed != "" && !strings.HasSuffix(trimmed, "}") && !strings.HasSuffix(trimmed, "]") {
		return fmt.Sprintf(
			"工具参数在第 %d 个字符处被截断（结尾是 %q），多半是这次输出超出了长度上限。"+
				"把这一步拆成几次调用：先产出一部分、用 store() 存着，或者让工具自己去读文件而不是把内容写进参数。",
			len([]rune(trimmed)), tailRunes(trimmed, 12),
		)
	}
	return fmt.Sprintf("参数不是合法 JSON 对象：%v", err)
}

func tailRunes(text string, n int) string {
	runes := []rune(text)
	if len(runes) <= n {
		return text
	}
	return "…" + string(runes[len(runes)-n:])
}

func schema(properties map[string]any, required ...string) json.RawMessage {
	body := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		body["required"] = required
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		// schema 是常量拼出来的，编码失败说明代码写错了，不该运行时容忍。
		panic(fmt.Sprintf("构造工具 schema 失败：%v", err))
	}
	return encoded
}
