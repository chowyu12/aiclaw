package server

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

func submitQueued(t *testing.T, server *Server, sessionID, requestID, text string, attachments ...protocol.TurnStartParams) protocol.TurnStartResult {
	t.Helper()
	writer := server.out.(*receiptWriter)
	params := protocol.TurnStartParams{SessionID: sessionID, RequestID: requestID, Text: text, Mode: "queue"}
	if len(attachments) > 0 {
		params.Images = attachments[0].Images
		params.AudioPaths = attachments[0].AudioPaths
		params.References = attachments[0].References
	}
	input, _ := json.Marshal(params)
	server.handleTurnStart(context.Background(), frame{ID: json.RawMessage(`"` + requestID + `"`), Params: input})
	writer.mu.Lock()
	defer writer.mu.Unlock()
	for _, f := range writer.frames {
		if string(f.ID) == `"`+requestID+`"` {
			if f.Error != nil {
				t.Fatalf("submit: %+v", f.Error)
			}
			var result protocol.TurnStartResult
			_ = json.Unmarshal(f.Result, &result)
			return result
		}
	}
	t.Fatal("missing receipt")
	return protocol.TurnStartResult{}
}

func TestQueuedTurnsWaitWithoutSteeringAndRunFIFO(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	model := &scriptedModel{root: func(_ int, r wireRequest) string {
		text := r.text()
		if !strings.Contains(text, "second task") {
			<-release
		}
		return sseText("done")
	}, child: func(wireRequest) string { return sseText("child") }}
	server, root := collabServer(t, model)
	server.out = &receiptWriter{}
	first := submitQueued(t, server, root.ID, "one", "first task")
	eventually(t, "first model request", func() bool { model.mu.Lock(); defer model.mu.Unlock(); return len(model.requests) == 1 })
	second := submitQueued(t, server, root.ID, "two", "second task")
	third := submitQueued(t, server, root.ID, "three", "third task")
	if !second.Queued || !third.Queued || first.TurnID == second.TurnID || second.TurnID == third.TurnID {
		t.Fatalf("receipts: %+v %+v %+v", first, second, third)
	}
	time.Sleep(60 * time.Millisecond)
	model.mu.Lock()
	calls := len(model.requests)
	model.mu.Unlock()
	if calls != 1 {
		t.Fatalf("queue interrupted sampling: %d requests", calls)
	}
	pending, err := server.db.PendingSubmissions(context.Background(), root.ID)
	if err != nil || len(pending) != 2 {
		t.Fatalf("durable queue: %+v %v", pending, err)
	}
	once.Do(func() { close(release) })
	eventually(t, "FIFO completion", func() bool {
		record, _ := server.db.Submission(context.Background(), root.ID, "three")
		return record.State == "completed" && !root.Busy()
	})
	model.mu.Lock()
	requests := append([]wireRequest(nil), model.requests...)
	model.mu.Unlock()
	if len(requests) != 3 || !strings.Contains(requests[1].text(), "second task") || strings.Contains(requests[1].text(), "third task") || !strings.Contains(requests[2].text(), "third task") {
		t.Fatalf("not separate FIFO turns: %+v", requests)
	}
}

