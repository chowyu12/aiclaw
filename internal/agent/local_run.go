package agent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chowyu12/aiclaw/internal/model"
)

func (e *Executor) startLocalBackgroundRun(ctx context.Context, req model.ChatRequest, ag *model.Agent) (*model.AgentRun, error) {
	if ag == nil || ag.RuntimeID <= 0 {
		return nil, errors.New("local agent has no runtime configured")
	}
	runtime, err := e.store.GetRuntime(ctx, ag.RuntimeID)
	if err != nil {
		return nil, fmt.Errorf("runtime not found: %w", err)
	}
	if !runtime.IsOnline(time.Now()) {
		return nil, fmt.Errorf("runtime %q is offline", runtime.Name)
	}
	if _, _, _, err := e.localCLIFor(ctx, ag, runtime); err != nil {
		return nil, err
	}

	isNewConversation := req.ConversationID == ""
	conv, err := e.memory.GetOrCreateConversation(ctx, req.ConversationID, req.UserID, ag.UUID)
	if err != nil {
		return nil, fmt.Errorf("get conversation: %w", err)
	}
	for _, status := range []model.AgentRunStatus{model.AgentRunQueued, model.AgentRunRunning} {
		active, _, listErr := e.store.ListAgentRuns(ctx, model.AgentRunListQuery{
			ConversationUUID: conv.UUID,
			Status:           status,
			Page:             1,
			PageSize:         1,
		})
		if listErr != nil {
			return nil, fmt.Errorf("check active runs: %w", listErr)
		}
		if len(active) > 0 {
			return nil, errors.New("conversation already has a running agent run")
		}
	}

	_, uploadedFiles := e.loadRequestFiles(ctx, req.Files, conv.ID)
	if _, err := e.memory.SaveUserMessage(ctx, conv.ID, req.Message, uploadedFiles); err != nil {
		return nil, fmt.Errorf("save user message: %w", err)
	}
	if isNewConversation {
		go e.memory.AutoSetTitle(context.WithoutCancel(ctx), conv.ID, req.Message)
	}

	run := &model.AgentRun{
		AgentID:          ag.ID,
		AgentUUID:        ag.UUID,
		RuntimeID:        runtime.ID,
		ConversationID:   conv.ID,
		ConversationUUID: conv.UUID,
		UserID:           conv.UserID,
		Input:            req.Message,
		Status:           model.AgentRunQueued,
		StartedAt:        time.Now(),
	}
	if err := e.store.CreateAgentRun(ctx, run); err != nil {
		return nil, fmt.Errorf("create agent run: %w", err)
	}
	e.runHub.start(run.UUID, nil)
	e.runHub.publish(run.UUID, model.AgentRunEvent{
		Type: model.AgentRunEventStarted, Status: model.AgentRunQueued, Run: cloneAgentRun(run), CreatedAt: time.Now(),
	})
	return cloneAgentRun(run), nil
}

