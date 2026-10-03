package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/store"
)

func persistTestSession(t *testing.T, s *Session) *store.Store {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s.SetPersistence(db)
	if err = s.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	return db
}
func TestGoalRequiresEvidenceAndPreservesBudgetOnResume(t *testing.T) {
	s := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	db := persistTestSession(t, s)
	g, err := s.SetGoal(Goal{Objective: "work", Acceptance: "tests pass", TokenBudget: 8, Steps: []PlanStep{{Text: "test", Status: "pending"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateGoal(GoalUpdate{Status: "complete"}, false); err == nil {
		t.Fatal("completed without evidence")
	}
	s.startGoalClock(func() {})
	defer s.stopGoalClock()
	if err = s.accountGoal(8); err == nil {
		t.Fatal("budget not enforced")
	}
	if _, err = s.UpdateGoal(GoalUpdate{Status: "active"}, true); err == nil {
		t.Fatal("resumed exhausted budget")
	}
	extra := int64(16)
	if _, err = s.UpdateGoal(GoalUpdate{TokenBudget: &extra}, false); err == nil {
		t.Fatal("model increased budget")
	}
	if _, err = s.UpdateGoal(GoalUpdate{TokenBudget: &extra, Status: "active"}, true); err != nil {
		t.Fatal(err)
	}
	done := []PlanStep{{Text: "test", Status: "completed"}}
	evidence := "go test passed"
	if g, err = s.UpdateGoal(GoalUpdate{Steps: &done, Evidence: &evidence, Status: "complete"}, false); err != nil || g.TokensUsed != 8 {
		t.Fatalf("%+v %v", g, err)
	}
	var saved Goal
	if err = db.State(context.Background(), s.ID, "goal", &saved); err != nil || saved.Evidence != evidence {
		t.Fatalf("goal not persisted: %+v %v", saved, err)
	}
}
func TestGoalContinuesBeyondOrdinaryIterationLimit(t *testing.T) {
	script := []string{}
	for i := 0; i < maxIterations+1; i++ {
		script = append(script, sseToolCall(fmt.Sprint(i), "get_goal", `{}`))
	}
	script = append(script, sseToolCall("complete", "update_goal", `{"status":"complete","evidence":"checked all steps"}`), sseText("finished"))
	f := &fakeModel{script: script}
	s := newTestSession(t, f, protocol.ApprovalBypass)
	persistTestSession(t, s)
	if _, err := s.SetGoal(Goal{Objective: "complete", Acceptance: "checked"}); err != nil {
		t.Fatal(err)
	}
	emitter := &recordingEmitter{}
	s.RunTurn(context.Background(), "turn", "start", nil, nil, emitter)
	if !emitter.find("session/workUpdated", `"complete"`) {
		t.Fatal("goal completion notification missing")
	}
	g, err := s.Goal()
	if err != nil || g.Status != "complete" || s.LastAnswer() != "finished" {
		t.Fatalf("%+v %s %v", g, s.LastAnswer(), err)
	}
	if g.TokensUsed != 8 {
		t.Fatalf("tokens=%d", g.TokensUsed)
	}
}
func TestGoalStallsStopAndRestartPauses(t *testing.T) {
	s := newTestSession(t, &fakeModel{script: []string{sseText("done"), sseText("done"), sseText("done")}}, protocol.ApprovalBypass)
	db := persistTestSession(t, s)
	if _, err := s.SetGoal(Goal{Objective: "work", Acceptance: "verify"}); err != nil {
		t.Fatal(err)
	}
	s.RunTurn(context.Background(), "turn", "start", nil, nil, &recordingEmitter{})
	g, _ := s.Goal()
	if g.Status != "blocked" {
		t.Fatalf("status %s", g.Status)
	}
	if _, err := s.UpdateGoal(GoalUpdate{Status: "active"}, true); err != nil {
		t.Fatal(err)
	}
	if err := db.RecoverSubmissions(context.Background()); err != nil {
		t.Fatal(err)
	}
	g, _ = s.Goal()
	if g.Status != "paused" {
		t.Fatal("restart auto-resumed goal")
	}
}
func TestGoalTimeBudgetCancelsBlockedModel(t *testing.T) {
	entered := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		close(entered)
		<-r.Context().Done()
	}))
	defer upstream.Close()
	s, err := New(context.Background(), "clock", protocol.SessionStartParams{Model: protocol.ModelConfig{BaseURL: upstream.URL, Model: "test"}, Workdir: t.TempDir()}, StaticKey("test"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	persistTestSession(t, s)
	if _, err = s.SetGoal(Goal{Objective: "wait", Acceptance: "response", TimeBudgetMS: 500}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { s.RunTurn(ctx, "turn", "start", nil, nil, &recordingEmitter{}); close(done) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("model did not start")
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("time budget did not stop model")
	}
	g, _ := s.Goal()
	if g.Status != "budget_exhausted" {
		t.Fatalf("%+v", g)
	}
}
func TestForkUsesFullTranscriptAtExactMessageAndIndependentGoal(t *testing.T) {
	s := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	db := persistTestSession(t, s)
	s.appendMessage(llm.Message{ID: "u1", Role: llm.RoleUser, Content: "first"})
	s.appendMessage(llm.Message{ID: "a1", Role: llm.RoleAssistant, Content: "answer"})
	s.appendMessage(llm.Message{ID: "u2", Role: llm.RoleUser, Content: "later"})
	// Simulate compaction dropping visible old messages from model context.
	s.mu.Lock()
	s.messages = s.messages[:1]
	s.mu.Unlock()
	child, err := s.ForkAt(context.Background(), "fork", "a1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	child.SetPersistence(db)
	if err = child.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	history := child.History()
	raw, _ := json.Marshal(history)
	if !strings.Contains(string(raw), "first") || !strings.Contains(string(raw), "answer") || strings.Contains(string(raw), "later") {
		t.Fatalf("%s", raw)
	}
	if source, item := child.ForkOrigin(); source != s.ID || item != "a1" {
		t.Fatalf("source %s/%s", source, item)
	}
	if g, _ := child.Goal(); g != nil {
		t.Fatal("fork inherited active goal")
	}
	if child.Workspace() != s.Workspace() {
		t.Fatal("fork changed workspace")
	}
	if _, err = s.ForkAt(context.Background(), "bad", "missing", nil); err == nil {
		t.Fatal("unknown boundary accepted")
	}
}
func TestRecoverPreservesToolResultAndRepairsIncompleteCalls(t *testing.T) {
	s := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	db := persistTestSession(t, s)
	s.appendMessage(llm.Message{ID: "u", Role: llm.RoleUser, Content: "work"})
	s.appendMessage(llm.Message{ID: "a", Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "done", Name: "write_file", Arguments: "{}"}, {ID: "unknown", Name: "run_command", Arguments: "{}"}}})
	if err := s.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	// A result was journaled just before process death, without a session checkpoint.
	if err := db.RecordMessage(context.Background(), s.ID, "result", llm.Message{ID: "result", Role: llm.RoleTool, ToolCallID: "done", Content: "write finished"}); err != nil {
		t.Fatal(err)
	}
	if err := db.AcceptSubmission(context.Background(), s.ID, store.Submission{RequestID: "r", TurnID: "t", Payload: json.RawMessage(`{"text":"work"}`)}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSubmission(context.Background(), s.ID, "r", "t", "uncertain"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecoverInterrupted(); err != nil {
		t.Fatal(err)
	}
	if !historyComplete(s.snapshotMessages()) {
		t.Fatal("unanswered tool call after recovery")
	}
	var found bool
	for _, m := range s.snapshotMessages() {
		if m.ToolCallID == "done" {
			found = m.Content == "write finished"
		}
	}
	if !found {
		t.Fatal("completed side effect result lost")
	}
}

func TestFileToolsRecordChangesAndUndoThroughSession(t *testing.T) {
	f := &fakeModel{script: []string{sseToolCall("write", "write_file", `{"path":"sample.txt","content":"before"}`), sseToolCall("edit", "edit_file", `{"path":"sample.txt","old_text":"before","new_text":"after"}`), sseText("done")}}
	s := newTestSession(t, f, protocol.ApprovalBypass)
	db := persistTestSession(t, s)
	s.RunTurn(context.Background(), "t", "edit a file", nil, nil, &recordingEmitter{})
	changes, err := db.Changes(context.Background(), s.ID)
	if err != nil || len(changes) != 2 {
		t.Fatalf("%+v %v", changes, err)
	}
	if changes[0].Before != "before" || changes[0].After != "after" {
		t.Fatalf("%+v", changes[0])
	}
	if err = s.UndoChange(context.Background(), changes[0].ID); err != nil {
		t.Fatal(err)
	}
	changes, _ = db.Changes(context.Background(), s.ID)
	if changes[0].State != "undone" || changes[1].Conflict {
		t.Fatalf("%+v", changes)
	}
	if err = s.UndoChange(context.Background(), changes[1].ID); err != nil {
		t.Fatal(err)
	}
}
func TestMissingTokenUsagePausesBudgetedGoal(t *testing.T) {
	f := &fakeModel{script: []string{sseToolCall("read", "get_goal", `{}`)}}
	s := newTestSession(t, f, protocol.ApprovalBypass)
	persistTestSession(t, s)
	if _, err := s.SetGoal(Goal{Objective: "test", Acceptance: "verify", TokenBudget: 100}); err != nil {
		t.Fatal(err)
	}
	s.RunTurn(context.Background(), "t", "start", nil, nil, &recordingEmitter{})
	g, _ := s.Goal()
	if g.Status != "paused" || !g.UsageIncomplete {
		t.Fatalf("%+v", g)
	}
	if _, err := s.UpdateGoal(GoalUpdate{Status: "active"}, true); err == nil {
		t.Fatal("resumed an unenforceable budget")
	}
}

func TestDeletedSessionCannotBeResurrectedByCheckpoint(t *testing.T) {
	s := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	db := persistTestSession(t, s)
	s.MarkDeleted()
	if err := db.Delete(context.Background(), s.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Checkpoint(); err == nil {
		t.Fatal("saved deleted session")
	}
	if _, err := db.Load(context.Background(), s.ID); err == nil {
		t.Fatal("deleted session resurrected")
	}
}

func TestRejectedGoalUpdatesLeavePersistedGoalIntact(t *testing.T) {
	s := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	persistTestSession(t, s)
	if _, err := s.SetGoal(Goal{Objective: "work", Acceptance: "verified", Steps: []PlanStep{{Text: "verify", Status: "pending"}}, TokenBudget: 100}); err != nil {
		t.Fatal(err)
	}
	original, err := s.Goal()
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(original)
	negative := int64(-1)
	evidence := "checks pass"
	invalid := []PlanStep{{Text: "verify", Status: "invented"}}
	parallel := []PlanStep{{Text: "one", Status: "in_progress"}, {Text: "two", Status: "in_progress"}}
	cases := []struct {
		name   string
		update GoalUpdate
		user   bool
	}{
		{"negative budget", GoalUpdate{TokenBudget: &negative}, true},
		{"invalid goal status", GoalUpdate{Status: "invented"}, true},
		{"invalid step status", GoalUpdate{Steps: &invalid}, true},
		{"multiple active steps", GoalUpdate{Steps: &parallel}, true},
		{"unfinished steps with evidence", GoalUpdate{Status: "complete", Evidence: &evidence}, true},
		{"model resuming goal", GoalUpdate{Status: "active"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.UpdateGoal(tc.update, tc.user); err == nil {
				t.Fatal("invalid update accepted")
			}
			saved, err := s.Goal()
			if err != nil {
				t.Fatal(err)
			}
			after, _ := json.Marshal(saved)
			if string(after) != string(before) {
				t.Fatalf("rejected update changed goal: %s", after)
			}
		})
	}
	if _, err := s.SetGoal(Goal{Objective: "replacement", Acceptance: "different"}); err == nil {
		t.Fatal("replaced unfinished goal")
	}
}

func TestCompletedGoalCannotLoseItsAcceptanceEvidence(t *testing.T) {
	s := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	persistTestSession(t, s)
	if _, err := s.SetGoal(Goal{Objective: "work", Acceptance: "verified"}); err != nil {
		t.Fatal(err)
	}
	evidence := "go test passed"
	if _, err := s.UpdateGoal(GoalUpdate{Status: "complete", Evidence: &evidence}, true); err != nil {
		t.Fatal(err)
	}
	empty := "  "
	pending := []PlanStep{{Text: "unfinished", Status: "pending"}}
	for _, update := range []GoalUpdate{{Evidence: &empty}, {Steps: &pending}} {
		if _, err := s.UpdateGoal(update, true); err == nil {
			t.Fatal("invalidated completed goal")
		}
	}
	g, err := s.Goal()
	if err != nil || g.Status != "complete" || g.Evidence != evidence || len(g.Steps) != 0 {
		t.Fatalf("completed goal corrupted: %+v %v", g, err)
	}
}

func TestForkToolBoundaryAlwaysCopiesCompleteToolExchange(t *testing.T) {
	for _, complete := range []bool{true, false} {
		t.Run(fmt.Sprintf("complete=%v", complete), func(t *testing.T) {
			s := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
			persistTestSession(t, s)
			s.appendMessage(llm.Message{ID: "user", Role: llm.RoleUser, Content: "read both"})
			s.appendMessage(llm.Message{ID: "calls", Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "one", Name: "read_file", Arguments: "{}"}, {ID: "two", Name: "read_file", Arguments: "{}"}}})
			s.appendMessage(llm.Message{ID: "result1", Role: llm.RoleTool, ToolCallID: "one", Content: "first result"})
			if complete {
				s.appendMessage(llm.Message{ID: "result2", Role: llm.RoleTool, ToolCallID: "two", Content: "second result"})
			}
			child, err := s.ForkAt(context.Background(), "fork", "calls", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer child.Close()
			messages := child.snapshotMessages()
			if !historyComplete(messages) {
				t.Fatal("fork would send orphaned tool calls to model")
			}
			results, calls := 0, 0
			for _, m := range messages {
				if m.Role == llm.RoleTool {
					results++
				}
				calls += len(m.ToolCalls)
			}
			if complete && (results != 2 || calls != 2) {
				t.Fatalf("lost completed exchange: calls=%d results=%d", calls, results)
			}
			if !complete && (results != 0 || calls != 0) {
				t.Fatalf("copied uncertain exchange: calls=%d results=%d", calls, results)
			}
		})
	}
}

func TestForkAndUndoRejectBusySession(t *testing.T) {
	s := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	db := persistTestSession(t, s)
	path := filepath.Join(s.Workspace(), "file")
	if err := db.WriteTracked(context.Background(), s.ID, "t", path, []byte("after"), nil); err != nil {
		t.Fatal(err)
	}
	changes, err := db.Changes(context.Background(), s.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if child, err := s.ForkAt(context.Background(), "fork", "", nil); err == nil {
		child.Close()
		t.Fatal("forked running session")
	}
	if err := s.UndoChange(context.Background(), changes[0].ID); err == nil {
		t.Fatal("undid a write while turn was running")
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != "after" {
		t.Fatal("busy undo changed file")
	}
}
