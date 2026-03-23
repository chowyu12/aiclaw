package auth

import (
	"context"

	"github.com/chowyu12/aiclaw/internal/agent"
	"github.com/chowyu12/aiclaw/internal/model"
)

// AgentTokenResolver 从内存单例 Agent（由 config.yaml 的 agent 段加载）校验 Agent API Token。
type AgentTokenResolver struct {
	Providers agent.ProviderLister
}

func (r AgentTokenResolver) GetAgentByToken(ctx context.Context, token string) (*model.Agent, error) {
	return agent.GetAgentByToken(ctx, token, r.Providers)
}