// ClaimLocalAgentRun atomically claims the oldest queued run for one runtime.
func (e *Executor) ClaimLocalAgentRun(ctx context.Context, runtimeID int64) (*model.RuntimeTask, error) {
	run, err := e.store.ClaimQueuedAgentRun(ctx, runtimeID)
	if err != nil {
		return nil, err
	}
	ag, err := e.store.GetAgent(ctx, run.AgentID)
	if err != nil {
		return nil, e.failClaimedLocalAgentRun(ctx, run, fmt.Errorf("load local agent: %w", err))
	}
	if ag.ExecutionMode != model.AgentExecutionLocal || ag.RuntimeID != runtimeID {
		return nil, e.failClaimedLocalAgentRun(ctx, run, errors.New("local agent runtime changed while the run was queued"))
	}
	runtime, err := e.store.GetRuntime(ctx, runtimeID)
	if err != nil {
		return nil, e.failClaimedLocalAgentRun(ctx, run, fmt.Errorf("load runtime: %w", err))
	}
	command, args, promptMode, err := e.localCLIFor(ctx, ag, runtime)
	if err != nil {
		return nil, e.failClaimedLocalAgentRun(ctx, run, err)
	}
	modelName, err := e.localAgentModel(ctx, ag, runtime)
	if err != nil {
		return nil, e.failClaimedLocalAgentRun(ctx, run, err)
	}
	msgs, err := e.store.ListMessages(ctx, run.ConversationID, ag.HistoryLimit())
	if err != nil {
		return nil, e.failClaimedLocalAgentRun(ctx, run, fmt.Errorf("load conversation history: %w", err))
	}
	taskMessages := make([]model.RuntimeTaskMessage, 0, len(msgs))
	for _, msg := range msgs {
		if msg.Role != "user" && msg.Role != "assistant" {
			continue
		}
		if strings.TrimSpace(msg.Content) == "" {
			continue
		}
		taskMessages = append(taskMessages, model.RuntimeTaskMessage{Role: msg.Role, Content: msg.Content})
	}
	e.runHub.publish(run.UUID, model.AgentRunEvent{
		Type: model.AgentRunEventUpdated, Status: model.AgentRunRunning, Run: cloneAgentRun(run), CreatedAt: time.Now(),
	})
	return &model.RuntimeTask{
		RunID:          run.UUID,
		ConversationID: run.ConversationUUID,
		AgentName:      ag.Name,
		AgentType:      ag.EffectiveLocalAgentType(),
		ModelName:      modelName,
		SystemPrompt:   ag.SystemPrompt,
		Messages:       taskMessages,
		Command:        command,
		Args:           args,
		PromptMode:     promptMode,
		WorkingDir:     ag.WorkingDir,
		TimeoutSeconds: ag.TimeoutSeconds(),
	}, nil
}

func (e *Executor) localAgentModel(ctx context.Context, ag *model.Agent, runtime *model.Runtime) (string, error) {
	if ag == nil || runtime == nil || ag.EffectiveLocalAgentType() == model.RuntimeAgentTypeCustom {
		return "", nil
	}
	config, err := e.store.GetRuntimeAgentConfig(ctx, runtime.ID, ag.EffectiveLocalAgentType())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("load local agent configuration: %w", err)
	}
	return strings.TrimSpace(config.ModelName), nil
}

func (e *Executor) localCLIFor(ctx context.Context, ag *model.Agent, runtime *model.Runtime) (string, []string, string, error) {
	if ag == nil || runtime == nil {
		return "", nil, "", errors.New("local agent runtime is not configured")
	}
	agentType := ag.EffectiveLocalAgentType()
	if agentType != model.RuntimeAgentTypeCustom {
		if !runtime.HasDetectedAgent(agentType) {
			return "", nil, "", fmt.Errorf("local agent CLI %q is not detected on runtime %q", agentType, runtime.Name)
		}
		spec, ok := model.LocalCLISpecFor(agentType)
		if !ok {
			return "", nil, "", fmt.Errorf("unsupported local agent CLI %q", agentType)
		}
		config, err := e.store.GetRuntimeAgentConfig(ctx, runtime.ID, agentType)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", nil, "", fmt.Errorf("load local agent configuration: %w", err)
		}
		if config != nil && !config.Enabled {
			return "", nil, "", fmt.Errorf("local agent CLI %q is disabled on runtime %q", agentType, runtime.Name)
		}
		modelName := ""
		if config != nil {
			modelName = config.ModelName
		}
		return spec.Command, spec.ArgsWithModel(modelName), spec.PromptMode, nil
	}
	if strings.TrimSpace(runtime.Command) == "" {
		return "", nil, "", errors.New("select a detected local agent CLI")
	}
	return runtime.Command, append([]string(nil), runtime.Args...), runtime.EffectivePromptMode(), nil
}

