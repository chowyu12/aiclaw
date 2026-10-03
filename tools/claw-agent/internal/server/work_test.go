package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/agent"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/store"
)

func workCall(t *testing.T, s *Server, method string, params any) frame {
	t.Helper()
	w, ok := s.out.(*receiptWriter)
	if !ok {
		w = &receiptWriter{}
		s.out = w
	}
	w.mu.Lock()
	offset := len(w.frames)
	w.mu.Unlock()
	raw, _ := json.Marshal(params)
	s.handleWork(context.Background(), frame{Method: method, ID: json.RawMessage(`99`), Params: raw})
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, f := range w.frames[offset:] {
		if string(f.ID) == "99" {
			return f
		}
	}
	t.Fatal("missing response")
	return frame{}
}
func TestRequestReceiptSurvivesKernelRestart(t *testing.T) {
	model := &scriptedModel{root: func(int, wireRequest) string { return sseText("answer") }, child: func(wireRequest) string { return sseText("child") }}
	original, root := collabServer(t, model)
	w := &receiptWriter{}
	original.out = w
	input := protocol.TurnStartParams{SessionID: root.ID, RequestID: "durable", Text: "only once"}
	raw, _ := json.Marshal(input)
	original.handleTurnStart(context.Background(), frame{ID: json.RawMessage(`1`), Params: raw})
	eventually(t, "first completion", func() bool { return strings.Contains(root.LastAnswer(), "answer") && !root.Busy() })
	receipt, err := original.db.Submission(context.Background(), root.ID, input.RequestID)
	if err != nil || receipt.State != "completed" {
		t.Fatalf("%+v %v", receipt, err)
	}
	original.closeAll()
	original.db.Close()
	next, err := New(original.options, &receiptWriter{})
	if err != nil {
		t.Fatal(err)
	}
	defer next.db.Close()
	defer next.closeAll()
	resumed, err := agent.Load(context.Background(), next.db, root.ID, next.keyFor, nil, next.sessionOptions(root.ID)...)
	if err != nil {
		t.Fatal(err)
	}
	next.guard(resumed)
	next.sessions[root.ID] = resumed
	writer := next.out.(*receiptWriter)
	next.handleTurnStart(context.Background(), frame{ID: json.RawMessage(`2`), Params: raw})
	writer.mu.Lock()
	var response protocol.TurnStartResult
	for _, f := range writer.frames {
		if string(f.ID) == "2" {
			if f.Error != nil {
				t.Fatal(f.Error)
			}
			json.Unmarshal(f.Result, &response)
		}
	}
	writer.mu.Unlock()
	if response.TurnID != receipt.TurnID {
		t.Fatal("restart changed original receipt")
	}
	model.mu.Lock()
	calls := len(model.requests)
	model.mu.Unlock()
	if calls != 1 {
		t.Fatalf("replayed: %d calls", calls)
	}
}
func TestRecoveryRejectsUncertainReplayAndConsumesQueuedExactlyOnce(t *testing.T) {
	model := &scriptedModel{root: func(int, wireRequest) string { return sseText("answer") }, child: func(wireRequest) string { return sseText("child") }}
	s, root := collabServer(t, model)
	ctx := context.Background()
	for _, id := range []string{"queued", "uncertain"} {
		raw, _ := json.Marshal(protocol.TurnStartParams{SessionID: root.ID, RequestID: id, Text: id})
		if err := s.db.AcceptSubmission(ctx, root.ID, store.Submission{RequestID: id, TurnID: "original", Payload: raw}); err != nil {
			t.Fatal(err)
		}
	}
	s.db.SetSubmission(ctx, root.ID, "uncertain", "original", "uncertain")
	result := workCall(t, s, "session/recover", map[string]any{"sessionId": root.ID, "requestId": "uncertain", "action": "resume"})
	if result.Error == nil {
		t.Fatal("uncertain side effects were replayed")
	}
	result = workCall(t, s, "session/recover", map[string]any{"sessionId": root.ID, "requestId": "queued", "action": "resume"})
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	eventually(t, "queued completion", func() bool { return strings.Contains(root.LastAnswer(), "answer") && !root.Busy() })
	result = workCall(t, s, "session/recover", map[string]any{"sessionId": root.ID, "requestId": "queued", "action": "resume"})
	if result.Error == nil {
		t.Fatal("replayed consumed request")
	}
	model.mu.Lock()
	calls := len(model.requests)
	model.mu.Unlock()
	if calls != 1 {
		t.Fatalf("%d calls", calls)
	}
}
func TestGoalAndForkAndChangesRPCRoundTrip(t *testing.T) {
	model := &scriptedModel{root: func(int, wireRequest) string { return sseText("answer") }, child: func(wireRequest) string { return sseText("child") }}
	s, root := collabServer(t, model)
	result := workCall(t, s, "goal/set", map[string]any{"sessionId": root.ID, "goal": agent.Goal{Objective: "test goal", Acceptance: "tests pass"}})
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	result = workCall(t, s, "goal/update", map[string]any{"sessionId": root.ID, "update": agent.GoalUpdate{Status: "paused"}})
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	result = workCall(t, s, "session/work", map[string]string{"sessionId": root.ID})
	if result.Error != nil || !strings.Contains(string(result.Result), `"paused"`) {
		t.Fatalf("%+v", result)
	}
	result = workCall(t, s, "session/fork", map[string]string{"sessionId": root.ID})
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	var fork struct {
		SessionID string `json:"sessionId"`
	}
	json.Unmarshal(result.Result, &fork)
	result = workCall(t, s, "session/work", map[string]string{"sessionId": fork.SessionID})
	if result.Error != nil || !strings.Contains(string(result.Result), fmt.Sprintf(`"forkSourceId":%q`, root.ID)) {
		t.Fatalf("%+v", result)
	}
	result = workCall(t, s, "changes/list", map[string]string{"sessionId": root.ID})
	if result.Error != nil || string(result.Result) != "[]" {
		t.Fatalf("%+v", result)
	}
}

