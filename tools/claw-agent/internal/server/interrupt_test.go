package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"os"
	"path/filepath"
	"testing"
)

func TestInterruptRejectsStaleAndDuplicateTurnWhileCancellingApproval(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(fmt.Sprintf("direct=%v", direct), func(t *testing.T) {
			model := &scriptedModel{root: func(_ int, r wireRequest) string {
				return sseToolCall("write", "write_file", `{"path":"should-not-exist.txt","content":"no"}`)
			}, child: func(wireRequest) string { return sseText("child") }}
			s, root := collabServer(t, model)
			writer := &receiptWriter{}
			s.out = writer
			raw, _ := json.Marshal(protocol.SessionConfigureParams{SessionID: root.ID, ApprovalPolicy: protocol.ApprovalAlways})
			s.dispatch(context.Background(), frame{ID: json.RawMessage(`"policy"`), Method: protocol.MethodSessionConfigure, Params: raw})
			waitApproval := func() frame {
				var request frame
				eventually(t, "approval", func() bool {
					writer.mu.Lock()
					defer writer.mu.Unlock()
					for _, f := range writer.frames {
						if f.Method == protocol.RequestApproval {
							request = f
							return true
						}
					}
					return false
				})
				return request
			}
			first := protocol.TurnStartResult{TurnID: "direct-turn"}
			if direct {
				go root.RunTurn(context.Background(), first.TurnID, "write first", nil, nil, &emitter{server: s})
			} else {
				first = submitQueued(t, s, root.ID, "one", "write first")
			}
			request := waitApproval()
			if s.interruptTurn(protocol.TurnInterruptParams{SessionID: root.ID, TurnID: "old"}) {
				t.Fatal("stale stop accepted")
			}
			if !root.Busy() {
				t.Fatal("stale stop cancelled active approval")
			}
			if !s.interruptTurn(protocol.TurnInterruptParams{SessionID: root.ID, TurnID: first.TurnID}) {
				t.Fatal("valid stop rejected")
			}
			if s.interruptTurn(protocol.TurnInterruptParams{SessionID: root.ID, TurnID: first.TurnID}) {
				t.Fatal("duplicate stop accepted")
			}
			eventually(t, "cancelled approval", func() bool {
				writer.mu.Lock()
				defer writer.mu.Unlock()
				for _, f := range writer.frames {
					if f.Method == "host/requestCancelled" {
						return true
					}
				}
				return false
			})
			eventually(t, "first completed", func() bool {
				s.submissionMu.Lock()
				defer s.submissionMu.Unlock()
				return s.activeTurns[root.ID] == "" && !root.Busy()
			})
			s.resolveOutbound(frame{ID: request.ID, Result: json.RawMessage(`{"approved":true}`)})
			writer.mu.Lock()
			writer.frames = nil
			writer.mu.Unlock()
			second := submitQueued(t, s, root.ID, "two", "write second")
			waitApproval()
			if s.interruptTurn(protocol.TurnInterruptParams{SessionID: root.ID, TurnID: first.TurnID}) {
				t.Fatal("late stop accepted for later turn")
			}
			if !root.Busy() {
				t.Fatal("later turn was stopped")
			}
			s.interruptTurn(protocol.TurnInterruptParams{SessionID: root.ID, TurnID: second.TurnID})
			eventually(t, "second completed", func() bool { return !root.Busy() })
			if _, err := os.Stat(filepath.Join(root.Workspace(), "should-not-exist.txt")); !os.IsNotExist(err) {
				t.Fatal("cancelled approval wrote a file")
			}
		})
	}
}