func TestQueuedInputCanBeCanceledOrPromotedExactlyOnce(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	model := &scriptedModel{root: func(_ int, r wireRequest) string {
		if !strings.Contains(r.text(), "change direction") {
			<-release
		}
		return sseText("guided")
	}, child: func(wireRequest) string { return sseText("child") }}
	server, root := collabServer(t, model)
	server.out = &receiptWriter{}
	first := submitQueued(t, server, root.ID, "one", "original task")
	eventually(t, "first model request", func() bool { model.mu.Lock(); defer model.mu.Unlock(); return len(model.requests) == 1 })
	submitQueued(t, server, root.ID, "cancel", "never execute", protocol.TurnStartParams{Images: [][]byte{[]byte("image")}, AudioPaths: []string{"recording.wav"}, References: []protocol.ThreadRef{{ID: "other", Title: "Reference"}}})
	stored, _ := server.db.Submission(context.Background(), root.ID, "cancel")
	var payload protocol.TurnStartParams
	if json.Unmarshal(stored.Payload, &payload) != nil || len(payload.Images) != 1 || string(payload.Images[0]) != "image" || len(payload.AudioPaths) != 1 || payload.AudioPaths[0] != "recording.wav" || len(payload.References) != 1 || payload.References[0].ID != "other" {
		t.Fatal("queued attachments/references were not retained")
	}
	view := workCall(t, server, "session/work", map[string]string{"sessionId": root.ID})
	if strings.Contains(string(view.Result), `"images"`) || strings.Contains(string(view.Result), "recording.wav") {
		t.Fatal("queue UI snapshot unnecessarily includes attachment payloads")
	}
	guided := submitQueued(t, server, root.ID, "guide", "change direction")
	workCall(t, server, "session/recover", map[string]string{"sessionId": root.ID, "requestId": "cancel", "action": "dismiss"})
	workCall(t, server, "session/recover", map[string]string{"sessionId": root.ID, "requestId": "guide", "action": "steer"})
	eventually(t, "steered completion", func() bool { return strings.Contains(root.LastAnswer(), "guided") && !root.Busy() })
	record, _ := server.db.Submission(context.Background(), root.ID, "guide")
	if record.State != "completed" || record.TurnID != first.TurnID || record.TurnID == guided.TurnID {
		t.Fatalf("not steered into current turn: %+v", record)
	}
	record, _ = server.db.Submission(context.Background(), root.ID, "cancel")
	if record.State != "dismissed" {
		t.Fatalf("not canceled: %+v", record)
	}
	model.mu.Lock()
	requests := append([]wireRequest(nil), model.requests...)
	model.mu.Unlock()
	if len(requests) != 2 || strings.Contains(requests[1].text(), "never execute") {
		t.Fatalf("unexpected execution: %+v", requests)
	}
	once.Do(func() { close(release) })
}

func TestStoppedTurnPreservesQueuedInputsWithoutAutomaticReplay(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	model := &scriptedModel{root: func(_ int, r wireRequest) string { <-release; return sseText("done") }, child: func(wireRequest) string { return sseText("child") }}
	server, root := collabServer(t, model)
	server.out = &receiptWriter{}
	submitQueued(t, server, root.ID, "one", "original task")
	eventually(t, "first model request", func() bool { model.mu.Lock(); defer model.mu.Unlock(); return len(model.requests) == 1 })
	submitQueued(t, server, root.ID, "two", "wait for consent")
	root.Interrupt()
	eventually(t, "stopped turn", func() bool { return !root.Busy() })
	record, _ := server.db.Submission(context.Background(), root.ID, "two")
	if record.State != "queued" {
		t.Fatalf("queue was lost or executed: %+v", record)
	}
	time.Sleep(60 * time.Millisecond)
	model.mu.Lock()
	calls := len(model.requests)
	model.mu.Unlock()
	if calls != 1 {
		t.Fatalf("stop started queued work: %d", calls)
	}
	// Re-sending the same request identity cannot replay it after a stop.
	submitQueued(t, server, root.ID, "two", "wait for consent")
	record, _ = server.db.Submission(context.Background(), root.ID, "two")
	if record.State != "queued" {
		t.Fatalf("retry replayed queued work: %+v", record)
	}
	once.Do(func() { close(release) })
	submitQueued(t, server, root.ID, "fresh", "new explicit task")
	eventually(t, "new explicit task completes", func() bool {
		record, _ := server.db.Submission(context.Background(), root.ID, "fresh")
		return record.State == "completed" && !root.Busy()
	})
	record, _ = server.db.Submission(context.Background(), root.ID, "two")
	if record.State != "queued" {
		t.Fatal("new work resumed a previously paused input")
	}
	result := workCall(t, server, "session/work", map[string]string{"sessionId": root.ID})
	var work struct {
		QueuedRequestIDs []string `json:"queuedRequestIds"`
	}
	if json.Unmarshal(result.Result, &work) != nil || len(work.QueuedRequestIDs) != 0 {
		t.Fatal("paused input was incorrectly marked for automatic execution")
	}
}
