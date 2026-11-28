package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

type approvalReplayEmitter struct {
	recordingEmitter
	approveCommand func() error
	onCompleted    func(protocol.Item)
}

func (e *approvalReplayEmitter) RequestApproval(context.Context, protocol.ApprovalRequestParams) (protocol.ApprovalResponse, error) {
	return protocol.ApprovalResponse{}, e.approveCommand()
}

func (e *approvalReplayEmitter) Notify(method string, params any) {
	e.recordingEmitter.Notify(method, params)
	if item, ok := params.(protocol.ItemNotification); method == protocol.NotifyItemCompleted && ok && item.Item.ToolName == "run_command" {
		e.onCompleted(item.Item)
	}
}

func TestApprovalCompletionIsPersistedBeforePublishing(t *testing.T) {
	for _, outcome := range []protocol.ApprovalOutcome{protocol.ApprovalDeclined, protocol.ApprovalCanceled, protocol.ApprovalFailed} {
		t.Run(string(outcome), func(t *testing.T) {
			model := &fakeModel{script: []string{
				sseToolCall("approval-command", "run_command", `{"command":"echo approved > approval-must-not-run.txt"}`),
				sseText("Understood."),
			}}
			s := newTestSession(t, model, protocol.ApprovalAlways)
			db := newTestStore(t)
			s.SetPersistence(db)
			if err := s.Save(t.Context(), db); err != nil {
				t.Fatal(err)
			}
			completed := 0
			e := &approvalReplayEmitter{
				approveCommand: func() error {
					switch outcome {
					case protocol.ApprovalCanceled:
						s.Interrupt()
						return context.Canceled
					case protocol.ApprovalFailed:
						return errors.New("malformed approval fixture")
					}
					return nil
				},
				onCompleted: func(item protocol.Item) {
					completed++
					if item.ApprovalOutcome != outcome || !item.ToolFailed || item.ToolResult == "" {
						t.Errorf("unexpected completion: %+v", item)
					}
					history, err := StoredHistory(t.Context(), db, s.ID)
					if err != nil {
						t.Error(err)
						return
					}
					for _, saved := range history {
						if saved.ID == item.ID && saved.ApprovalOutcome == item.ApprovalOutcome && saved.ToolResult == item.ToolResult && saved.ToolFailed {
							return
						}
					}
					t.Error("approval completion was not in the stored transcript when published")
				},
			}
			s.RunTurn(t.Context(), "turn", "Ask before executing", nil, nil, e)
			if completed != 1 {
				t.Fatalf("expected one completion, got %d", completed)
			}
			if _, err := os.Stat(filepath.Join(s.config.Workdir, "approval-must-not-run.txt")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("command ran or file state was unclear: %v", err)
			}
			if !e.find(protocol.NotifyTurnCompleted, "turn") {
				t.Fatal("turn never ended")
			}
		})
	}
}
