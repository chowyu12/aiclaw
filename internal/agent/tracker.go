package agent

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/store"
)

type StepTracker struct {
	store          store.ConversationStore
	conversationID int64
	messageID      int64

	mu        sync.Mutex
	stepOrder int
	steps     []model.ExecutionStep
	onStep    func(step model.ExecutionStep)
}

func NewStepTracker(s store.ConversationStore, conversationID int64) *StepTracker {
	return &StepTracker{
		store:          s,
		conversationID: conversationID,
	}
}

func (t *StepTracker) SetMessageID(id int64) {
	t.mu.Lock()
	t.messageID = id
	for i := range t.steps {
		t.steps[i].MessageID = id
	}
	t.mu.Unlock()

	t.store.UpdateStepsMessageID(context.Background(), t.conversationID, id)
}

func (t *StepTracker) RecordStep(ctx context.Context, stepType model.StepType, name, input, output string, status model.StepStatus, stepErr string, duration time.Duration, tokensUsed int, meta *model.StepMetadata) *model.ExecutionStep {
	t.mu.Lock()
	t.stepOrder++
	order := t.stepOrder
	t.mu.Unlock()

	var metaJSON model.JSON
	if meta != nil {
		data, _ := json.Marshal(meta)
		metaJSON = model.JSON(data)
	}

	step := &model.ExecutionStep{
		MessageID:      t.messageID,
		ConversationID: t.conversationID,
		StepOrder:      order,
		StepType:       stepType,
		Name:           name,
		Input:          truncate(input, 65000),
		Output:         truncate(output, 65000),
		Status:         status,
		Error:          stepErr,
		DurationMs:     int(duration.Milliseconds()),
		TokensUsed:     tokensUsed,
		Metadata:       metaJSON,
	}

	t.store.CreateExecutionStep(ctx, step)

	t.mu.Lock()
	t.steps = append(t.steps, *step)
	fn := t.onStep
	t.mu.Unlock()

	if fn != nil {
		fn(*step)
	}

	return step
}

func (t *StepTracker) SetOnStep(fn func(step model.ExecutionStep)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onStep = fn
}

func (t *StepTracker) Steps() []model.ExecutionStep {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make([]model.ExecutionStep, len(t.steps))
	copy(result, t.steps)
	return result
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...[truncated]"
}
