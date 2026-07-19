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
}

func WithExecutionContext(ctx context.Context, value ExecutionContext) context.Context {
	return context.WithValue(ctx, executionContextKey{}, value)
}

func ExecutionContextFromContext(ctx context.Context) ExecutionContext {
	value, _ := ctx.Value(executionContextKey{}).(ExecutionContext)
	return value
}
