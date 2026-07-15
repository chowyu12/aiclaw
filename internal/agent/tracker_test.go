package agent

import (
	"sync"
	"testing"
	"time"

	"github.com/chowyu12/aiclaw/internal/model"
)

func TestStepTracker_RecordStep_Basic(t *testing.T) {
	s := newMockStore()
	tracker := NewStepTracker(s, 42)

	step := tracker.RecordStep(t.Context(), model.StepToolCall, "test_tool", "input", "output", model.StepSuccess, "", time.Second, 100, nil)

	if step.ConversationID != 42 {
		t.Errorf("expected conversationID 42, got %d", step.ConversationID)
	}
	if step.StepOrder != 1 {
		t.Errorf("expected stepOrder 1, got %d", step.StepOrder)
	}
	if step.Name != "test_tool" {
		t.Errorf("expected name 'test_tool', got %q", step.Name)
	}
	if step.TokensUsed != 100 {
		t.Errorf("expected 100 tokens, got %d", step.TokensUsed)
	}
}

func TestStepTracker_StepOrderIncreases(t *testing.T) {
	s := newMockStore()
	tracker := NewStepTracker(s, 1)

	s1 := tracker.RecordStep(t.Context(), model.StepToolCall, "a", "", "", model.StepSuccess, "", 0, 0, nil)
	s2 := tracker.RecordStep(t.Context(), model.StepToolCall, "b", "", "", model.StepSuccess, "", 0, 0, nil)
	s3 := tracker.RecordStep(t.Context(), model.StepToolCall, "c", "", "", model.StepSuccess, "", 0, 0, nil)

	if s1.StepOrder != 1 || s2.StepOrder != 2 || s3.StepOrder != 3 {
		t.Errorf("step orders should be 1,2,3 got %d,%d,%d", s1.StepOrder, s2.StepOrder, s3.StepOrder)
	}
}

func TestStepTracker_SetMessageID_UpdatesExistingSteps(t *testing.T) {
	s := newMockStore()
	tracker := NewStepTracker(s, 1)

	tracker.RecordStep(t.Context(), model.StepToolCall, "a", "", "", model.StepSuccess, "", 0, 0, nil)
	tracker.RecordStep(t.Context(), model.StepToolCall, "b", "", "", model.StepSuccess, "", 0, 0, nil)

	tracker.SetMessageID(999)

	steps := tracker.Steps()
	for _, step := range steps {
		if step.MessageID != 999 {
			t.Errorf("expected messageID 999 after SetMessageID, got %d", step.MessageID)
		}
	}
}

func TestStepTracker_SetMessageIDScopesStepsToRun(t *testing.T) {
	s := newMockStore()
	first := NewStepTracker(s, 1)
	second := NewStepTracker(s, 1)
	first.SetRunUUID("run-first")
	second.SetRunUUID("run-second")

	first.RecordStep(t.Context(), model.StepToolCall, "first", "", "", model.StepSuccess, "", 0, 0, nil)
	second.RecordStep(t.Context(), model.StepToolCall, "second", "", "", model.StepSuccess, "", 0, 0, nil)
	first.SetMessageID(101)

	steps, err := s.ListExecutionStepsByConversation(t.Context(), 1)
	if err != nil {
		t.Fatalf("ListExecutionStepsByConversation: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("steps = %#v", steps)
	}
	for _, step := range steps {
		switch step.RunUUID {
		case "run-first":
			if step.MessageID != 101 {
				t.Fatalf("first run message ID = %d, want 101", step.MessageID)
			}
		case "run-second":
			if step.MessageID != 0 {
				t.Fatalf("second run must remain unlinked, got message ID %d", step.MessageID)
			}
		}
	}
}

func TestStepTracker_ConcurrentRecordAndSetMessageID(t *testing.T) {
	s := newMockStore()
	tracker := NewStepTracker(s, 1)

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			tracker.RecordStep(t.Context(), model.StepToolCall, "tool", "", "", model.StepSuccess, "", 0, 0, nil)
		}()
		go func() {
			defer wg.Done()
			tracker.SetMessageID(42)
		}()
	}
	wg.Wait()

	steps := tracker.Steps()
	if len(steps) != 50 {
		t.Errorf("expected 50 steps, got %d", len(steps))
	}
}

