package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/chowyu12/aiclaw/internal/model"
	harnesspkg "github.com/chowyu12/aiclaw/pkg/harness"
)

// StartBackgroundRun persists a durable run before execution and returns its
// ID immediately. The caller may disconnect; only explicit cancellation stops
// the Agent.
func (e *Executor) StartBackgroundRun(ctx context.Context, req model.ChatRequest) (*model.AgentRun, error) {
	if err := e.checkShutdown(); err != nil {
		return nil, err
	}
	ag, err := e.loadAgent(ctx, req.AgentUUID)
	if err != nil {
		e.activeExecs.Done()
		return nil, err
	}
	if ag.ExecutionMode == model.AgentExecutionLocal {
		defer e.activeExecs.Done()
		return e.startLocalBackgroundRun(ctx, req, ag)
	}

	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	ec, err := e.prepare(runCtx, req)
	if err != nil {
		cancel()
		e.activeExecs.Done()
		return nil, err
	}

	// Plan State is conversation-scoped, so overlapping foreground runs in the
	// same conversation would produce an ambiguous plan and step timeline.
	active, _, err := e.store.ListAgentRuns(runCtx, model.AgentRunListQuery{
		ConversationUUID: ec.conv.UUID,
		Status:           model.AgentRunRunning,
		Page:             1,
		PageSize:         1,
	})
	if err != nil {
		cancel()
		e.activeExecs.Done()
		return nil, fmt.Errorf("check active runs: %w", err)
	}
	if len(active) > 0 {
		cancel()
		e.activeExecs.Done()
		return nil, fmt.Errorf("conversation already has a running agent run")
	}

	run, err := e.beginAgentRun(runCtx, ec)
	if err != nil {
		cancel()
		e.activeExecs.Done()
		return nil, err
	}
	e.runHub.start(run.UUID, cancel)
	e.runHub.publish(run.UUID, model.AgentRunEvent{
		Type:      model.AgentRunEventStarted,
		Status:    model.AgentRunRunning,
		Run:       cloneAgentRun(run),
		CreatedAt: time.Now(),
	})

	go e.executeBackgroundRun(runCtx, ec, run.UUID, cancel)
	return cloneAgentRun(run), nil
}

func (e *Executor) executeBackgroundRun(ctx context.Context, ec *execContext, runID string, cancel context.CancelFunc) {
	defer e.activeExecs.Done()
	defer ec.closeMCP()
	defer cancel()
	defer e.runHub.complete(runID)

	result, execErr := e.runPreparedStream(ctx, ec, func(chunk model.StreamChunk) error {
		e.runHub.publish(runID, model.AgentRunEvent{
			Type:      model.AgentRunEventChunk,
			Status:    model.AgentRunRunning,
			Chunk:     &chunk,
			CreatedAt: time.Now(),
		})
		return nil
	}, harnesspkg.NoopSink{})

	var errorMessageID int64
	if execErr != nil && !errors.Is(execErr, context.Canceled) && ctx.Err() == nil {
		errorMessageID = e.saveErrorMessage(ec, execErr)
	}
	run := e.finishAgentRun(ec, result, execErr, errorMessageID)
	eventType := model.AgentRunEventCompleted
	if run.Status == model.AgentRunFailed {
		eventType = model.AgentRunEventFailed
	}
	if run.Status == model.AgentRunCancelled {
		eventType = model.AgentRunEventCancelled
	}
	e.runHub.publish(runID, model.AgentRunEvent{
		Type:      eventType,
		Status:    run.Status,
		Run:       cloneAgentRun(run),
		Error:     run.Error,
		CreatedAt: time.Now(),
	})
}

func (e *Executor) beginAgentRun(ctx context.Context, ec *execContext) (*model.AgentRun, error) {
	if ec == nil || ec.ephemeral {
		return nil, nil
	}
	run := &model.AgentRun{
		AgentID:          ec.ag.ID,
		AgentUUID:        ec.ag.UUID,
		ConversationID:   ec.conv.ID,
		ConversationUUID: ec.conv.UUID,
		UserID:           ec.conv.UserID,
		Input:            ec.userMsg,
		Status:           model.AgentRunRunning,
		StartedAt:        time.Now(),
	}
	if err := e.store.CreateAgentRun(ctx, run); err != nil {
		return nil, fmt.Errorf("create agent run: %w", err)
	}
	ec.run = run
	ec.tracker.SetRunUUID(run.UUID)
	ec.l = ec.l.WithField("run", run.UUID)
	return run, nil
}

