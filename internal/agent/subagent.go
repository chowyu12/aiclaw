package agent

import (
	"context"
	"encoding/json"
	"fmt"

	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/workspace"
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

// propagateAgentValues 将 Agent 层面的 context 值传播到 toolCallContext 创建的脱离 context。
func propagateAgentValues(parent context.Context) context.Context {
	ctx := context.Background()
	if d := subAgentDepth(parent); d > 0 {
		ctx = withSubAgentDepth(ctx, d)
	}
	if id := workspace.WorkdirScopeFromContext(parent); id != "" {
		ctx = workspace.WithWorkdirScope(ctx, id)
	}
	return ctx
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

	log.WithFields(log.Fields{
		"depth":      depth,
		"agent_uuid": params.AgentUUID,
		"prompt":     truncateLog(params.Prompt, 200),
	}).Info("[SubAgent] >> spawning")

	childCtx := withSubAgentDepth(ctx, depth)
	req := model.ChatRequest{
		AgentUUID: params.AgentUUID,
		Message:   params.Prompt,
		UserID:    fmt.Sprintf("sub_agent:depth_%d", depth),
	}

	result, err := e.Execute(childCtx, req)
	if err != nil {
		log.WithError(err).WithField("depth", depth).Warn("[SubAgent] << failed")
		return fmt.Sprintf("sub-agent execution failed: %s", err), nil
	}

	log.WithFields(log.Fields{
		"depth":  depth,
		"tokens": result.TokensUsed,
		"len":    len(result.Content),
	}).Info("[SubAgent] << done")
	return result.Content, nil
}
