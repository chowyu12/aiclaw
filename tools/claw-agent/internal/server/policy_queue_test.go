package server

import (
	"context"
	"encoding/json"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestQueuedTurnUsesAcknowledgedPolicyChange(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	model := &scriptedModel{root: func(_ int, r wireRequest) string {
		if !strings.Contains(r.text(), "queued protected write") {
			<-release
			return sseText("first done")
		}
		if r.toolResults() == 0 {
			return sseToolCall("write", "write_file", `{"path":"queued.txt","content":"must require approval"}`)
		}
		return sseText("second done")
	}, child: func(wireRequest) string { return sseText("child") }}
	s, root := collabServer(t, model)
	writer := &receiptWriter{}
	s.out = writer
	submitQueued(t, s, root.ID, "first", "first task")
	eventually(t, "first sampling", func() bool { model.mu.Lock(); defer model.mu.Unlock(); return len(model.requests) == 1 })
	submitQueued(t, s, root.ID, "second", "queued protected write")
	raw, _ := json.Marshal(protocol.SessionConfigureParams{SessionID: root.ID, ApprovalPolicy: protocol.ApprovalAlways})
	s.dispatch(context.Background(), frame{ID: json.RawMessage(`"policy"`), Method: protocol.MethodSessionConfigure, Params: raw})
	writer.mu.Lock()
	ack := false
	for _, f := range writer.frames {
		if string(f.ID) == `"policy"` {
			ack = f.Error == nil
		}
	}
	writer.mu.Unlock()
	if !ack {
		t.Fatal("policy not acknowledged")
	}
	once.Do(func() { close(release) })
	var approval frame
	eventually(t, "queued turn approval", func() bool {
		writer.mu.Lock()
		defer writer.mu.Unlock()
		for _, f := range writer.frames {
			if f.Method == protocol.RequestApproval {
				approval = f
				return true
			}
		}
		return false
	})
	if _, err := os.Stat(filepath.Join(root.Workspace(), "queued.txt")); !os.IsNotExist(err) {
		t.Fatal("queued write bypassed new strict policy")
	}
	s.resolveOutbound(frame{ID: approval.ID, Result: json.RawMessage(`{"approved":false}`)})
	eventually(t, "second completion", func() bool {
		r, _ := s.db.Submission(context.Background(), root.ID, "second")
		return r.State == "completed" && !root.Busy()
	})
}
