package agent

import (
	"context"
	"encoding/json"
	"fmt"

	openai "github.com/chowyu12/go-openai"
	log "github.com/sirupsen/logrus"
)

const maxSubAgentDepth = 3

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

// subAgentRoundCountKey 传递本轮 sub_agent 调用总数，供 handler 决定是否走轻量路径。
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

// ── 父执行上下文传递 ─────────────────────────────────────────
// sub_agent handler 通过 context 获取父 tracker 和会话 ID，
// 使子 agent 的执行步骤记录在父会话中，而非创建独立会话。

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

// subAgentCallIDKey 标识当前 sub_agent 调用对应的父级 tool_call_id，
// 使同一 sub_agent 内的所有步骤可以关联到具体的调用。
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

func (e *Executor) subAgentHandler(ctx context.Context, args string) (string, error) {
	depth := subAgentDepth(ctx) + 1
	if depth > maxSubAgentDepth {
		return "", fmt.Errorf("sub-agent depth limit (%d) reached, cannot create deeper sub-agents", maxSubAgentDepth)
	}

	var params struct {
		Prompt    string `json:"prompt"`
		AgentUUID string `json:"agent_uuid,omitempty"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("parse sub_agent arguments: %w", err)
	}
	if params.Prompt == "" {
		return "", fmt.Errorf("prompt is required")
	}

	// 单个 sub_agent 走轻量 inline 路径（无工具、无会话）
	if subAgentRoundCount(ctx) <= 1 {
		log.WithFields(log.Fields{
			"depth":  depth,
			"prompt": truncateLog(params.Prompt, 200),
		}).Info("[SubAgent] >> inline (single sub_agent, skipping full Execute)")
		return e.inlineSubAgentCall(ctx, params.Prompt, params.AgentUUID)
	}

	// 多并发 sub_agent：完整工具支持，步骤记录在父会话
	log.WithFields(log.Fields{
		"depth":      depth,
		"agent_uuid": params.AgentUUID,
		"prompt":     truncateLog(params.Prompt, 200),
	}).Info("[SubAgent] >> spawning (parallel)")

	childCtx := withSubAgentDepth(ctx, depth)
	return e.executeAsSubAgent(childCtx, params.Prompt, params.AgentUUID)
}

// executeAsSubAgent 在父会话上下文内执行子 agent：
//   - 使用父 tracker 记录步骤（带 SubAgentDepth 标记）
//   - 不创建独立会话、不持久化消息
//   - 完整加载工具/MCP/skills
func (e *Executor) executeAsSubAgent(ctx context.Context, prompt, agentUUID string) (string, error) {
	if err := e.checkShutdown(); err != nil {
		return "", err
	}
	defer e.activeExecs.Done()

	ec, err := e.prepareSubAgent(ctx, prompt, agentUUID)
	if err != nil {
		return "", fmt.Errorf("sub_agent prepare: %w", err)
	}
	defer ec.closeMCP()

	ec.l.Debug("[SubAgent] >> executeAsSubAgent start")

	res, err := e.run(ec.ctx, ec, blockingCaller(ec.llmProv), false)
	if err != nil {
		ec.l.WithError(err).Warn("[SubAgent] << failed")
		return fmt.Sprintf("sub-agent execution failed: %s", err), nil
	}

	ec.l.WithFields(log.Fields{
		"tokens": res.TokensUsed,
		"len":    len(res.Content),
	}).Info("[SubAgent] << done")
	return res.Content, nil
}

// inlineSubAgentCall 轻量子任务执行：单次 LLM 调用，不创建 conversation、不加载工具/MCP/skills。
// 适用于只有 1 个 sub_agent 的场景，避免完整 Execute 流程的大量开销。
func (e *Executor) inlineSubAgentCall(ctx context.Context, prompt, agentUUID string) (string, error) {
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

	resp, err := llmProv.CreateChatCompletion(ctx, req)
	if err != nil {
		return "", fmt.Errorf("inline sub_agent: LLM call: %w", err)
	}

	content := extractContent(resp)
	l.WithFields(log.Fields{
		"tokens": resp.Usage.TotalTokens,
		"len":    len(content),
	}).Info("[SubAgent] << inline done")
	return content, nil
}
