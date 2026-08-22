package memory

import "context"

type executionContextKey struct{}

// ExecutionContext supplies the ownership and evidence boundary for an Agent
// tool call. It is intentionally server-generated rather than model-provided.
type ExecutionContext struct {
	UserID         string
	AgentUUID      string
	ConversationID int64
	MessageID      int64
	RunUUID        string
	Input          string
}

type TurnPolicy struct {
	UseMemories      bool
	GenerateMemories bool
}

type turnPolicyKey struct{}

func WithExecutionContext(ctx context.Context, value ExecutionContext) context.Context {
	return context.WithValue(ctx, executionContextKey{}, value)
}

func ExecutionContextFromContext(ctx context.Context) ExecutionContext {
	value, _ := ctx.Value(executionContextKey{}).(ExecutionContext)
	return value
}

func WithTurnPolicy(ctx context.Context, value TurnPolicy) context.Context {
	return context.WithValue(ctx, turnPolicyKey{}, value)
}

func TurnPolicyFromContext(ctx context.Context) TurnPolicy {
	value, ok := ctx.Value(turnPolicyKey{}).(TurnPolicy)
	if !ok {
		return TurnPolicy{UseMemories: true, GenerateMemories: true}
	}
	return value
}
