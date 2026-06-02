package agent

import (
	"context"
	"fmt"
)

func (e *Executor) planHandler(ctx context.Context, args string) (string, error) {
	pm := planManagerFromContext(ctx)
	if pm == nil {
		return planErr(planToolName, "plan state is not available in this execution context"), nil
	}
	out, err := pm.HandleTool(ctx, args)
	if err != nil {
		return "", fmt.Errorf("plan: %w", err)
	}
	return out, nil
}
