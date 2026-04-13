package agent

import "testing"

func TestBudgetTracker_UnlimitedBudget(t *testing.T) {
	b := NewBudgetTracker(0)

	b.Add(999999)
	if b.Exceeded() {
		t.Error("unlimited budget (limit=0) should never be exceeded")
	}
	if b.Remaining() != -1 {
		t.Errorf("unlimited budget remaining should be -1, got %d", b.Remaining())
	}
	if b.Consumed() != 999999 {
		t.Errorf("consumed should be 999999, got %d", b.Consumed())
	}
}

func TestBudgetTracker_NegativeLimit(t *testing.T) {
	b := NewBudgetTracker(-100)

	b.Add(50)
	if b.Exceeded() {
		t.Error("negative limit should behave as unlimited")
	}
	if b.Remaining() != -1 {
		t.Errorf("expected -1 for negative limit, got %d", b.Remaining())
	}
}

func TestBudgetTracker_ExactLimit(t *testing.T) {
	b := NewBudgetTracker(1000)

	b.Add(500)
	if b.Exceeded() {
		t.Error("500 < 1000, should not be exceeded")
	}
	if b.Remaining() != 500 {
		t.Errorf("expected 500 remaining, got %d", b.Remaining())
	}

	b.Add(500)
	if !b.Exceeded() {
		t.Error("1000 >= 1000, should be exceeded")
	}
	if b.Remaining() != 0 {
		t.Errorf("expected 0 remaining, got %d", b.Remaining())
	}
}

func TestBudgetTracker_OverLimit(t *testing.T) {
	b := NewBudgetTracker(100)

	b.Add(200)
	if !b.Exceeded() {
		t.Error("200 >= 100, should be exceeded")
	}
	if b.Remaining() != 0 {
		t.Errorf("remaining should be 0 (not negative), got %d", b.Remaining())
	}
}

func TestBudgetTracker_AccumulatesTokens(t *testing.T) {
	b := NewBudgetTracker(1000)

	for range 10 {
		b.Add(90)
	}
	if b.Consumed() != 900 {
		t.Errorf("expected 900, got %d", b.Consumed())
	}
	if b.Exceeded() {
		t.Error("900 < 1000, should not be exceeded")
	}

	b.Add(100)
	if !b.Exceeded() {
		t.Error("1000 >= 1000, should be exceeded")
	}
}
