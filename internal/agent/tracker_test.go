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
