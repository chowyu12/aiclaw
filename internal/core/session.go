// Package core contains the Codex-style session and turn state machine.
// Provider sampling and tool dispatch are attached to this boundary rather
// than being owned by an HTTP handler or a UI client.
package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/protocol"
)

// ThreadStore is the minimum durable contract required by a live session.
// GormStore implements this while keeping the core independent of GORM.
type ThreadStore interface {
	GetThreadByUUID(context.Context, string, bool) (*model.Thread, error)
	AppendRollout(context.Context, int64, []model.RolloutItem) ([]model.RolloutItem, error)
	LoadRollout(context.Context, int64) ([]model.RolloutItem, error)
}

type attachmentResolver interface {
	GetFileByUUID(context.Context, string) (*model.File, error)
}

type EventSink func(protocol.Event) error

// Session serializes turns for a single thread, matching Codex's invariant
// that a thread has at most one active sampling task at a time.
type Session struct {
	store  ThreadStore
	thread *model.Thread
	emit   EventSink

	mu     sync.Mutex
	active *Turn
}

type Turn struct {
	ID          string
	Input       string
	Attachments []string
	StartedAt   time.Time
}

func Resume(ctx context.Context, store ThreadStore, threadID string, emit EventSink) (*Session, error) {
	thread, err := store.GetThreadByUUID(ctx, threadID, false)
	if err != nil {
		return nil, fmt.Errorf("resume thread: %w", err)
	}
	return &Session{store: store, thread: thread, emit: emit}, nil
}

func (s *Session) Thread() model.Thread { return *s.thread }

// ModelContext rebuilds the ordered, model-visible rollout suffix used for a
// resumed sampling request. The caller supplies a hard byte limit so a thread
// can never inject unbounded persisted history into a Provider request.
func (s *Session) ModelContext(ctx context.Context, maxBytes int) ([]model.RolloutItem, error) {
	items, err := s.store.LoadRollout(ctx, s.thread.ID)
	if err != nil {
		return nil, fmt.Errorf("load rollout: %w", err)
	}
	visible := make([]model.RolloutItem, 0, len(items))
	used := 0
	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		if !item.ModelVisible || item.Kind == model.RolloutAssistantDelta {
			continue
		}
		size := len(item.Payload)
		if maxBytes > 0 && used+size > maxBytes {
			break
		}
		visible = append(visible, item)
		used += size
	}
	for left, right := 0, len(visible)-1; left < right; left, right = left+1, right-1 {
		visible[left], visible[right] = visible[right], visible[left]
	}
	return visible, nil
}

// StartTurn persists the input before sampling begins. If the process exits
// after this call, the next session can reconstruct the unfinished turn from
// the durable rollout rather than relying on an in-memory chat request.
func (s *Session) StartTurn(ctx context.Context, input string) (*Turn, error) {
	return s.startTurn(ctx, input, nil)
}

func (s *Session) startTurn(ctx context.Context, input string, attachments []string) (*Turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != nil {
		return nil, fmt.Errorf("thread already has an active turn")
	}
	attachments = uniqueAttachmentIDs(attachments)
	turn := &Turn{ID: uuid.NewString(), Input: input, Attachments: attachments, StartedAt: time.Now()}
	payload, err := json.Marshal(struct {
		Content     string   `json:"content"`
		Attachments []string `json:"attachments,omitempty"`
	}{Content: input, Attachments: attachments})
	if err != nil {
		return nil, err
	}
	items, err := s.store.AppendRollout(ctx, s.thread.ID, []model.RolloutItem{
		{TurnID: turn.ID, Kind: model.RolloutTurnStarted, Payload: model.JSON(`{}`)},
		{TurnID: turn.ID, Kind: model.RolloutUserMessage, ModelVisible: true, Payload: model.JSON(payload)},
	})
	if err != nil {
		return nil, fmt.Errorf("persist turn input: %w", err)
	}
	s.active = turn
	s.emitEvent(protocol.Event{Kind: protocol.EventTurnStarted, ThreadID: s.thread.UUID, TurnID: turn.ID, Item: &items[0]})
	return turn, nil
}

// RunTurn is the Codex-style sampling loop owned by Session. Provider and
// tool implementations are injected adapters; neither persistence nor the
// local App participates in the loop.
func (s *Session) RunTurn(ctx context.Context, input string, sampler Sampler, dispatcher ToolDispatcher) error {
	return s.RunTurnWithAttachments(ctx, input, nil, sampler, dispatcher)
}

