package agent

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/model"
)

const maxSubAgentDepth = 3

// ── sub-agent 嵌套深度 ──────────────────────────────────────

type subAgentDepthKey struct{}

func subAgentDepth(ctx context.Context) int {
	if v, ok := ctx.Value(subAgentDepthKey{}).(int); ok {
		return v
	}
	return 0
}

func withSubAgentDepth(ctx context.Context, depth int) context.Context {
	return context.WithValue(ctx, subAgentDepthKey{}, depth)
}

// propagateAgentValues 保留父 context 中的所有值（auth、trace ID、WorkdirScope 等），
// 但断开取消信号传播链，使工具执行拥有独立的生命周期控制。
func propagateAgentValues(parent context.Context) context.Context {
	return context.WithoutCancel(parent)
}

// ── 工具 blocklist ──────────────────────────────────────────

type blockedToolsKey struct{}

func withBlockedTools(ctx context.Context, blocked []string) context.Context {
	if len(blocked) == 0 {
		return ctx
	}
	m := make(map[string]bool, len(blocked))
	for _, name := range blocked {
		m[name] = true
	}
	// 合并父级的 blocklist
	if parent, ok := ctx.Value(blockedToolsKey{}).(map[string]bool); ok {
		for k, v := range parent {
			m[k] = v
		}
	}
	return context.WithValue(ctx, blockedToolsKey{}, m)
}

// IsToolBlocked 检查工具是否被当前 sub-agent 上下文 block。
func IsToolBlocked(ctx context.Context, toolName string) bool {
	if m, ok := ctx.Value(blockedToolsKey{}).(map[string]bool); ok {
		return m[toolName]
	}
	return false
}

// ── 父执行上下文传递 ─────────────────────────────────────────

type parentExecInfoKey struct{}

type parentExecInfo struct {
	tracker *StepTracker
	convID  int64
}

func withParentExecInfo(ctx context.Context, tracker *StepTracker, convID int64) context.Context {
	return context.WithValue(ctx, parentExecInfoKey{}, &parentExecInfo{tracker: tracker, convID: convID})
}

func parentExecInfoFromCtx(ctx context.Context) (*StepTracker, int64) {
	if v, ok := ctx.Value(parentExecInfoKey{}).(*parentExecInfo); ok && v != nil {
		return v.tracker, v.convID
	}
	return nil, 0
}

// ── sub_agent call ID ──────────────────────────────────────

type subAgentCallIDKey struct{}

func withSubAgentCallID(ctx context.Context, callID string) context.Context {
	return context.WithValue(ctx, subAgentCallIDKey{}, callID)
}

func subAgentCallID(ctx context.Context) string {
	if v, ok := ctx.Value(subAgentCallIDKey{}).(string); ok {
		return v
	}
	return ""
}

// ── sub_agent 本轮调用计数 ──────────────────────────────────

type subAgentRoundCountKey struct{}

func withSubAgentRoundCount(ctx context.Context, count int) context.Context {
	return context.WithValue(ctx, subAgentRoundCountKey{}, count)
}

func subAgentRoundCount(ctx context.Context) int {
	if v, ok := ctx.Value(subAgentRoundCountKey{}).(int); ok {
		return v
	}
	return 0
}

// ── 任务参数和结果 ──────────────────────────────────────────

// subAgentTask 对应 tasks 数组中的单个子任务。
type subAgentTask struct {
	Goal         string   `json:"goal"`
	Context      string   `json:"context,omitempty"`
	AgentUUID    string   `json:"agent_uuid,omitempty"`
	BlockedTools []string `json:"blocked_tools,omitempty"`
	Mode         string   `json:"mode,omitempty"`  // auto, explore, shell
	Model        string   `json:"model,omitempty"` // "", "fast"
}

type subAgentTaskResult struct {
	TaskIndex       int      `json:"task_index"`
	Goal            string   `json:"goal"`
	Status          string   `json:"status"`
	Summary         string   `json:"summary"`
	TokensUsed      int      `json:"tokens_used"`
	ToolsUsed       []string `json:"tools_used,omitempty"`
	DurationSeconds float64  `json:"duration_seconds"`
	ExitReason      string   `json:"exit_reason"`
}

