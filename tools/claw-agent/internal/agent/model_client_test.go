package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

func TestLateSampleUsageDoesNotBelongToReplacementModel(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(sseText("old model result")))
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := New(ctx, "usage-snapshot", protocol.SessionStartParams{Workdir: t.TempDir(), Model: protocol.ModelConfig{BaseURL: srv.URL, Model: "old"}}, StaticKey("fixture"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	done := make(chan struct{})
	go func() { s.RunTurn(ctx, "t", "hello", nil, nil, &recordingEmitter{}); close(done) }()
	select {
	case <-entered:
	case <-ctx.Done():
		close(release)
		t.Fatal("request not started")
	}
	if err = s.Configure(protocol.ModelConfig{BaseURL: srv.URL, Model: "new"}); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("request did not complete")
	}
	status, err := s.ContextStatus()
	if err != nil || status.Model != "new" || status.LastInputTokens != 0 {
		t.Fatal("old usage was assigned to the new model", status, err)
	}
}

func TestServiceEditAppliesToNextSamplingWithoutChangingInFlightRequest(t *testing.T) {
	var useResponses atomic.Bool
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "text/event-stream")
		if len(paths) == 1 {
			if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer old-key" {
				t.Error("first snapshot changed")
			}
			useResponses.Store(true)
			w.Write([]byte(sseToolCall("c", "read_file", `{"path":"fixture.txt"}`)))
		} else {
			if r.URL.Path != "/responses" || r.Header.Get("Authorization") != "Bearer new-key" {
				t.Error("next sampling retained stale service configuration")
			}
			var request map[string]any
			_ = json.NewDecoder(r.Body).Decode(&request)
			if request["messages"] != nil || request["input"] == nil {
				t.Error("adapter was not refreshed")
			}
			w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"finished\"}]}]}}\n\n"))
		}
	}))
	defer srv.Close()
	resolver := func(m *protocol.ModelConfig) (string, error) {
		m.BaseURL = srv.URL
		if useResponses.Load() {
			m.APIProtocol = model.APIResponses
			m.ProtocolSource = "service-override"
			return "new-key", nil
		}
		m.APIProtocol = model.APIChatCompletions
		return "old-key", nil
	}
	s, err := New(context.Background(), "refresh-service", protocol.SessionStartParams{Workdir: t.TempDir(), ApprovalPolicy: protocol.ApprovalBypass, Model: protocol.ModelConfig{Model: "fixture"}}, resolver)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = os.WriteFile(filepath.Join(s.Workspace(), "fixture.txt"), []byte("local result"), 0600); err != nil {
		t.Fatal(err)
	}
	db := persistTestSession(t, s)
	emitter := &recordingEmitter{}
	s.RunTurn(context.Background(), "t1", "read fixture.txt", nil, nil, emitter)
	if len(paths) != 2 || !emitter.find(protocol.NotifyItemCompleted, "finished") || s.Model().APIProtocol != model.APIResponses {
		t.Fatal("same turn did not refresh", paths, s.Model(), emitter.events)
	}
	restored, err := Load(context.Background(), db, s.ID, resolver, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if restored.Model().APIProtocol != model.APIResponses {
		t.Fatal("effective route not saved")
	}
}
