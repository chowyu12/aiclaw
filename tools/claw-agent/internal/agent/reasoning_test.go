package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

func TestReasoningChangeAppliesAtNextSamplingAndPersists(t *testing.T) {
	requests := make(chan string, 4)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Effort string `json:"reasoning_effort"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		requests <- req.Effort
		w.Header().Set("Content-Type", "text/event-stream")
		if req.Effort == "low" {
			<-release
			io.WriteString(w, sseToolCall("r", "read_file", `{"path":"sample.txt"}`))
		} else {
			io.WriteString(w, sseText("done"))
		}
	}))
	defer server.Close()
	s, err := New(context.Background(), "reasoning", protocol.SessionStartParams{Model: protocol.ModelConfig{BaseURL: server.URL, Model: "fixture", ReasoningEffort: "low", ContextWindow: 12345}, Workdir: t.TempDir(), ApprovalPolicy: protocol.ApprovalBypass}, StaticKey("fake"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = os.WriteFile(filepath.Join(s.Workspace(), "sample.txt"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	db := persistTestSession(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { s.RunTurn(ctx, "t", "read", nil, nil, &recordingEmitter{}); close(done) }()
	if got := <-requests; got != "low" {
		t.Fatal(got)
	}
	if err = s.ConfigureReasoning(ctx, "high"); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("turn did not finish")
	}
	if got := <-requests; got != "high" {
		t.Fatal(got)
	}
	if s.Model().ContextWindow != 12345 {
		t.Fatal("effort change replaced model settings")
	}
	record, err := db.Load(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	var config protocol.SessionStartParams
	_ = json.Unmarshal(record.Config, &config)
	if config.Model.ReasoningEffort != "high" {
		t.Fatal("not persisted")
	}
	if err = s.ConfigureReasoning(ctx, "invalid"); err == nil {
		t.Fatal("invalid effort accepted")
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if err = s.ConfigureReasoning(ctx, "low"); err == nil || s.Model().ReasoningEffort != "high" {
		t.Fatal("failed save did not roll back")
	}
}