func (e *Executor) finishAgentRun(ec *execContext, result *ExecuteResult, execErr error, errorMessageID int64) *model.AgentRun {
	if ec == nil || ec.run == nil {
		return nil
	}
	run := ec.run
	status := model.AgentRunSucceeded
	errorText := ""
	if execErr != nil {
		status = model.AgentRunFailed
		errorText = execErr.Error()
		if errors.Is(execErr, context.Canceled) || ec.ctx.Err() != nil {
			status = model.AgentRunCancelled
		}
	}
	finishedAt := time.Now()
	updates := map[string]any{
		"status":      status,
		"error":       errorText,
		"finished_at": finishedAt,
	}
	if result != nil {
		updates["message_id"] = result.MessageID
		updates["content"] = result.Content
		updates["tokens_used"] = result.TokensUsed
		updates["duration_ms"] = result.DurationMs
		result.RunID = run.UUID
	} else if errorMessageID != 0 {
		updates["message_id"] = errorMessageID
	}

	persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.store.UpdateAgentRun(persistCtx, run.ID, updates); err != nil {
		ec.l.WithError(err).Warn("[Run] update agent run failed")
	}
	run.Status = status
	run.Error = errorText
	run.FinishedAt = &finishedAt
	if result != nil {
		run.MessageID = result.MessageID
		run.Content = result.Content
		run.TokensUsed = result.TokensUsed
		run.DurationMs = result.DurationMs
	} else if errorMessageID != 0 {
		run.MessageID = errorMessageID
	}
	return run
}

// GetAgentRun returns the persisted run snapshot for reconnect and history.
func (e *Executor) GetAgentRun(ctx context.Context, runID string) (*model.AgentRun, error) {
	return e.store.GetAgentRunByUUID(ctx, runID)
}

// SubscribeAgentRun returns the active SSE event stream when the run is still
// running. A false active value means callers should rebuild the final state
// from storage instead.
func (e *Executor) SubscribeAgentRun(ctx context.Context, runID string) (*model.AgentRun, <-chan model.AgentRunEvent, func(), bool, error) {
	run, err := e.store.GetAgentRunByUUID(ctx, runID)
	if err != nil {
		return nil, nil, nil, false, err
	}
	events, unsubscribe, active := e.runHub.subscribe(runID)
	// Local runs survive a server restart in the database and may still be
	// queued or executing on the runtime. Recreate their in-memory stream entry
	// so a reconnecting browser can keep waiting for subsequent callbacks.
	if !active && run.RuntimeID > 0 && (run.Status == model.AgentRunQueued || run.Status == model.AgentRunRunning) {
		e.runHub.start(runID, nil)
		events, unsubscribe, active = e.runHub.subscribe(runID)
	}
	return run, events, unsubscribe, active, nil
}

// CancelAgentRun requests cancellation for a live background run. A stale
// running record (for example after restart) is closed persistently as well.
func (e *Executor) CancelAgentRun(ctx context.Context, runID string) (*model.AgentRun, error) {
	run, err := e.store.GetAgentRunByUUID(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run.Status != model.AgentRunRunning && !(run.RuntimeID > 0 && run.Status == model.AgentRunQueued) {
		return run, nil
	}
	if e.runHub.cancel(runID) {
		return run, nil
	}
	cancelReason := "cancelled after executor restart"
	if run.RuntimeID > 0 {
		cancelReason = "cancelled by user"
	}
	finishedAt := time.Now()
	if err := e.store.UpdateAgentRun(ctx, run.ID, map[string]any{
		"status":      model.AgentRunCancelled,
		"error":       cancelReason,
		"finished_at": finishedAt,
	}); err != nil {
		return nil, err
	}
	run.Status = model.AgentRunCancelled
	run.Error = cancelReason
	run.FinishedAt = &finishedAt
	e.runHub.publish(runID, model.AgentRunEvent{
		Type: model.AgentRunEventCancelled, Status: run.Status, Run: cloneAgentRun(run), Error: run.Error, CreatedAt: finishedAt,
	})
	e.runHub.complete(runID)
	return run, nil
}

func cloneAgentRun(run *model.AgentRun) *model.AgentRun {
	if run == nil {
		return nil
	}
	clone := *run
	if run.FinishedAt != nil {
		finishedAt := *run.FinishedAt
		clone.FinishedAt = &finishedAt
	}
	return &clone
}
