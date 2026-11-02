package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

func TestDefaultPreferenceWorksButExplicitOverrideIsRetained(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["reasoning_effort"] != nil {
			t.Error("default reasoning preference blocked ordinary model")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(sseText("completed")))
	}))
	defer srv.Close()
	resolver := func(m *protocol.ModelConfig) (string, error) {
		m.BaseURL = srv.URL
		m.Capabilities = model.ModelCapabilities{Tools: "supported", Reasoning: "unsupported"}
		return "fixture", nil
	}
	ctx := context.Background()
	s, err := New(ctx, "reasoning-preference", protocol.SessionStartParams{Workdir: t.TempDir(), Model: protocol.ModelConfig{Model: "ordinary", ReasoningEffort: "medium"}}, resolver)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	db := persistTestSession(t, s)
	emitter := &recordingEmitter{}
	s.RunTurn(ctx, "t1", "hello", nil, nil, emitter)
	if calls != 1 || !emitter.find(protocol.NotifyItemCompleted, "completed") || s.Model().ReasoningEffort != "medium" || s.Model().ReasoningExplicit {
		t.Fatal("preference was changed or failed", s.Model(), calls)
	}
	if err = s.ConfigureReasoning(ctx, "high"); err != nil {
		t.Fatal(err)
	}
	restored, err := Load(ctx, db, s.ID, resolver, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	emitter = &recordingEmitter{}
	restored.RunTurn(ctx, "t2", "continue", nil, nil, emitter)
	if calls != 1 || !restored.Model().ReasoningExplicit || restored.Model().ReasoningEffort != "high" || !emitter.find(protocol.NotifyTurnCompleted, "reasoning settings") {
		t.Fatal("explicit setting was lost or silently retried", restored.Model(), calls, emitter.events)
	}
}

func TestFailedReasoningSaveRestoresPreferenceOrigin(t *testing.T) {
	s := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	s.config.Model.ReasoningEffort = "medium"
	db := persistTestSession(t, s)
	_ = db.Close()
	if err := s.ConfigureReasoning(context.Background(), "high"); err == nil || s.Model().ReasoningExplicit || s.Model().ReasoningEffort != "medium" {
		t.Fatal("failed save changed preference origin", s.Model(), err)
	}
}

func TestModelSwitchKeepsExplicitReasoningChoice(t *testing.T) {
	s := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	if err := s.ConfigureReasoning(context.Background(), "high"); err != nil {
		t.Fatal(err)
	}
	next := s.Model()
	next.Model = "another-model"
	next.ReasoningExplicit = false
	if err := s.Configure(next); err != nil {
		t.Fatal(err)
	}
	if !s.Model().ReasoningExplicit || s.Model().ReasoningEffort != "high" {
		t.Fatal("model switch converted explicit choice to a preference")
	}
}