func TestConcurrentRecoveryClaimsQueuedInputExactlyOnce(t *testing.T) {
	model := &scriptedModel{root: func(int, wireRequest) string { return sseText("recovered") }, child: func(wireRequest) string { return sseText("child") }}
	s, root := collabServer(t, model)
	writer := &receiptWriter{}
	s.out = writer
	ctx := context.Background()
	payload, err := json.Marshal(protocol.TurnStartParams{SessionID: root.ID, RequestID: "queued", Text: "recover once"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.AcceptSubmission(ctx, root.ID, store.Submission{RequestID: "queued", TurnID: "old", Payload: payload}); err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(map[string]string{"sessionId": root.ID, "requestId": "queued", "action": "resume"})
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 1; i <= 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			s.handleWork(ctx, frame{Method: "session/recover", ID: json.RawMessage(fmt.Sprint(i)), Params: params})
		}(i)
	}
	close(start)
	wg.Wait()
	writer.mu.Lock()
	successes, failures := 0, 0
	for _, f := range writer.frames {
		if len(f.ID) == 0 {
			continue
		}
		if f.Error == nil {
			successes++
		} else {
			failures++
		}
	}
	writer.mu.Unlock()
	if successes != 1 || failures != 11 {
		t.Fatalf("recovery receipts: success=%d failure=%d", successes, failures)
	}
	eventually(t, "recovered request completed", func() bool {
		receipt, err := s.db.Submission(ctx, root.ID, "queued")
		return err == nil && receipt.State == "completed" && !root.Busy()
	})
	model.mu.Lock()
	calls := len(model.requests)
	model.mu.Unlock()
	if calls != 1 {
		t.Fatalf("recovery repeated side effects: %d model calls", calls)
	}
}

