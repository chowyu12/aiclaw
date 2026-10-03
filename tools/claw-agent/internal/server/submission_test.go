package server

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

type receiptWriter struct {
	mu     sync.Mutex
	frames []frame
}

func (w *receiptWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var f frame
	if err := json.Unmarshal(data, &f); err != nil {
		return 0, err
	}
	w.frames = append(w.frames, f)
	return len(data), nil
}

func TestSubmissionRetryIsIdempotentAndPayloadConflictsFail(t *testing.T) {
	model := &scriptedModel{root: func(int, wireRequest) string { return sseText("answer") }, child: func(wireRequest) string { return sseText("child") }}
	server, root := collabServer(t, model)
	writer := &receiptWriter{}
	server.out = writer
	input := protocol.TurnStartParams{SessionID: root.ID, RequestID: "request-1", Text: "exactly once"}
	params, _ := json.Marshal(input)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			server.handleTurnStart(context.Background(), frame{ID: json.RawMessage(`1`), Params: params})
		}()
	}
	wg.Wait()
	eventually(t, "completion", func() bool { return strings.Contains(root.LastAnswer(), "answer") && !root.Busy() })
	input.Text = "different payload"
	params, _ = json.Marshal(input)
	server.handleTurnStart(context.Background(), frame{ID: json.RawMessage(`2`), Params: params})
	model.mu.Lock()
	calls := len(model.requests)
	model.mu.Unlock()
	if calls != 1 {
		t.Fatalf("retried input executed %d times", calls)
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	var turnID string
	receipts := 0
	conflict := false
	for _, f := range writer.frames {
		if string(f.ID) == "1" {
			var receipt protocol.TurnStartResult
			if err := json.Unmarshal(f.Result, &receipt); err != nil {
				t.Fatal(err)
			}
			if turnID != "" && turnID != receipt.TurnID {
				t.Fatal("retry changed turn id")
			}
			turnID = receipt.TurnID
			receipts++
		}
		if string(f.ID) == "2" {
			conflict = f.Error != nil
		}
	}
	if receipts != 12 || turnID == "" || !conflict {
		t.Fatalf("receipts=%d turn=%q conflict=%v", receipts, turnID, conflict)
	}
}
