package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/store"
)

const planToolName = "plan"

const (
	planPromptMaxFieldRunes  = 240
	planPromptMaxReasonRunes = 320
)

type planContextKey struct{}

type PlanManager struct {
	store          store.PlanStore
	conversationID int64
	mu             sync.Mutex
	run            *model.PlanRun
	onChange       func(*model.PlanState)
}

type planArgs struct {
	Action string         `json:"action"`
	Goal   string         `json:"goal,omitempty"`
	Items  []planItemArgs `json:"items,omitempty"`
	Reason string         `json:"reason,omitempty"`
}

type planItemArgs struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	Status string `json:"status,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type planToolResult struct {
	Success bool             `json:"success"`
	Action  string           `json:"action"`
	Plan    *model.PlanState `json:"plan,omitempty"`
	Summary string           `json:"summary,omitempty"`
	Error   string           `json:"error,omitempty"`
}

func NewPlanManager(s store.PlanStore, conversationID int64) *PlanManager {
	return &PlanManager{store: s, conversationID: conversationID}
}

func WithPlanManager(ctx context.Context, pm *PlanManager) context.Context {
	if pm == nil {
		return ctx
	}
	return context.WithValue(ctx, planContextKey{}, pm)
}

func planManagerFromContext(ctx context.Context) *PlanManager {
	if pm, ok := ctx.Value(planContextKey{}).(*PlanManager); ok {
		return pm
	}
	return nil
}

func (pm *PlanManager) SetOnChange(fn func(*model.PlanState)) {
	pm.mu.Lock()
	pm.onChange = fn
	pm.mu.Unlock()
}

func (pm *PlanManager) EnsureRun(ctx context.Context, goal string) (*model.PlanRun, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if pm.run != nil {
		return pm.run, nil
	}
	run, err := pm.store.GetActivePlanRun(ctx, pm.conversationID)
	if err == nil {
		pm.run = run
		return run, nil
	}
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	run = &model.PlanRun{
		ConversationID: pm.conversationID,
		Goal:           strings.TrimSpace(goal),
		Status:         model.PlanStatusActive,
	}
	if err := pm.store.CreatePlanRun(ctx, run); err != nil {
		return nil, err
	}
	pm.run = run
	return run, nil
}

func (pm *PlanManager) State(ctx context.Context) (*model.PlanState, error) {
	run, err := pm.EnsureRun(ctx, "")
	if err != nil {
		return nil, err
	}
	items, err := pm.store.ListPlanItems(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	return &model.PlanState{
		ID:             run.ID,
		UUID:           run.UUID,
		ConversationID: run.ConversationID,
		MessageID:      run.MessageID,
		Goal:           run.Goal,
		Status:         run.Status,
		RevisionReason: run.RevisionReason,
		Items:          items,
		UpdatedAt:      run.UpdatedAt,
	}, nil
}

func (pm *PlanManager) PromptBlock(ctx context.Context) string {
	state, err := pm.activeState(ctx)
	if err != nil || state == nil || len(state.Items) == 0 {
		return ""
	}
	var running *model.PlanItem
	var pending []model.PlanItem
	done := 0
	for i := range state.Items {
		switch state.Items[i].Status {
		case model.PlanItemRunning:
			running = &state.Items[i]
		case model.PlanItemPending:
			pending = append(pending, state.Items[i])
		case model.PlanItemCompleted, model.PlanItemSkipped:
			done++
		}
	}
	var sb strings.Builder
	sb.WriteString("<plan_state>\n")
	if state.Goal != "" {
		sb.WriteString("Goal: ")
		sb.WriteString(planPromptText(state.Goal, planPromptMaxFieldRunes))
		sb.WriteString("\n")
	}
	if running != nil {
		sb.WriteString("Current step: ")
		sb.WriteString(planPromptText(running.Title, planPromptMaxFieldRunes))
		sb.WriteString("\n")
	} else {
		sb.WriteString("Current step: not set\n")
	}
	if len(pending) > 0 {
		sb.WriteString("Pending steps: ")
		for i, item := range pending {
			if i >= 3 {
				sb.WriteString(fmt.Sprintf(" and %d more", len(pending)-i))
				break
			}
			if i > 0 {
				sb.WriteString("; ")
			}
			sb.WriteString(planPromptText(item.Title, planPromptMaxFieldRunes))
		}
		sb.WriteString("\n")
	}
	if state.RevisionReason != "" {
		sb.WriteString("Latest revision: ")
		sb.WriteString(planPromptText(state.RevisionReason, planPromptMaxReasonRunes))
		sb.WriteString("\n")
	}
	sb.WriteString(fmt.Sprintf("Progress: %d/%d completed\n", done, len(state.Items)))
	sb.WriteString("</plan_state>")
	return sb.String()
}

func (pm *PlanManager) HandleTool(ctx context.Context, args string) (string, error) {
	var p planArgs
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return planErr("unknown", fmt.Sprintf("invalid arguments: %s", err)), nil
	}
	p.Action = strings.TrimSpace(p.Action)
	switch p.Action {
	case "set":
		return pm.handleSet(ctx, p)
	case "update":
		return pm.handleUpdate(ctx, p)
	case "revise":
		return pm.handleRevise(ctx, p)
	case "read":
		return pm.handleRead(ctx, p.Action)
	default:
		return planErr(p.Action, `unknown action, use: set, update, revise, read`), nil
	}
}

func (pm *PlanManager) handleSet(ctx context.Context, p planArgs) (string, error) {
	if len(p.Items) == 0 {
		return planErr("set", "items are required"), nil
	}
	run, err := pm.EnsureRun(ctx, p.Goal)
	if err != nil {
		return "", err
	}
	if p.Goal != "" {
		run.Goal = strings.TrimSpace(p.Goal)
	}
	run.Status = model.PlanStatusActive
	run.RevisionReason = strings.TrimSpace(p.Reason)

	items, err := normalizePlanItems(p.Items)
	if err != nil {
		return planErr("set", err.Error()), nil
	}
	ensureOneRunning(items)
	if err := pm.store.UpdatePlanRun(ctx, run); err != nil {
		return "", err
	}
	if err := pm.store.ReplacePlanItems(ctx, run.ID, items); err != nil {
		return "", err
	}
	pm.emit(ctx)
	return pm.ok(ctx, "set", "plan set"), nil
}

func (pm *PlanManager) handleUpdate(ctx context.Context, p planArgs) (string, error) {
	if len(p.Items) == 0 {
		return planErr("update", "items are required"), nil
	}
	run, err := pm.EnsureRun(ctx, p.Goal)
	if err != nil {
		return "", err
	}
	items, err := pm.store.ListPlanItems(ctx, run.ID)
	if err != nil {
		return "", err
	}
	byKey := make(map[string]int, len(items))
	for i := range items {
		byKey[items[i].ItemKey] = i
	}
	seenUpdate := make(map[string]bool, len(p.Items))
	for _, u := range p.Items {
		itemKey := strings.TrimSpace(u.ID)
		if itemKey == "" {
			return planErr("update", "item id is required"), nil
		}
		if seenUpdate[itemKey] {
			return planErr("update", fmt.Sprintf("duplicate item id %q", itemKey)), nil
		}
		seenUpdate[itemKey] = true
		pos, ok := byKey[itemKey]
		if !ok {
			return planErr("update", fmt.Sprintf("unknown item id %q", itemKey)), nil
		}
		if u.Title != "" {
			items[pos].Title = strings.TrimSpace(u.Title)
		}
		if u.Detail != "" {
			items[pos].Detail = strings.TrimSpace(u.Detail)
		}
		if u.Status != "" {
			status, err := parsePlanItemStatus(u.Status)
			if err != nil {
				return planErr("update", err.Error()), nil
			}
			items[pos].Status = status
		}
		if u.Reason != "" {
			items[pos].Reason = strings.TrimSpace(u.Reason)
		}
	}
	ensureOneRunning(items)
	if p.Reason != "" {
		run.RevisionReason = strings.TrimSpace(p.Reason)
		if err := pm.store.UpdatePlanRun(ctx, run); err != nil {
			return "", err
		}
	}
	if err := pm.store.ReplacePlanItems(ctx, run.ID, items); err != nil {
		return "", err
	}
	pm.emit(ctx)
	return pm.ok(ctx, "update", "plan updated"), nil
}

func (pm *PlanManager) handleRevise(ctx context.Context, p planArgs) (string, error) {
	return pm.handleSet(ctx, planArgs{Action: "set", Goal: p.Goal, Items: p.Items, Reason: p.Reason})
}

func (pm *PlanManager) handleRead(ctx context.Context, action string) (string, error) {
	state, err := pm.activeState(ctx)
	if err == sql.ErrNoRows {
		out, _ := json.Marshal(planToolResult{Success: true, Action: action, Summary: "no active plan"})
		return string(out), nil
	}
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(planToolResult{Success: true, Action: action, Plan: state, Summary: "plan read"})
	return string(out), nil
}

func (pm *PlanManager) CompleteRunning(ctx context.Context) {
	pm.updateRunningTerminal(ctx, model.PlanItemCompleted, "")
}

func (pm *PlanManager) FailRunning(ctx context.Context, reason string) {
	pm.updateRunningTerminal(ctx, model.PlanItemFailed, reason)
}

func (pm *PlanManager) LinkMessage(ctx context.Context, messageID int64) (*model.PlanState, error) {
	run, err := pm.activeRun(ctx)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	items, err := pm.store.ListPlanItems(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].Status == model.PlanItemRunning {
			items[i].Status = model.PlanItemCompleted
		}
	}
	run.Status = planRunStatus(items)
	run.MessageID = messageID
	if err := pm.store.UpdatePlanRun(ctx, run); err != nil {
		return nil, err
	}
	if err := pm.store.ReplacePlanItems(ctx, run.ID, items); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	state, err := pm.State(ctx)
	pm.emit(ctx)
	return state, err
}

func (pm *PlanManager) LinkErrorMessage(ctx context.Context, messageID int64, reason string) (*model.PlanState, error) {
	run, err := pm.activeRun(ctx)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	items, err := pm.store.ListPlanItems(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	reason = strings.TrimSpace(reason)
	marked := false
	for i := range items {
		if items[i].Status == model.PlanItemRunning {
			items[i].Status = model.PlanItemFailed
			items[i].Reason = reason
			marked = true
			break
		}
	}
	if !marked {
		for i := range items {
			if items[i].Status == model.PlanItemPending {
				items[i].Status = model.PlanItemFailed
				items[i].Reason = reason
				break
			}
		}
	}
	run.MessageID = messageID
	run.Status = model.PlanStatusFailed
	if reason != "" {
		run.RevisionReason = reason
	}
	if err := pm.store.UpdatePlanRun(ctx, run); err != nil {
		return nil, err
	}
	if err := pm.store.ReplacePlanItems(ctx, run.ID, items); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	state, err := pm.State(ctx)
	pm.emit(ctx)
	return state, err
}

func (pm *PlanManager) updateRunningTerminal(ctx context.Context, status model.PlanItemStatus, reason string) {
	run, err := pm.activeRun(ctx)
	if err != nil {
		return
	}
	items, err := pm.store.ListPlanItems(ctx, run.ID)
	if err != nil {
		return
	}
	changed := false
	for i := range items {
		if items[i].Status == model.PlanItemRunning {
			items[i].Status = status
			items[i].Reason = reason
			changed = true
			break
		}
	}
	if !changed {
		return
	}
	ensureOneRunning(items)
	_ = pm.store.ReplacePlanItems(ctx, run.ID, items)
	pm.emit(ctx)
}

func (pm *PlanManager) activeRun(ctx context.Context) (*model.PlanRun, error) {
	pm.mu.Lock()
	if pm.run != nil {
		run := pm.run
		pm.mu.Unlock()
		return run, nil
	}
	pm.mu.Unlock()
	run, err := pm.store.GetActivePlanRun(ctx, pm.conversationID)
	if err != nil {
		return nil, err
	}
	pm.mu.Lock()
	pm.run = run
	pm.mu.Unlock()
	return run, nil
}

func (pm *PlanManager) activeState(ctx context.Context) (*model.PlanState, error) {
	run, err := pm.activeRun(ctx)
	if err != nil {
		return nil, err
	}
	items, err := pm.store.ListPlanItems(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	return &model.PlanState{
		ID:             run.ID,
		UUID:           run.UUID,
		ConversationID: run.ConversationID,
		MessageID:      run.MessageID,
		Goal:           run.Goal,
		Status:         run.Status,
		RevisionReason: run.RevisionReason,
		Items:          items,
		UpdatedAt:      run.UpdatedAt,
	}, nil
}

func (pm *PlanManager) emit(ctx context.Context) {
	pm.mu.Lock()
	fn := pm.onChange
	pm.mu.Unlock()
	if fn == nil {
		return
	}
	state, err := pm.State(ctx)
	if err == nil && state != nil && len(state.Items) > 0 {
		fn(state)
	}
}

func (pm *PlanManager) ok(ctx context.Context, action, summary string) string {
	state, _ := pm.State(ctx)
	out, _ := json.Marshal(planToolResult{Success: true, Action: action, Plan: state, Summary: summary})
	return string(out)
}

func normalizePlanItems(in []planItemArgs) ([]model.PlanItem, error) {
	items := make([]model.PlanItem, 0, len(in))
	seen := make(map[string]bool, len(in))
	for i, p := range in {
		itemKey := strings.TrimSpace(p.ID)
		if itemKey == "" {
			return nil, fmt.Errorf("item id is required")
		}
		if seen[itemKey] {
			return nil, fmt.Errorf("duplicate item id %q", itemKey)
		}
		seen[itemKey] = true
		if strings.TrimSpace(p.Title) == "" {
			return nil, fmt.Errorf("title is required for item %q", itemKey)
		}
		status := model.PlanItemPending
		if p.Status != "" {
			parsed, err := parsePlanItemStatus(p.Status)
			if err != nil {
				return nil, err
			}
			status = parsed
		}
		items = append(items, model.PlanItem{
			ItemKey:   itemKey,
			Title:     strings.TrimSpace(p.Title),
			Detail:    strings.TrimSpace(p.Detail),
			Status:    status,
			Reason:    strings.TrimSpace(p.Reason),
			SortOrder: i + 1,
		})
	}
	return items, nil
}

func parsePlanItemStatus(s string) (model.PlanItemStatus, error) {
	s = strings.TrimSpace(s)
	switch model.PlanItemStatus(s) {
	case model.PlanItemPending, model.PlanItemRunning, model.PlanItemCompleted, model.PlanItemBlocked, model.PlanItemFailed, model.PlanItemSkipped:
		return model.PlanItemStatus(s), nil
	default:
		return "", fmt.Errorf("invalid status %q", s)
	}
}

func ensureOneRunning(items []model.PlanItem) {
	running := -1
	for i := range items {
		if items[i].Status == model.PlanItemRunning {
			if running == -1 {
				running = i
			} else {
				items[i].Status = model.PlanItemPending
			}
		}
	}
	if running != -1 {
		return
	}
	for i := range items {
		if items[i].Status == model.PlanItemPending {
			items[i].Status = model.PlanItemRunning
			return
		}
	}
}

func planRunStatus(items []model.PlanItem) model.PlanStatus {
	if len(items) == 0 {
		return model.PlanStatusCompleted
	}
	allDone := true
	for _, item := range items {
		switch item.Status {
		case model.PlanItemFailed, model.PlanItemBlocked:
			return model.PlanStatusFailed
		case model.PlanItemPending, model.PlanItemRunning:
			allDone = false
		}
	}
	if allDone {
		return model.PlanStatusCompleted
	}
	return model.PlanStatusActive
}

func planPromptText(s string, maxRunes int) string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return ""
	}
	runes := []rune(s)
	if maxRunes > 0 && len(runes) > maxRunes {
		s = string(runes[:maxRunes]) + "..."
	}
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
	).Replace(s)
}

func planErr(action, msg string) string {
	out, _ := json.Marshal(planToolResult{Success: false, Action: action, Error: msg})
	return string(out)
}