func TestGoalResumeRequiresAllRecoveryNoticesToBeReviewed(t *testing.T) {
	model := &scriptedModel{root: func(int, wireRequest) string { return sseText("unexpected") }, child: func(wireRequest) string { return sseText("child") }}
	s, root := collabServer(t, model)
	ctx := context.Background()
	if _, err := root.SetGoal(agent.Goal{Objective: "continue work", Acceptance: "verified"}); err != nil {
		t.Fatal(err)
	}
	if _, err := root.UpdateGoal(agent.GoalUpdate{Status: "paused"}, true); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"first", "second"} {
		if err := s.db.AcceptSubmission(ctx, root.ID, store.Submission{RequestID: id, TurnID: "old", Payload: json.RawMessage(`{"text":"old input"}`)}); err != nil {
			t.Fatal(err)
		}
		if err := s.db.SetSubmission(ctx, root.ID, id, "old", "uncertain"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.db.PutState(ctx, root.ID, "execution", store.Execution{TurnID: "old", State: "uncertain"}); err != nil {
		t.Fatal(err)
	}
	resume := func() frame {
		return workCall(t, s, "goal/update", map[string]any{"sessionId": root.ID, "update": agent.GoalUpdate{Status: "active"}})
	}
	if resume().Error == nil {
		t.Fatal("resumed with uncertain side effects")
	}
	for i, id := range []string{"first", "second"} {
		result := workCall(t, s, "session/recover", map[string]string{"sessionId": root.ID, "requestId": id, "action": "dismiss"})
		if result.Error != nil {
			t.Fatal(result.Error)
		}
		if i == 0 && resume().Error == nil {
			t.Fatal("resumed before reviewing second notice")
		}
	}
	if result := resume(); result.Error != nil {
		t.Fatal(result.Error)
	}
	var execution store.Execution
	if err := s.db.State(ctx, root.ID, "execution", &execution); err != nil || execution.State != "reviewed" {
		t.Fatalf("execution=%+v err=%v", execution, err)
	}
	model.mu.Lock()
	calls := len(model.requests)
	model.mu.Unlock()
	if calls != 0 {
		t.Fatal("reviewing notices replayed the model")
	}
}

func TestRecoveryOfExecutionWithoutRequestDoesNotReplay(t *testing.T) {
	model := &scriptedModel{root: func(int, wireRequest) string { return sseText("unexpected") }, child: func(wireRequest) string { return sseText("child") }}
	s, root := collabServer(t, model)
	ctx := context.Background()
	if err := s.db.PutState(ctx, root.ID, "execution", store.Execution{TurnID: "old", State: "uncertain"}); err != nil {
		t.Fatal(err)
	}
	result := workCall(t, s, "session/work", map[string]string{"sessionId": root.ID})
	var work struct {
		Pending []store.Submission `json:"pending"`
	}
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	if err := json.Unmarshal(result.Result, &work); err != nil {
		t.Fatal(err)
	}
	if len(work.Pending) != 1 || work.Pending[0].RequestID != "" || work.Pending[0].State != "uncertain" {
		t.Fatalf("missing synthetic notice: %+v", work)
	}
	params := map[string]string{"sessionId": root.ID, "action": "dismiss"}
	if result := workCall(t, s, "session/recover", params); result.Error != nil {
		t.Fatal(result.Error)
	}
	if result := workCall(t, s, "session/recover", params); result.Error == nil {
		t.Fatal("reviewed same execution twice")
	}
	result = workCall(t, s, "session/work", map[string]string{"sessionId": root.ID})
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	if err := json.Unmarshal(result.Result, &work); err != nil || len(work.Pending) != 0 {
		t.Fatalf("notice not cleared: %+v %v", work, err)
	}
	model.mu.Lock()
	calls := len(model.requests)
	model.mu.Unlock()
	if calls != 0 {
		t.Fatal("dismiss replayed uncertain execution")
	}
}

func TestPausingGoalCancelsInFlightModelAndPreservesPausedState(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	model := &scriptedModel{root: func(int, wireRequest) string {
		once.Do(func() { close(entered) })
		<-release
		return sseText("late response")
	}, child: func(wireRequest) string { return sseText("child") }}
	s, root := collabServer(t, model)
	defer close(release)
	s.out = &receiptWriter{}
	if _, err := root.SetGoal(agent.Goal{Objective: "work", Acceptance: "verified"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { root.RunTurn(ctx, "turn", "start", nil, nil, &emitter{server: s}); close(done) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("model did not start")
	}
	result := workCall(t, s, "goal/update", map[string]any{"sessionId": root.ID, "update": agent.GoalUpdate{Status: "paused"}})
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("pause did not cancel model wait")
	}
	goal, err := root.Goal()
	if err != nil || goal.Status != "paused" {
		t.Fatalf("pause lost during turn cleanup: %+v %v", goal, err)
	}
	if strings.Contains(root.LastAnswer(), "late response") {
		t.Fatal("accepted canceled response")
	}
	var execution store.Execution
	if err := s.db.State(context.Background(), root.ID, "execution", &execution); err != nil || execution.State != "uncertain" {
		t.Fatalf("interrupted execution was not recorded: %+v %v", execution, err)
	}
}

func TestScheduledFollowupRejectsUnsafeSessionBeforeAcceptingInput(t *testing.T) {
	for _, state := range []string{"running", "queued", "uncertain", "paused-goal"} {
		t.Run(state, func(t *testing.T) {
			model := &scriptedModel{root: func(int, wireRequest) string { return sseText("unexpected") }, child: func(wireRequest) string { return sseText("child") }}
			s, root := collabServer(t, model)
			ctx := context.Background()
			if state == "paused-goal" {
				if _, err := root.SetGoal(agent.Goal{Objective: "work", Acceptance: "verified"}); err != nil {
					t.Fatal(err)
				}
				if _, err := root.UpdateGoal(agent.GoalUpdate{Status: "paused"}, true); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := s.db.AcceptSubmission(ctx, root.ID, store.Submission{RequestID: "previous", TurnID: "old", Payload: json.RawMessage(`{"text":"old"}`)}); err != nil {
					t.Fatal(err)
				}
				if err := s.db.SetSubmission(ctx, root.ID, "previous", "old", state); err != nil {
					t.Fatal(err)
				}
			}
			writer := &receiptWriter{}
			s.out = writer
			input, _ := json.Marshal(protocol.TurnStartParams{SessionID: root.ID, RequestID: "followup", Text: "scheduled check", IdleOnly: true})
			s.handleTurnStart(ctx, frame{ID: json.RawMessage(`1`), Params: input})
			writer.mu.Lock()
			rejected := len(writer.frames) == 1 && writer.frames[0].Error != nil
			writer.mu.Unlock()
			if !rejected {
				t.Fatal("started an unsafe followup")
			}
			if _, err := s.db.Submission(ctx, root.ID, "followup"); err == nil {
				t.Fatal("rejected followup left a replayable receipt")
			}
			model.mu.Lock()
			calls := len(model.requests)
			model.mu.Unlock()
			if calls != 0 {
				t.Fatal("scheduled check called model")
			}
		})
	}
}

func TestOAuthCredentialAndKeyFilesCannotBeReadByAgent(t *testing.T) {
	var paths []string
	model := &scriptedModel{root: func(step int, _ wireRequest) string {
		if step < len(paths) {
			args, _ := json.Marshal(map[string]string{"path": paths[step]})
			return sseToolCall(fmt.Sprint(step), "read_file", string(args))
		}
		return sseText("done")
	}, child: func(wireRequest) string { return sseText("child") }}
	s, root := collabServer(t, model)
	paths = []string{s.oauth.CredentialPath(), s.oauth.KeyPath()}
	if err := os.WriteFile(paths[0], []byte("private-oauth-credential"), 0600); err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(paths[1])
	if err != nil {
		t.Fatal(err)
	}
	root.RunTurn(context.Background(), "guard", "read protected files", nil, nil, &emitter{server: s})
	results := 0
	for _, message := range model.lastRootRequest().Messages {
		if message.Role == "tool" {
			results++
			text, _ := message.Content.(string)
			if strings.Contains(text, "private-oauth-credential") || strings.Contains(text, strings.TrimSpace(string(key))) {
				t.Fatal("agent read OAuth credential/key")
			}
			if !strings.Contains(text, "Error") && !strings.Contains(text, "错误") && !strings.Contains(text, "error") {
				t.Fatalf("read was not denied: %s", text)
			}
		}
	}
	if results != 2 {
		t.Fatalf("checked %d protected files", results)
	}
}