type subAgentBatchResult struct {
	Status  string               `json:"status"`
	Results []subAgentTaskResult `json:"results"`
}

// ── 默认 blocklist ─────────────────────────────────────────

// defaultSubAgentBlockedTools 子 Agent 默认不可用的工具。
var defaultSubAgentBlockedTools = []string{
	"sub_agent", // 除非显式解除，子 agent 默认不能再递归委派
}

// 模式白名单：explore 和 shell 模式只允许使用列出的工具，其余全部 block。
var modeAllowedTools = map[string]map[string]bool{
	"explore": {
		"read": true, "grep": true, "find": true, "ls": true,
		"web_fetch": true, "current_time": true, "session_search": true,
	},
	"shell": {
		"exec": true, "process": true, "read": true, "ls": true, "current_time": true,
	},
}

// ── handler 入口 ────────────────────────────────────────────

func (e *Executor) subAgentHandler(ctx context.Context, args string) (string, error) {
	depth := subAgentDepth(ctx) + 1
	if depth > maxSubAgentDepth {
		return "", fmt.Errorf("sub-agent depth limit (%d) reached", maxSubAgentDepth)
	}

	// 兼容旧版 {prompt, agent_uuid} 格式
	var raw struct {
		Tasks     []subAgentTask `json:"tasks"`
		Prompt    string         `json:"prompt,omitempty"`
		AgentUUID string         `json:"agent_uuid,omitempty"`
	}
	if err := json.Unmarshal([]byte(args), &raw); err != nil {
		return "", fmt.Errorf("parse sub_agent arguments: %w", err)
	}

	tasks := raw.Tasks
	if len(tasks) == 0 && raw.Prompt != "" {
		tasks = []subAgentTask{{Goal: raw.Prompt, AgentUUID: raw.AgentUUID}}
	}
	if len(tasks) == 0 {
		return "", fmt.Errorf("at least one task is required (provide 'tasks' array or 'prompt' string)")
	}

	ctx = withSubAgentDepth(ctx, depth)

	if len(tasks) == 1 {
		log.WithFields(log.Fields{
			"depth": depth,
			"goal":  truncateLog(tasks[0].Goal, 200),
			"mode":  cmp.Or(tasks[0].Mode, "auto"),
		}).Info("[SubAgent] >> single task")
	} else {
		log.WithFields(log.Fields{
			"depth":      depth,
			"task_count": len(tasks),
		}).Info("[SubAgent] >> batch parallel")
	}

	return e.executeTaskBatch(ctx, tasks, depth)
}

// ── 批量并行执行 ────────────────────────────────────────────

func (e *Executor) executeTaskBatch(ctx context.Context, tasks []subAgentTask, depth int) (string, error) {
	results := make([]subAgentTaskResult, len(tasks))
	var wg sync.WaitGroup
	var mu sync.Mutex
	allOK := true

	for i, t := range tasks {
		wg.Go(func() {
			start := time.Now()
			blocked := mergeBlockedToolsWithMode(t.BlockedTools, depth, t.Mode)
			prompt := buildSubAgentPrompt(t)

			log.WithFields(log.Fields{
				"depth":    depth,
				"task_idx": i,
				"mode":     cmp.Or(t.Mode, "auto"),
				"goal":     truncateLog(t.Goal, 120),
			}).Info("[SubAgent] >> task start")

			summary, tokens, toolsUsed, err := e.executeOneTask(ctx, prompt, t.AgentUUID, blocked, t.Model)
			dur := time.Since(start)

			r := subAgentTaskResult{
				TaskIndex:       i,
				Goal:            t.Goal,
				TokensUsed:      tokens,
				ToolsUsed:       toolsUsed,
				DurationSeconds: dur.Seconds(),
			}
			if err != nil {
				r.Status = "failed"
				r.Summary = err.Error()
				r.ExitReason = "error"
				mu.Lock()
				allOK = false
				mu.Unlock()
			} else {
				r.Status = "completed"
				r.Summary = summary
				r.ExitReason = "completed"
			}
			results[i] = r

			log.WithFields(log.Fields{
				"depth":    depth,
				"task_idx": i,
				"status":   r.Status,
				"tokens":   tokens,
				"duration": dur,
			}).Info("[SubAgent] << task done")
		})
	}
	wg.Wait()

	batchStatus := "completed"
	if !allOK {
		batchStatus = "partial_failure"
	}

	out, _ := json.Marshal(subAgentBatchResult{
		Status:  batchStatus,
		Results: results,
	})
	return string(out), nil
}