func (s *Session) RunTurnWithAttachments(ctx context.Context, input string, attachments []string, sampler Sampler, dispatcher ToolDispatcher) error {
	turn, err := s.startTurn(ctx, input, attachments)
	if err != nil {
		return err
	}
	if preparer, ok := dispatcher.(TurnContextPreparer); ok {
		ctx, err = preparer.PrepareTurn(ctx, *s.thread, *turn)
		if err != nil {
			_ = s.FailTurn(ctx, turn.ID, err)
			return err
		}
	}
	for iteration := 0; iteration < 50; iteration++ {
		contextItems, err := s.ModelContext(ctx, 200_000)
		if err != nil {
			_ = s.FailTurn(ctx, turn.ID, err)
			return err
		}
		messages := make([]SamplingMessage, 0, len(contextItems))
		if provider, ok := dispatcher.(ContextProvider); ok {
			pluginContext, contextErr := provider.ContextMessages(ctx, *s.thread)
			if contextErr != nil {
				_ = s.FailTurn(ctx, turn.ID, contextErr)
				return contextErr
			}
			messages = append(messages, pluginContext...)
		}
		for _, item := range contextItems {
			var payload struct {
				Content     string     `json:"content"`
				Attachments []string   `json:"attachments"`
				Name        string     `json:"name"`
				CallID      string     `json:"call_id"`
				ToolCalls   []ToolCall `json:"tool_calls"`
				Output      string     `json:"output"`
				Error       string     `json:"error"`
			}
			_ = json.Unmarshal(item.Payload, &payload)
			switch item.Kind {
			case model.RolloutUserMessage:
				files, resolveErr := s.resolveAttachments(ctx, payload.Attachments)
				if resolveErr != nil {
					_ = s.FailTurn(ctx, turn.ID, resolveErr)
					return resolveErr
				}
				messages = append(messages, SamplingMessage{Role: "user", Content: payload.Content, Attachments: files})
			case model.RolloutAssistantFinal:
				messages = append(messages, SamplingMessage{Role: "assistant", Content: payload.Content})
			case model.RolloutToolRequested:
				messages = append(messages, SamplingMessage{Role: "assistant", Content: payload.Content, ToolCalls: payload.ToolCalls})
			case model.RolloutToolCompleted:
				content := payload.Output
				if content == "" {
					content = payload.Content
				}
				if payload.Error != "" {
					content = "tool error: " + payload.Error
				}
				if payload.CallID == "" {
					// Legacy conversations did not persist tool-call IDs. Preserve
					// their useful output without emitting an invalid tool message.
					messages = append(messages, SamplingMessage{Role: "system", Content: "Legacy tool output: " + content})
				} else {
					messages = append(messages, SamplingMessage{Role: "tool", Content: content, Name: payload.Name, ToolCallID: payload.CallID})
				}
			}
		}
		definitions, err := dispatcher.Definitions(ctx, *s.thread)
		if err != nil {
			_ = s.FailTurn(ctx, turn.ID, err)
			return err
		}
		result, err := sampler.Sample(ctx, SamplingRequest{Thread: *s.thread, Messages: messages, Tools: definitions}, func(delta string) error { return s.AppendAssistantDelta(ctx, turn.ID, delta) })
		if err != nil {
			_ = s.FailTurn(ctx, turn.ID, err)
			return err
		}
		if len(result.ToolCalls) == 0 {
			return s.CompleteTurn(ctx, turn.ID, result.Text)
		}
		if err := s.AppendToolRequests(ctx, turn.ID, result.Text, result.ToolCalls); err != nil {
			_ = s.FailTurn(ctx, turn.ID, err)
			return err
		}
		for _, call := range result.ToolCalls {
			s.emitEvent(protocol.Event{
				Kind: protocol.EventToolLifecycle, ThreadID: s.thread.UUID, TurnID: turn.ID,
				CallID: call.ID, Name: call.Name, Status: string(model.StepRunning), Message: "正在执行工具",
			})
			toolResult, callErr := dispatcher.Execute(ctx, *s.thread, call)
			if toolResult.CallID == "" {
				toolResult.CallID = call.ID
			}
			if toolResult.Name == "" {
				toolResult.Name = call.Name
			}
			if callErr != nil {
				toolResult.Error = callErr.Error()
			}
			status := model.StepSuccess
			if toolResult.Error != "" {
				status = model.StepError
			}
			if err := s.AppendToolResult(ctx, turn.ID, toolResult.CallID, model.ExecutionStep{Name: toolResult.Name, Input: call.Arguments, Output: toolResult.Content, Error: toolResult.Error, Status: status}); err != nil {
				_ = s.FailTurn(ctx, turn.ID, err)
				return err
			}
		}
	}
	err = fmt.Errorf("turn exceeded tool iteration limit")
	_ = s.FailTurn(ctx, turn.ID, err)
	return err
}

func uniqueAttachmentIDs(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func (s *Session) resolveAttachments(ctx context.Context, ids []string) ([]*model.File, error) {
	ids = uniqueAttachmentIDs(ids)
	if len(ids) == 0 {
		return nil, nil
	}
	resolver, ok := s.store.(attachmentResolver)
	if !ok {
		return nil, fmt.Errorf("attachment storage is unavailable")
	}
	files := make([]*model.File, 0, len(ids))
	for _, id := range ids {
		file, err := resolver.GetFileByUUID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("load attachment %q: %w", id, err)
		}
		if file.ThreadID != 0 && file.ThreadID != s.thread.ID {
			return nil, fmt.Errorf("attachment %q belongs to another conversation", file.Filename)
		}
		files = append(files, file)
	}
	return files, nil
}

