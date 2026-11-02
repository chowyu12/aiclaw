package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

func TestResponsesToolTurnSurvivesColdResume(t *testing.T) {
	var paths []string
	var inputs []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input     json.RawMessage `json:"input"`
			Reasoning struct {
				Effort string `json:"effort"`
			} `json:"reasoning"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		paths = append(paths, r.URL.Path)
		if r.URL.Path != "/responses" {
			t.Error("wrong configured protocol", r.URL.Path)
		}
		if req.Reasoning.Effort != "medium" {
			t.Error("model effort was silently disabled")
		}
		inputs = append(inputs, string(req.Input))
		output := `[{"type":"message","role":"assistant","status":"completed","phase":"final_answer","content":[{"type":"output_text","text":"read complete","annotations":[]}]}]`
		if len(inputs) == 1 {
			output = `[{"type":"reasoning","id":"rs_test","summary":[],"encrypted_content":"opaque-local-test"},{"type":"function_call","id":"fc_test","call_id":"read_test","name":"read_file","arguments":"{\"path\":\"sample.txt\"}","status":"completed"}]`
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"status":"completed","output":` + output + `,"usage":{"input_tokens":100,"output_tokens":10,"total_tokens":110}}}` + "\n\n"))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := New(ctx, "responses-resume", protocol.SessionStartParams{Workdir: t.TempDir(), ApprovalPolicy: protocol.ApprovalBypass,
		Model: protocol.ModelConfig{APIProtocol: model.APIResponses, BaseURL: server.URL, Model: "gpt-6-astra", ReasoningEffort: "medium", ReasoningExplicit: true}}, StaticKey("fake"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = os.WriteFile(filepath.Join(s.Workspace(), "sample.txt"), []byte("local-read-evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	db := persistTestSession(t, s)
	emitter := &recordingEmitter{}
	s.RunTurn(ctx, "t1", "read sample.txt", nil, nil, emitter)
	if !emitter.find(protocol.NotifyItemCompleted, "read complete") || emitter.find(protocol.NotifyTurnCompleted, `"error"`) {
		t.Fatal("tool turn failed", emitter.events)
	}
	if len(inputs) != 2 || !strings.Contains(inputs[1], "local-read-evidence") || !strings.Contains(inputs[1], "opaque-local-test") {
		t.Fatal("tool result or encrypted reasoning was lost", inputs)
	}
	s.Close()
	resumed, err := Load(ctx, db, s.ID, StaticKey("fake"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	resumed.RunTurn(ctx, "t2", "continue", nil, nil, &recordingEmitter{})
	if strings.Join(paths, ",") != "/responses,/responses,/responses" || len(inputs) != 3 || !strings.Contains(inputs[2], "opaque-local-test") || !strings.Contains(inputs[2], "final_answer") {
		t.Fatal("cold resume lost protocol or replay state", paths, inputs)
	}
	if resumed.Model().ReasoningEffort != "medium" {
		t.Fatal("effort fallback changed persisted configuration")
	}
	for _, session := range []*Session{s, resumed} {
		history, _ := json.Marshal(session.History())
		if strings.Contains(string(history), "opaque-local-test") {
			t.Fatal("opaque reasoning should not appear in visible history")
		}
	}
}