// executeOneTask 执行单个子任务，返回 (summary, tokens, toolsUsed, error)。
func (e *Executor) executeOneTask(ctx context.Context, prompt, agentUUID string, blocked []string, modelHint string) (string, int, []string, error) {
	if err := e.checkShutdown(); err != nil {
		return "", 0, nil, err
	}
	defer e.activeExecs.Done()

	ctx = withBlockedTools(ctx, blocked)
	ec, err := e.prepareSubAgent(ctx, prompt, agentUUID)
	if err != nil {
		return "", 0, nil, fmt.Errorf("sub_agent prepare: %w", err)
	}
	defer ec.closeMCP()

	// 根据 model hint 覆盖模型
	if modelHint == "fast" && ec.ag.FastModelName != "" {
		ec.l.WithFields(log.Fields{
			"original": ec.ag.ModelName,
			"fast":     ec.ag.FastModelName,
		}).Debug("[SubAgent] using fast model")
		ec.ag.ModelName = ec.ag.FastModelName
	}

	res, err := e.run(ec.ctx, ec, blockingCaller(ec.llmProv), false)
	if err != nil {
		return "", 0, nil, err
	}

	toolsUsed := extractToolsUsed(res.Steps)
	return res.Content, res.TokensUsed, toolsUsed, nil
}

// ── 辅助函数 ────────────────────────────────────────────────

// buildSubAgentPrompt 将 task 的 goal 和 context 合并为完整 prompt。
func buildSubAgentPrompt(t subAgentTask) string {
	var sb strings.Builder

	switch t.Mode {
	case "explore":
		sb.WriteString("## 约束\n你处于只读探索模式，仅可使用 read/grep/find/ls/web_fetch 等工具查看信息，不得修改任何文件。\n\n")
	case "shell":
		sb.WriteString("## 约束\n你处于命令执行模式，仅可使用 exec/process/read/ls 工具运行命令和查看结果。\n\n")
	}

	if t.Context != "" {
		sb.WriteString(fmt.Sprintf("## 背景信息\n%s\n\n", t.Context))
	}
	sb.WriteString(fmt.Sprintf("## 任务目标\n%s", t.Goal))
	return sb.String()
}

// mergeBlockedToolsWithMode 根据模式、深度和用户 blocklist 生成最终的工具黑名单。
// explore/shell 模式通过白名单反转为 blocklist（block 所有不在白名单中的内置工具）。
func mergeBlockedToolsWithMode(userBlocked []string, depth int, mode string) []string {
	seen := make(map[string]bool, len(defaultSubAgentBlockedTools)+len(userBlocked))
	var merged []string
	for _, name := range defaultSubAgentBlockedTools {
		if !seen[name] {
			merged = append(merged, name)
			seen[name] = true
		}
	}
	for _, name := range userBlocked {
		if !seen[name] {
			merged = append(merged, name)
			seen[name] = true
		}
	}
	if depth >= 2 {
		for _, name := range []string{"execute", "browser"} {
			if !seen[name] {
				merged = append(merged, name)
				seen[name] = true
			}
		}
	}

	// 模式白名单：block 所有不在白名单中的已知内置工具
	if allowed, ok := modeAllowedTools[mode]; ok {
		allBuiltins := []string{
			"read", "write", "edit", "grep", "find", "ls",
			"exec", "process", "web_fetch", "browser", "canvas",
			"code_interpreter", "memory", "cron", "todo",
			"session_search", "sub_agent",
		}
		for _, name := range allBuiltins {
			if !allowed[name] && !seen[name] {
				merged = append(merged, name)
				seen[name] = true
			}
		}
	}

	return merged
}

// extractToolsUsed 从执行步骤中提取不重复的工具名列表。
func extractToolsUsed(steps []model.ExecutionStep) []string {
	seen := make(map[string]bool)
	var result []string
	for _, s := range steps {
		if s.StepType == model.StepToolCall && s.Name != "" && !seen[s.Name] {
			seen[s.Name] = true
			result = append(result, s.Name)
		}
	}
	return result
}