// AppendToolRequests persists the assistant tool-call message before any tool
// executes. The following tool results can therefore be reconstructed with
// their call IDs into a provider-valid conversation after a restart.
func (s *Session) AppendToolRequests(ctx context.Context, turnID, content string, calls []ToolCall) error {
	item, err := s.append(ctx, turnID, model.RolloutToolRequested, true, map[string]any{
		"content": content, "tool_calls": calls,
	})
	if err == nil {
		for _, call := range calls {
			s.emitEvent(protocol.Event{
				Kind: protocol.EventToolLifecycle, ThreadID: s.thread.UUID, TurnID: turnID, Item: item,
				CallID: call.ID, Name: call.Name, Status: string(model.StepPending), Message: "等待执行工具",
			})
		}
	}
	return err
}

func (s *Session) AppendAssistantDelta(ctx context.Context, turnID, delta string) error {
	_ = ctx
	payload, err := json.Marshal(map[string]string{"content": delta})
	if err != nil {
		return err
	}
	// Deltas are transport events, not durable state. Persisting every token in
	// its own SQLite transaction throttles long streams; the complete assistant
	// message is durably written once by CompleteTurn.
	item := &model.RolloutItem{
		ThreadID: s.thread.ID, TurnID: turnID, Kind: model.RolloutAssistantDelta,
		Payload: model.JSON(payload), CreatedAt: time.Now(),
	}
	s.emitEvent(protocol.Event{Kind: protocol.EventAssistantDelta, ThreadID: s.thread.UUID, TurnID: turnID, Item: item, Delta: delta})
	return nil
}

// AppendToolResult persists tool lifecycle output as a model-visible rollout
// item. MCP and search are both represented through this same tool boundary.
func (s *Session) AppendToolResult(ctx context.Context, turnID, callID string, step model.ExecutionStep) error {
	item, err := s.append(ctx, turnID, model.RolloutToolCompleted, true, map[string]any{
		"call_id": callID, "name": step.Name, "status": step.Status, "input": step.Input,
		"output": step.Output, "error": step.Error,
	})
	if err == nil {
		message := "工具执行完成"
		if step.Status == model.StepError {
			message = "工具执行失败"
		}
		s.emitEvent(protocol.Event{
			Kind: protocol.EventToolLifecycle, ThreadID: s.thread.UUID, TurnID: turnID, Item: item,
			CallID: callID, Name: step.Name, Status: string(step.Status), Message: message, Error: step.Error,
		})
	}
	return err
}

func (s *Session) AppendPlanUpdate(ctx context.Context, turnID string, plan *model.PlanState) error {
	if plan == nil {
		return nil
	}
	_, err := s.append(ctx, turnID, model.RolloutPlanUpdated, true, plan)
	return err
}

func (s *Session) CompleteTurn(ctx context.Context, turnID, content string) error {
	return s.closeTurn(ctx, turnID, model.RolloutTurnCompleted, map[string]string{"content": content}, "")
}

func (s *Session) FailTurn(ctx context.Context, turnID string, cause error) error {
	return s.closeTurn(ctx, turnID, model.RolloutTurnFailed, map[string]string{"error": cause.Error()}, cause.Error())
}

func (s *Session) append(ctx context.Context, turnID string, kind model.RolloutKind, visible bool, payload any) (*model.RolloutItem, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	items, err := s.store.AppendRollout(ctx, s.thread.ID, []model.RolloutItem{{TurnID: turnID, Kind: kind, ModelVisible: visible, Payload: model.JSON(encoded)}})
	if err != nil {
		return nil, err
	}
	return &items[0], nil
}

func (s *Session) closeTurn(ctx context.Context, turnID string, kind model.RolloutKind, payload any, eventError string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil || s.active.ID != turnID {
		return fmt.Errorf("turn %q is not active", turnID)
	}
	final, err := s.append(ctx, turnID, model.RolloutAssistantFinal, true, payload)
	if err != nil {
		return err
	}
	completed, err := s.append(ctx, turnID, kind, false, map[string]string{})
	if err != nil {
		return err
	}
	s.active = nil
	eventKind := protocol.EventTurnCompleted
	if kind == model.RolloutTurnFailed {
		eventKind = protocol.EventTurnFailed
	}
	s.emitEvent(protocol.Event{Kind: eventKind, ThreadID: s.thread.UUID, TurnID: turnID, Item: completed, Error: eventError})
	_ = final
	return nil
}

func (s *Session) emitEvent(event protocol.Event) {
	if s.emit != nil {
		_ = s.emit(event)
	}
}
