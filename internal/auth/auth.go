package auth

import (
	"context"
)

type Kind string

const (
	KindWebSession Kind = "web_session"
	KindAgentToken Kind = "agent_token"
)

// Identity 请求身份：Web 控制台会话或 Agent API Token。
type Identity struct {
	Kind Kind
}

func (id *Identity) IsWebSession() bool {
	return id != nil && id.Kind == KindWebSession
}

func (id *Identity) IsAgentToken() bool {
	return id != nil && id.Kind == KindAgentToken
}

// DefaultChatUserID Web 登录用户在对话中的默认 user_id（无多账户时统一归档）。
const DefaultChatUserID = "default"

type ctxKey struct{}

func WithIdentity(ctx context.Context, id *Identity) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

func IdentityFromContext(ctx context.Context) *Identity {
	id, _ := ctx.Value(ctxKey{}).(*Identity)
	return id
}
