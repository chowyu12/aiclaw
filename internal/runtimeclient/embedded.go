package runtimeclient

import (
	"context"
	"fmt"
	"time"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/store"
)

// EnsureBuiltinRuntime creates the runtime that executes agent CLIs on the
// same machine as AiClaw. It is safe to call on every server startup.
func EnsureBuiltinRuntime(ctx context.Context, s store.RuntimeStore, version string) (*model.Runtime, error) {
	items, _, err := s.ListRuntimes(ctx, model.ListQuery{Page: 1, PageSize: 1000})
	if err != nil {
		return nil, fmt.Errorf("list runtimes: %w", err)
	}

	var runtime *model.Runtime
	for _, item := range items {
		if item.Builtin {
			runtime = item
			break
		}
	}
	if runtime == nil {
		runtime = &model.Runtime{
			Name:        model.BuiltinLocalRuntimeName,
			Description: model.BuiltinLocalRuntimeDescription,
			Builtin:     true,
			AgentType:   model.RuntimeAgentTypeCustom,
		}
		if err := s.CreateRuntime(ctx, runtime); err != nil {
			return nil, fmt.Errorf("create built-in runtime: %w", err)
		}
	}

	now := time.Now()
	detected := DetectLocalAgents()
	if err := s.TouchRuntime(ctx, runtime.ID, version, detected, now); err != nil {
		return nil, fmt.Errorf("refresh built-in runtime: %w", err)
	}
	runtime.Status = model.RuntimeStatusOnline
	runtime.Version = version
	runtime.DetectedAgents = model.StringSlice(detected)
	runtime.LastSeenAt = &now
	return runtime, nil
}
