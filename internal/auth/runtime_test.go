package auth

import (
	"context"
	"database/sql"
	"testing"

	"github.com/chowyu12/aiclaw/internal/model"
)

type runtimeAuthStore struct{}

func (runtimeAuthStore) GetAgentByToken(context.Context, string) (*model.Agent, error) {
	return nil, sql.ErrNoRows
}

func (runtimeAuthStore) GetRuntimeByToken(_ context.Context, token string) (*model.Runtime, error) {
	if token != "rt-valid" {
		return nil, sql.ErrNoRows
	}
	return &model.Runtime{ID: 42, Token: token}, nil
}

func TestRuntimeTokenScope(t *testing.T) {
	store := runtimeAuthStore{}
	id, err := authenticate(Config{AgentStore: store, RuntimeStore: store}, t.Context(), "rt-valid")
	if err != nil {
		t.Fatal(err)
	}
	if !id.IsRuntime() || id.RuntimeID != 42 {
		t.Fatalf("unexpected identity: %#v", id)
	}
	if err := authorize(id, "/api/v1/runtime-daemon/tasks/claim"); err != nil {
		t.Fatalf("runtime endpoint should be allowed: %v", err)
	}
	if err := authorize(id, "/api/v1/agents"); err == nil {
		t.Fatal("runtime token must not access management endpoints")
	}
}
