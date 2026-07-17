package runtimeclient

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/chowyu12/aiclaw/internal/model"
)

type runtimeStoreFake struct {
	nextID   int64
	runtimes map[int64]*model.Runtime
}

func newRuntimeStoreFake() *runtimeStoreFake {
	return &runtimeStoreFake{runtimes: make(map[int64]*model.Runtime)}
}

func (s *runtimeStoreFake) CreateRuntime(_ context.Context, runtime *model.Runtime) error {
	s.nextID++
	runtime.ID = s.nextID
	if runtime.Token == "" {
		runtime.Token = fmt.Sprintf("rt-%d", runtime.ID)
	}
	cp := *runtime
	s.runtimes[runtime.ID] = &cp
	return nil
}

func (s *runtimeStoreFake) GetRuntime(_ context.Context, id int64) (*model.Runtime, error) {
	runtime := s.runtimes[id]
	if runtime == nil {
		return nil, sql.ErrNoRows
	}
	cp := *runtime
	return &cp, nil
}

func (s *runtimeStoreFake) GetRuntimeByToken(_ context.Context, token string) (*model.Runtime, error) {
	for _, runtime := range s.runtimes {
		if runtime.Token == token {
			cp := *runtime
			return &cp, nil
		}
	}
	return nil, sql.ErrNoRows
}

func (s *runtimeStoreFake) ListRuntimes(_ context.Context, _ model.ListQuery) ([]*model.Runtime, int64, error) {
	items := make([]*model.Runtime, 0, len(s.runtimes))
	for _, runtime := range s.runtimes {
		cp := *runtime
		items = append(items, &cp)
	}
	return items, int64(len(items)), nil
}

func (s *runtimeStoreFake) UpdateRuntime(context.Context, int64, model.UpdateRuntimeReq) error {
	return nil
}

func (s *runtimeStoreFake) TouchRuntime(_ context.Context, id int64, version string, detected []string, seenAt time.Time) error {
	runtime := s.runtimes[id]
	if runtime == nil {
		return sql.ErrNoRows
	}
	runtime.Status = model.RuntimeStatusOnline
	runtime.Version = version
	runtime.DetectedAgents = model.StringSlice(detected)
	runtime.LastSeenAt = &seenAt
	return nil
}

func (s *runtimeStoreFake) ResetRuntimeToken(context.Context, int64) (string, error) {
	return "", nil
}

func (s *runtimeStoreFake) DeleteRuntime(context.Context, int64) error {
	return nil
}

func TestEnsureBuiltinRuntimeCreatesAndRefreshesOneRuntime(t *testing.T) {
	store := newRuntimeStoreFake()
	first, err := EnsureBuiltinRuntime(t.Context(), store, "test")
	if err != nil {
		t.Fatal(err)
	}
	if !first.Builtin || first.Name != model.BuiltinLocalRuntimeName || first.Status != model.RuntimeStatusOnline {
		t.Fatalf("unexpected built-in runtime: %#v", first)
	}
	if first.LastSeenAt == nil {
		t.Fatal("built-in runtime must record a startup heartbeat")
	}

	second, err := EnsureBuiltinRuntime(t.Context(), store, "test")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || len(store.runtimes) != 1 {
		t.Fatalf("expected one reused built-in runtime, got first=%d second=%d count=%d", first.ID, second.ID, len(store.runtimes))
	}
}