func (e *Executor) failClaimedLocalAgentRun(ctx context.Context, run *model.AgentRun, cause error) error {
	if run == nil || cause == nil {
		return cause
	}
	finishedAt := time.Now()
	errorText := cause.Error()
	if err := e.store.UpdateAgentRun(ctx, run.ID, map[string]any{
		"status":      model.AgentRunFailed,
		"error":       errorText,
		"finished_at": finishedAt,
	}); err != nil {
		return fmt.Errorf("%v; persist failed run: %w", cause, err)
	}
	run.Status = model.AgentRunFailed
	run.Error = errorText
	run.FinishedAt = &finishedAt
	e.runHub.publish(run.UUID, model.AgentRunEvent{
		Type: model.AgentRunEventFailed, Status: run.Status, Run: cloneAgentRun(run), Error: errorText, CreatedAt: finishedAt,
	})
	e.runHub.complete(run.UUID)
	return cause
}

func (e *Executor) PublishLocalAgentRun(ctx context.Context, runtimeID int64, runID, delta string) error {
	if delta == "" {
		return nil
	}
	run, err := e.store.GetAgentRunByUUID(ctx, runID)
	if err != nil {
		return err
	}
	if run.RuntimeID != runtimeID {
		return errors.New("run does not belong to this runtime")
	}
	if run.Status != model.AgentRunRunning {
		return errors.New("run is not running")
	}
	e.runHub.publish(runID, model.AgentRunEvent{
		Type:      model.AgentRunEventChunk,
		Status:    model.AgentRunRunning,
		Chunk:     &model.StreamChunk{RunID: runID, ConversationID: run.ConversationUUID, Delta: delta},
		CreatedAt: time.Now(),
	})
	return nil
}

func (e *Executor) CompleteLocalAgentRun(ctx context.Context, runtimeID int64, runID, content, errorText string) (*model.AgentRun, error) {
	run, err := e.store.GetAgentRunByUUID(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run.RuntimeID != runtimeID {
		return nil, errors.New("run does not belong to this runtime")
	}
	if run.Status != model.AgentRunRunning {
		if run.Status == model.AgentRunSucceeded || run.Status == model.AgentRunFailed || run.Status == model.AgentRunCancelled {
			return run, nil
		}
		return nil, errors.New("run is not running")
	}

	status := model.AgentRunSucceeded
	messageContent := strings.TrimSpace(content)
	errorText = strings.TrimSpace(errorText)
	if errorText != "" {
		status = model.AgentRunFailed
		messageContent = "[Error] " + errorText
	} else if messageContent == "" {
		status = model.AgentRunFailed
		errorText = "local agent returned an empty response"
		messageContent = "[Error] " + errorText
	}
	durationMs := int(time.Since(run.StartedAt).Milliseconds())
	messageID, err := e.memory.SaveAssistantMessage(ctx, run.ConversationID, messageContent, 0, durationMs)
	if err != nil {
		return nil, fmt.Errorf("save local agent response: %w", err)
	}
	finishedAt := time.Now()
	if err := e.store.UpdateAgentRun(ctx, run.ID, map[string]any{
		"message_id":  messageID,
		"content":     messageContent,
		"status":      status,
		"error":       errorText,
		"duration_ms": durationMs,
		"finished_at": finishedAt,
	}); err != nil {
		return nil, err
	}
	run.MessageID = messageID
	run.Content = messageContent
	run.Status = status
	run.Error = errorText
	run.DurationMs = durationMs
	run.FinishedAt = &finishedAt

	if status == model.AgentRunSucceeded {
		e.runHub.publish(runID, model.AgentRunEvent{
			Type: model.AgentRunEventChunk, Status: status,
			Chunk:     &model.StreamChunk{RunID: runID, ConversationID: run.ConversationUUID, MessageID: messageID, Content: messageContent, DurationMs: durationMs, Done: true},
			CreatedAt: finishedAt,
		})
	}
	eventType := model.AgentRunEventCompleted
	if status == model.AgentRunFailed {
		eventType = model.AgentRunEventFailed
	}
	e.runHub.publish(runID, model.AgentRunEvent{
		Type: eventType, Status: status, Run: cloneAgentRun(run), Error: errorText, CreatedAt: finishedAt,
	})
	e.runHub.complete(runID)
	return cloneAgentRun(run), nil
}

func IsNoRuntimeTask(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
