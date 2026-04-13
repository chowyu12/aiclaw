package agent

import (
	"context"
	"sync"
	"time"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/workspace"
)

// HookEvent 表示可被拦截的 Agent 生命周期事件。
type HookEvent string

const (
	HookPreToolUse  HookEvent = "pre_tool_use"
	HookPostToolUse HookEvent = "post_tool_use"
	HookPreLLMCall  HookEvent = "pre_llm_call"
	HookPostLLMCall HookEvent = "post_llm_call"
	HookAgentDone   HookEvent = "agent_done"
)

// HookAction 控制事件触发后的执行走向。
type HookAction int

const (
	HookContinue HookAction = iota
	HookSkip                // 跳过当前工具调用（仅 PreToolUse 有效）
	HookAbort               // 终止 Agent 循环（仅 PreLLMCall 有效）
)

// HookPayload 传递给 Hook 函数的上下文数据。
type HookPayload struct {
	// 工具上下文 (pre/post_tool_use)
	ToolName string
	ToolArgs string
	Result   string
	Error    error

	// LLM 调用上下文 (pre/post_llm_call)
	Model  string
	Round  int
	Tokens int // 单轮 LLM 调用消耗的 token 数（PostLLMCall）

	// Agent 完成上下文 (agent_done)
	ConvUUID    string
	UserMsg     string
	Content     string
	TotalTokens int // 整次执行累计 token 数（AgentDone）
	Duration     time.Duration
	Agent        *model.Agent
	Skills       []model.Skill
	CalledTools  map[string]bool
	ToolSkillMap map[string]string
	Tracker      *StepTracker
	WS           *workspace.Workspace
}

// HookFunc 签名：返回 HookAction 决定后续行为。
type HookFunc func(ctx context.Context, event HookEvent, payload *HookPayload) HookAction

// HookRegistry 管理并分发生命周期钩子。
type HookRegistry struct {
	mu    sync.RWMutex
	hooks map[HookEvent][]HookFunc
}

func NewHookRegistry() *HookRegistry {
	return &HookRegistry{hooks: make(map[HookEvent][]HookFunc)}
}

// Register 注册指定事件的钩子；同一事件可注册多个，按注册顺序执行。
func (r *HookRegistry) Register(event HookEvent, fn HookFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks[event] = append(r.hooks[event], fn)
}

// Fire 触发事件，依次执行所有钩子；返回最高优先级的 HookAction（Abort > Skip > Continue）。
func (r *HookRegistry) Fire(ctx context.Context, event HookEvent, payload *HookPayload) HookAction {
	r.mu.RLock()
	fns := r.hooks[event]
	r.mu.RUnlock()

	action := HookContinue
	for _, fn := range fns {
		if a := fn(ctx, event, payload); a > action {
			action = a
		}
	}
	return action
}