func TestStepTracker_OnStepCallback(t *testing.T) {
	s := newMockStore()
	tracker := NewStepTracker(s, 1)

	var received []string
	tracker.SetOnStep(func(step model.ExecutionStep) {
		received = append(received, step.Name)
	})

	tracker.RecordStep(t.Context(), model.StepToolCall, "first", "", "", model.StepSuccess, "", 0, 0, nil)
	tracker.RecordStep(t.Context(), model.StepToolCall, "second", "", "", model.StepSuccess, "", 0, 0, nil)

	if len(received) != 2 || received[0] != "first" || received[1] != "second" {
		t.Errorf("expected [first, second], got %v", received)
	}
}

func TestStepTracker_Steps_ReturnsCopy(t *testing.T) {
	s := newMockStore()
	tracker := NewStepTracker(s, 1)

	tracker.RecordStep(t.Context(), model.StepToolCall, "a", "", "", model.StepSuccess, "", 0, 0, nil)

	steps1 := tracker.Steps()
	steps2 := tracker.Steps()
	steps1[0].Name = "modified"
	if steps2[0].Name == "modified" {
		t.Error("Steps() should return a copy, mutations should not affect the original")
	}
}

func TestStepTracker_Truncate(t *testing.T) {
	if truncate("short", 100) != "short" {
		t.Error("short string should not be truncated")
	}
	got := truncate("hello world", 5)
	if got != "hello...[truncated]" {
		t.Errorf("expected 'hello...[truncated]', got %q", got)
	}
}

func TestStepTracker_WithMetadata(t *testing.T) {
	s := newMockStore()
	tracker := NewStepTracker(s, 1)

	meta := &model.StepMetadata{SkillName: "test-skill"}
	step := tracker.RecordStep(t.Context(), model.StepToolCall, "tool", "", "", model.StepSuccess, "", 0, 0, meta)

	if len(step.Metadata) == 0 {
		t.Error("metadata should be serialized")
	}
}

func TestStepTracker_BeginAndFinalize(t *testing.T) {
	s := newMockStore()
	tracker := NewStepTracker(s, 1)

	var emitted []model.ExecutionStep
	tracker.SetOnStep(func(step model.ExecutionStep) {
		emitted = append(emitted, step)
	})

	ctx := t.Context()
	step := tracker.BeginStep(ctx, model.StepLLMCall, "deepseek-v4", "你好",
		&model.StepMetadata{Provider: "test", Model: "deepseek-v4"})

	if step.Status != model.StepRunning {
		t.Errorf("expected status running, got %s", step.Status)
	}
	if step.StepOrder != 1 {
		t.Errorf("expected order 1, got %d", step.StepOrder)
	}
	if len(emitted) != 1 || emitted[0].Status != model.StepRunning {
		t.Errorf("expected initial running emit, got %d events", len(emitted))
	}

	tracker.FinalizeStep(ctx, step, "你好！", model.StepSuccess, "", 250*time.Millisecond, 42, nil)

	if step.Status != model.StepSuccess {
		t.Errorf("expected status success after finalize, got %s", step.Status)
	}
	if step.DurationMs != 250 {
		t.Errorf("expected duration 250ms, got %d", step.DurationMs)
	}
	if step.TokensUsed != 42 {
		t.Errorf("expected tokens 42, got %d", step.TokensUsed)
	}
	if len(emitted) != 2 || emitted[1].Status != model.StepSuccess {
		t.Errorf("expected finalized emit, got %d events", len(emitted))
	}

	steps := tracker.Steps()
	if len(steps) != 1 {
		t.Fatalf("expected 1 step (replaced), got %d", len(steps))
	}
	if steps[0].Status != model.StepSuccess {
		t.Errorf("expected stored step finalized, got %s", steps[0].Status)
	}
}

func TestStepTracker_FinalizeStep_Error(t *testing.T) {
	s := newMockStore()
	tracker := NewStepTracker(s, 1)

	step := tracker.BeginStep(t.Context(), model.StepLLMCall, "model", "input", nil)
	tracker.FinalizeStep(t.Context(), step, "", model.StepError, "boom", time.Second, 0, nil)

	steps := tracker.Steps()
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if steps[0].Status != model.StepError {
		t.Errorf("expected error status, got %s", steps[0].Status)
	}
	if steps[0].Error != "boom" {
		t.Errorf("expected error message, got %q", steps[0].Error)
	}
}
