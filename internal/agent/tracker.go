package agent

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/store"
)

type StepTracker struct {
	store          store.ConversationStore
	conversationID int64
	messageID      int64
	channelTrace   *model.ChannelExecTrace

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

// SetChannelTrace 在首轮 RecordStep 之前调用；之后所有步骤 metadata 会附带渠道追溯字段。
func (t *StepTracker) SetChannelTrace(tr *model.ChannelExecTrace) {
	t.mu.Lock()
	t.channelTrace = tr
	t.mu.Unlock()
}

func (t *StepTracker) enrichMeta(meta *model.StepMetadata) *model.StepMetadata {
	t.mu.Lock()
	tr := t.channelTrace
	t.mu.Unlock()
	if tr == nil {
		return meta
	}
	if meta == nil {
		meta = &model.StepMetadata{}
	}
	meta.ChannelID = tr.ID
	meta.ChannelUUID = tr.UUID
	meta.ChannelType = tr.Type
	meta.ChannelThreadKey = tr.ThreadKey
	meta.ChannelSenderID = tr.SenderID
	return meta
}

func (t *StepTracker) RecordStep(ctx context.Context, stepType model.StepType, name, input, output string, status model.StepStatus, stepErr string, duration time.Duration, tokensUsed int, meta *model.StepMetadata) *model.ExecutionStep {
	step := t.newStep(ctx, stepType, name, input)
	step.Output = truncate(output, 65000)
	step.Status = status
	step.Error = stepErr
	step.DurationMs = int(duration.Milliseconds())
	step.TokensUsed = tokensUsed
	step.Metadata = encodeMeta(t.enrichMeta(meta))

	if err := t.store.CreateExecutionStep(ctx, step); err != nil {
		log.WithError(err).WithField("step_type", string(step.StepType)).Warn("[Tracker] save execution step failed")
	}

	t.appendStep(*step)
	t.fireOnStep(*step)
	return step
}

// BeginStep 创建一条 status=running 的步骤并立即下发到流式订阅者，
// 用于在长耗时调用（如 LLM 流式生成）开始时给前端展示「运行中」标识。
// 返回的 *ExecutionStep 应稍后传给 FinalizeStep 完成最终状态写入。
func (t *StepTracker) BeginStep(ctx context.Context, stepType model.StepType, name, input string, meta *model.StepMetadata) *model.ExecutionStep {
	step := t.newStep(ctx, stepType, name, input)
	step.Status = model.StepRunning
	step.Metadata = encodeMeta(t.enrichMeta(meta))

	if err := t.store.CreateExecutionStep(ctx, step); err != nil {
		log.WithError(err).WithField("step_type", string(step.StepType)).Warn("[Tracker] begin execution step failed")
	}

	t.appendStep(*step)
	t.fireOnStep(*step)
	return step
}

// FinalizeStep 把 BeginStep 创建的步骤更新为最终状态（success/error），
// 并再次推送一份到流式订阅者，前端按 step_order 覆盖之前的「运行中」记录。
func (t *StepTracker) FinalizeStep(ctx context.Context, step *model.ExecutionStep, output string, status model.StepStatus, stepErr string, duration time.Duration, tokensUsed int, meta *model.StepMetadata) {
	if step == nil {
		return
	}

	step.Output = truncate(output, 65000)
	step.Status = status
	step.Error = stepErr
	step.DurationMs = int(duration.Milliseconds())
	step.TokensUsed = tokensUsed
	if meta != nil {
		step.Metadata = encodeMeta(t.enrichMeta(meta))
	}

	if err := t.store.UpdateExecutionStep(ctx, step); err != nil {
		log.WithError(err).WithField("step_id", step.ID).Warn("[Tracker] finalize execution step failed")
	}

	t.replaceStep(*step)
	t.fireOnStep(*step)
}

// newStep 构造步骤通用字段（不含状态/输出），由调用方按 record/begin 语义填充剩余字段。
func (t *StepTracker) newStep(ctx context.Context, stepType model.StepType, name, input string) *model.ExecutionStep {
	t.mu.Lock()
	t.stepOrder++
	order := t.stepOrder
	msgID := t.messageID
	t.mu.Unlock()

	return &model.ExecutionStep{
		MessageID:      msgID,
		ConversationID: t.conversationID,
		StepOrder:      order,
		StepType:       stepType,
		Name:           name,
		Input:          truncate(input, 65000),
		SubAgentCallID: subAgentCallID(ctx),
		SubAgentDepth:  subAgentDepth(ctx),
	}
}

func encodeMeta(meta *model.StepMetadata) model.JSON {
	if meta == nil {
		return nil
	}
	data, err := json.Marshal(meta)
	if err != nil {
		log.WithError(err).Warn("[Tracker] marshal step metadata failed")
		return nil
	}
	return model.JSON(data)
}

func (t *StepTracker) appendStep(step model.ExecutionStep) {
	t.mu.Lock()
	t.steps = append(t.steps, step)
	t.mu.Unlock()
}

// replaceStep 按 step_order 替换内存中已有步骤（FinalizeStep 用），保证 Steps() 返回最新状态。
func (t *StepTracker) replaceStep(step model.ExecutionStep) {
	t.mu.Lock()
	for i := range t.steps {
		if t.steps[i].StepOrder == step.StepOrder {
			t.steps[i] = step
			t.mu.Unlock()
			return
		}
	}
	t.steps = append(t.steps, step)
	t.mu.Unlock()
}

func (t *StepTracker) fireOnStep(step model.ExecutionStep) {
	t.mu.Lock()
	fn := t.onStep
	t.mu.Unlock()
	if fn != nil {
		fn(step)
	}
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
