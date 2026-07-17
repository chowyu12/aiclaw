package handler

import (
	"reflect"
	"testing"

	"github.com/chowyu12/aiclaw/internal/model"
)

func TestNormalizeDetectedAgents(t *testing.T) {
	agents, ok := normalizeDetectedAgents([]string{
		model.RuntimeAgentTypeCodex,
		" Hermes ",
		model.RuntimeAgentTypeCodex,
	})
	if !ok {
		t.Fatal("expected supported agent list")
	}
	want := []string{model.RuntimeAgentTypeCodex, model.RuntimeAgentTypeHermes}
	if !reflect.DeepEqual(agents, want) {
		t.Fatalf("agents = %v, want %v", agents, want)
	}

	if _, ok := normalizeDetectedAgents([]string{model.RuntimeAgentTypeCustom}); ok {
		t.Fatal("custom must not be accepted as an auto-detected agent")
	}
	if _, ok := normalizeDetectedAgents([]string{"unknown"}); ok {
		t.Fatal("unknown agent must not be accepted")
	}
}
