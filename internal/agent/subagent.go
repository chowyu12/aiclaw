package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	openai "github.com/chowyu12/go-openai"
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

	// 单任务 → inline 轻量路径
	if len(tasks) == 1 {
		t := tasks[0]
		log.WithFields(log.Fields{
			"depth": depth,
			"goal":  truncateLog(t.Goal, 200),
		}).Info("[SubAgent] >> inline (single task)")

		blocked := mergeBlockedTools(t.BlockedTools, depth)
		prompt := buildSubAgentPrompt(t)
		return e.inlineSubAgentCall(ctx, prompt, t.AgentUUID, blocked)
	}

	// 多任务 → 并行执行
	log.WithFields(log.Fields{
		"depth":      depth,
		"task_count": len(tasks),
	}).Info("[SubAgent] >> batch parallel")

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
			blocked := mergeBlockedTools(t.BlockedTools, depth)
			prompt := buildSubAgentPrompt(t)

			log.WithFields(log.Fields{
				"depth":    depth,
				"task_idx": i,
				"goal":     truncateLog(t.Goal, 120),
			}).Info("[SubAgent] >> task start")

			summary, tokens, toolsUsed, err := e.executeOneTask(ctx, prompt, t.AgentUUID, blocked)
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
func (e *Executor) executeOneTask(ctx context.Context, prompt, agentUUID string, blocked []string) (string, int, []string, error) {
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

	res, err := e.run(ec.ctx, ec, blockingCaller(ec.llmProv), false)
	if err != nil {
		return "", 0, nil, err
	}

	toolsUsed := extractToolsUsed(res.Steps)
	return res.Content, res.TokensUsed, toolsUsed, nil
}

// ── inline 轻量路径 ─────────────────────────────────────────

func (e *Executor) inlineSubAgentCall(ctx context.Context, prompt, agentUUID string, blocked []string) (string, error) {
	ag, err := e.loadAgent(ctx, agentUUID)
	if err != nil {
		return "", fmt.Errorf("inline sub_agent: load agent: %w", err)
	}

	prov, err := e.store.GetProvider(ctx, ag.ProviderID)
	if err != nil {
		return "", fmt.Errorf("inline sub_agent: provider not found: %w", err)
	}

	llmProv, err := e.providerFactory(prov)
	if err != nil {
		return "", fmt.Errorf("inline sub_agent: create provider: %w", err)
	}

	systemPrompt := ag.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = "你是一个运行在 Aiclaw 内部的个人助手。"
	}

	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
		{Role: openai.ChatMessageRoleUser, Content: prompt},
	}

	l := log.WithFields(log.Fields{"agent": ag.Name, "model": ag.ModelName})

	req := openai.ChatCompletionRequest{
		Model:    ag.ModelName,
		Messages: messages,
	}
	applyModelCaps(&req, ag, prov.Type, l)

	start := time.Now()
	resp, err := llmProv.CreateChatCompletion(ctx, req)
	dur := time.Since(start)
	if err != nil {
		return "", fmt.Errorf("inline sub_agent: LLM call: %w", err)
	}

	content := extractContent(resp)

	result := subAgentTaskResult{
		TaskIndex:       0,
		Status:          "completed",
		Summary:         content,
		TokensUsed:      resp.Usage.TotalTokens,
		DurationSeconds: dur.Seconds(),
		ExitReason:      "completed",
	}

	l.WithFields(log.Fields{
		"tokens":   resp.Usage.TotalTokens,
		"duration": dur,
	}).Info("[SubAgent] << inline done")

	out, _ := json.Marshal(subAgentBatchResult{
		Status:  "completed",
		Results: []subAgentTaskResult{result},
	})
	return string(out), nil
}

// ── 辅助函数 ────────────────────────────────────────────────

// buildSubAgentPrompt 将 task 的 goal 和 context 合并为完整 prompt。
func buildSubAgentPrompt(t subAgentTask) string {
	if t.Context == "" {
		return t.Goal
	}
	return fmt.Sprintf("## 背景信息\n%s\n\n## 任务目标\n%s", t.Context, t.Goal)
}

// mergeBlockedTools 将用户指定的 blocklist 与深度相关的默认 blocklist 合并。
func mergeBlockedTools(userBlocked []string, depth int) []string {
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
	// 深度 >= 2 时额外 block 高风险工具
	if depth >= 2 {
		for _, name := range []string{"execute", "browser"} {
			if !seen[name] {
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
