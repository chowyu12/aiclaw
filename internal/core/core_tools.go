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
	"github.com/chowyu12/aiclaw/internal/skills"
	"github.com/chowyu12/aiclaw/internal/store"
)

type toolTurnContext struct {
	ThreadID   int64
	ThreadUUID string
	TurnID     string
}

type toolTurnContextKey struct{}

func withToolTurn(ctx context.Context, thread model.Thread, turn Turn) context.Context {
	return context.WithValue(ctx, toolTurnContextKey{}, toolTurnContext{ThreadID: thread.ID, ThreadUUID: thread.UUID, TurnID: turn.ID})
}

func toolTurnFromContext(ctx context.Context) toolTurnContext {
	value, _ := ctx.Value(toolTurnContextKey{}).(toolTurnContext)
	return value
}

type subAgentContextKey struct{}

type subAgentTask struct {
	Goal         string   `json:"goal"`
	Context      string   `json:"context,omitempty"`
	BlockedTools []string `json:"blocked_tools,omitempty"`
	Mode         string   `json:"mode,omitempty"`
}

type subAgentTaskResult struct {
	Index      int      `json:"index"`
	Goal       string   `json:"goal"`
	Status     string   `json:"status"`
	Summary    string   `json:"summary,omitempty"`
	Error      string   `json:"error,omitempty"`
	Tools      []string `json:"tools,omitempty"`
	DurationMS int64    `json:"duration_ms"`
}

func (d *LocalToolDispatcher) executeSubAgent(ctx context.Context, thread model.Thread, call ToolCall) (ToolResult, error) {
	if d.sampler == nil {
		return ToolResult{CallID: call.ID, Name: call.Name}, fmt.Errorf("sub-agent sampler is unavailable")
	}
	depth, _ := ctx.Value(subAgentContextKey{}).(int)
	if depth >= 3 {
		return ToolResult{CallID: call.ID, Name: call.Name}, fmt.Errorf("sub-agent depth limit reached")
	}
	var input struct {
		Tasks  []subAgentTask `json:"tasks"`
		Prompt string         `json:"prompt,omitempty"`
	}
	if err := json.Unmarshal([]byte(call.Arguments), &input); err != nil {
		return ToolResult{CallID: call.ID, Name: call.Name}, fmt.Errorf("parse sub_agent arguments: %w", err)
	}
	if len(input.Tasks) == 0 && strings.TrimSpace(input.Prompt) != "" {
		input.Tasks = []subAgentTask{{Goal: strings.TrimSpace(input.Prompt)}}
	}
	if len(input.Tasks) == 0 {
		return ToolResult{CallID: call.ID, Name: call.Name}, fmt.Errorf("at least one sub-agent task is required")
	}
	if len(input.Tasks) > 8 {
		return ToolResult{CallID: call.ID, Name: call.Name}, fmt.Errorf("at most 8 sub-agent tasks are allowed")
	}

	results := make([]subAgentTaskResult, len(input.Tasks))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for index, task := range input.Tasks {
		index, task := index, task
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			started := time.Now()
			result := subAgentTaskResult{Index: index, Goal: task.Goal}
			summary, used, err := d.runSubAgent(context.WithValue(ctx, subAgentContextKey{}, depth+1), thread, task)
			result.DurationMS = time.Since(started).Milliseconds()
			result.Tools = used
			if err != nil {
				result.Status, result.Error = "failed", err.Error()
			} else {
				result.Status, result.Summary = "completed", summary
			}
			results[index] = result
		}()
	}
	wg.Wait()
	encoded, _ := json.Marshal(map[string]any{"success": true, "results": results})
	return ToolResult{CallID: call.ID, Name: call.Name, Content: string(encoded)}, nil
}

func (d *LocalToolDispatcher) runSubAgent(ctx context.Context, thread model.Thread, task subAgentTask) (string, []string, error) {
	goal := strings.TrimSpace(task.Goal)
	if goal == "" {
		return "", nil, fmt.Errorf("sub-agent goal is required")
	}
	blocked := make(map[string]bool, len(task.BlockedTools)+1)
	for _, name := range task.BlockedTools {
		blocked[strings.TrimSpace(name)] = true
	}
	blocked["sub_agent"] = true
	allowed := map[string]bool{}
	switch task.Mode {
	case "explore":
		for _, name := range []string{"read", "grep", "find", "ls", "web_fetch", "session_search"} {
			allowed[name] = true
		}
	case "shell":
		for _, name := range []string{"exec", "process", "read", "ls"} {
			allowed[name] = true
		}
	}
	messages := []SamplingMessage{
		{Role: "system", Content: "You are an isolated AIClaw sub-agent. Complete the delegated goal, use tools when useful, and return a concise evidence-based result."},
		{Role: "user", Content: goal + optionalContext(task.Context)},
	}
	used := make([]string, 0)
	for iteration := 0; iteration < 12; iteration++ {
		registry, err := d.registry(ctx, thread)
		if err != nil {
			return "", used, err
		}
		definitions := make([]ToolDefinition, 0)
		for _, definition := range registry.Definitions() {
			if blocked[definition.Name] || (len(allowed) > 0 && !allowed[definition.Name]) {
				continue
			}
			definitions = append(definitions, definition)
		}
		result, err := d.sampler.Sample(ctx, SamplingRequest{Thread: thread, Messages: messages, Tools: definitions}, nil)
		if err != nil {
			return "", used, err
		}
		if len(result.ToolCalls) == 0 {
			return result.Text, used, nil
		}
		messages = append(messages, SamplingMessage{Role: "assistant", Content: result.Text, ToolCalls: result.ToolCalls})
		for _, nestedCall := range result.ToolCalls {
			if blocked[nestedCall.Name] || (len(allowed) > 0 && !allowed[nestedCall.Name]) {
				messages = append(messages, SamplingMessage{Role: "tool", Name: nestedCall.Name, ToolCallID: nestedCall.ID, Content: "tool is blocked for this sub-agent"})
				continue
			}
			tool, ok := registry.Lookup(nestedCall.Name)
			if !ok {
				messages = append(messages, SamplingMessage{Role: "tool", Name: nestedCall.Name, ToolCallID: nestedCall.ID, Content: "tool is not registered"})
				continue
			}
			used = append(used, nestedCall.Name)
			toolResult, callErr := tool.Handler(ctx, thread, nestedCall)
			content := toolResult.Content
			if callErr != nil {
				content = "tool error: " + callErr.Error()
			}
			messages = append(messages, SamplingMessage{Role: "tool", Name: nestedCall.Name, ToolCallID: nestedCall.ID, Content: content})
		}
	}
	return "", used, fmt.Errorf("sub-agent exceeded tool iteration limit")
}

func optionalContext(value string) string {
	if value = strings.TrimSpace(value); value != "" {
		return "\n\nContext:\n" + value
	}
	return ""
}

type planToolItem struct {
	ID     string               `json:"id"`
	Title  string               `json:"title,omitempty"`
	Detail string               `json:"detail,omitempty"`
	Status model.PlanItemStatus `json:"status,omitempty"`
	Reason string               `json:"reason,omitempty"`
}

func (d *LocalToolDispatcher) executePlan(ctx context.Context, thread model.Thread, call ToolCall) (ToolResult, error) {
	var input struct {
		Action string         `json:"action"`
		Goal   string         `json:"goal,omitempty"`
		Items  []planToolItem `json:"items,omitempty"`
		Reason string         `json:"reason,omitempty"`
	}
	if err := json.Unmarshal([]byte(call.Arguments), &input); err != nil {
		return ToolResult{CallID: call.ID, Name: call.Name}, fmt.Errorf("parse plan arguments: %w", err)
	}
	input.Action = strings.TrimSpace(input.Action)
	d.planMu.Lock()
	defer d.planMu.Unlock()
	current := clonePlan(d.plans[thread.UUID])
	if current == nil {
		current = &model.PlanState{UUID: uuid.NewString(), ConversationID: thread.ID, Source: model.PlanSourceModel, Status: model.PlanStatusActive}
	}
	switch input.Action {
	case "read":
		return planResult(call, current), nil
	case "set", "revise":
		if strings.TrimSpace(input.Goal) == "" || len(input.Items) == 0 {
			return ToolResult{CallID: call.ID, Name: call.Name}, fmt.Errorf("plan %s requires goal and items", input.Action)
		}
		current.Goal = strings.TrimSpace(input.Goal)
		current.RevisionReason = strings.TrimSpace(input.Reason)
		current.Items = make([]model.PlanItem, 0, len(input.Items))
		for index, item := range input.Items {
			if strings.TrimSpace(item.ID) == "" {
				return ToolResult{CallID: call.ID, Name: call.Name}, fmt.Errorf("plan item %d requires id", index)
			}
			status := item.Status
			if status == "" {
				status = model.PlanItemPending
			}
			current.Items = append(current.Items, model.PlanItem{ItemKey: item.ID, Title: item.Title, Detail: item.Detail, Status: status, Reason: item.Reason, SortOrder: index})
		}
	case "update":
		if len(input.Items) == 0 {
			return ToolResult{CallID: call.ID, Name: call.Name}, fmt.Errorf("plan update requires items")
		}
		byID := make(map[string]*model.PlanItem, len(current.Items))
		for index := range current.Items {
			byID[current.Items[index].ItemKey] = &current.Items[index]
		}
		for _, update := range input.Items {
			item := byID[update.ID]
			if item == nil {
				return ToolResult{CallID: call.ID, Name: call.Name}, fmt.Errorf("plan item %q was not found", update.ID)
			}
			if update.Title != "" {
				item.Title = update.Title
			}
			if update.Detail != "" {
				item.Detail = update.Detail
			}
			if update.Status != "" {
				item.Status = update.Status
			}
			if update.Reason != "" {
				item.Reason = update.Reason
			}
		}
		current.RevisionReason = strings.TrimSpace(input.Reason)
	default:
		return ToolResult{CallID: call.ID, Name: call.Name}, fmt.Errorf("unknown plan action %q", input.Action)
	}
	if err := validatePlan(current); err != nil {
		return ToolResult{CallID: call.ID, Name: call.Name}, err
	}
	current.Status = planStatus(current.Items)
	current.UpdatedAt = time.Now()
	d.plans[thread.UUID] = clonePlan(current)
	turn := toolTurnFromContext(ctx)
	if turn.TurnID == "" {
		return ToolResult{CallID: call.ID, Name: call.Name}, fmt.Errorf("plan state is unavailable outside an active turn")
	}
	rollouts, ok := d.store.(ThreadStore)
	if !ok {
		return ToolResult{CallID: call.ID, Name: call.Name}, fmt.Errorf("plan rollout storage is unavailable")
	}
	encoded, _ := json.Marshal(current)
	if _, err := rollouts.AppendRollout(ctx, thread.ID, []model.RolloutItem{{TurnID: turn.TurnID, Kind: model.RolloutPlanUpdated, ModelVisible: true, Payload: model.JSON(encoded)}}); err != nil {
		return ToolResult{CallID: call.ID, Name: call.Name}, err
	}
	return planResult(call, current), nil
}

func validatePlan(plan *model.PlanState) error {
	running := 0
	seen := make(map[string]bool, len(plan.Items))
	for _, item := range plan.Items {
		if seen[item.ItemKey] {
			return fmt.Errorf("duplicate plan item id %q", item.ItemKey)
		}
		seen[item.ItemKey] = true
		if item.Status == model.PlanItemRunning {
			running++
		}
	}
	if running > 1 {
		return fmt.Errorf("a plan may have at most one running item")
	}
	return nil
}

func planStatus(items []model.PlanItem) model.PlanStatus {
	if len(items) == 0 {
		return model.PlanStatusActive
	}
	complete := true
	for _, item := range items {
		if item.Status == model.PlanItemFailed {
			return model.PlanStatusFailed
		}
		if item.Status != model.PlanItemCompleted && item.Status != model.PlanItemSkipped {
			complete = false
		}
	}
	if complete {
		return model.PlanStatusCompleted
	}
	return model.PlanStatusActive
}

func clonePlan(value *model.PlanState) *model.PlanState {
	if value == nil {
		return nil
	}
	encoded, _ := json.Marshal(value)
	var clone model.PlanState
	_ = json.Unmarshal(encoded, &clone)
	return &clone
}

func planResult(call ToolCall, plan *model.PlanState) ToolResult {
	encoded, _ := json.Marshal(map[string]any{"success": true, "plan": plan})
	return ToolResult{CallID: call.ID, Name: call.Name, Content: string(encoded)}
}

func (d *LocalToolDispatcher) executeSkillManager(ctx context.Context, _ model.Thread, call ToolCall) (ToolResult, error) {
	var input struct {
		Action      string `json:"action"`
		FileName    string `json:"file_name,omitempty"`
		Name        string `json:"name,omitempty"`
		Description string `json:"description,omitempty"`
		Limit       int    `json:"limit,omitempty"`
	}
	if err := json.Unmarshal([]byte(call.Arguments), &input); err != nil {
		return ToolResult{CallID: call.ID, Name: call.Name}, err
	}
	skillStore, ok := d.store.(store.SkillStore)
	if !ok {
		return ToolResult{CallID: call.ID, Name: call.Name}, fmt.Errorf("skill storage is unavailable")
	}
	var result any
	switch input.Action {
	case "list_active":
		items, err := skillStore.ListSkills(ctx)
		if err != nil {
			return ToolResult{CallID: call.ID, Name: call.Name}, err
		}
		result = items
	case "list_pending":
		items, err := skills.ListPending(d.root, input.Limit)
		if err != nil {
			return ToolResult{CallID: call.ID, Name: call.Name}, err
		}
		result = items
	case "read_pending":
		content, err := skills.ReadPending(d.root, input.FileName)
		if err != nil {
			return ToolResult{CallID: call.ID, Name: call.Name}, err
		}
		result = content
	case "discard":
		if err := skills.DiscardPending(d.root, input.FileName); err != nil {
			return ToolResult{CallID: call.ID, Name: call.Name}, err
		}
		result = "discarded"
	case "promote":
		dir, info, err := skills.PromotePending(d.root, input.FileName, input.Name, input.Description)
		if err != nil {
			return ToolResult{CallID: call.ID, Name: call.Name}, err
		}
		skill := skills.InfoToSkill(*info, model.SkillSourceLocal, info.Slug)
		skill.InstallDir = dir
		if err := skillStore.UpsertSkill(ctx, skill); err != nil {
			return ToolResult{CallID: call.ID, Name: call.Name}, err
		}
		result = map[string]any{"path": dir, "skill": skill}
	default:
		return ToolResult{CallID: call.ID, Name: call.Name}, fmt.Errorf("unknown skill action %q", input.Action)
	}
	encoded, _ := json.Marshal(map[string]any{"success": true, "action": input.Action, "result": result})
	return ToolResult{CallID: call.ID, Name: call.Name, Content: string(encoded)}, nil
}

type threadHistoryStore interface {
	ListThreads(context.Context, string, bool, int, int) ([]model.Thread, int64, error)
	LoadRollout(context.Context, int64) ([]model.RolloutItem, error)
}

func (d *LocalToolDispatcher) executeSessionSearch(ctx context.Context, thread model.Thread, call ToolCall) (ToolResult, error) {
	var input struct {
		Query string `json:"query,omitempty"`
		Limit int    `json:"limit,omitempty"`
	}
	if err := json.Unmarshal([]byte(call.Arguments), &input); err != nil {
		return ToolResult{CallID: call.ID, Name: call.Name}, err
	}
	if input.Limit <= 0 {
		input.Limit = 5
	}
	if input.Limit > 20 {
		input.Limit = 20
	}
	history, ok := d.store.(threadHistoryStore)
	if !ok {
		return ToolResult{CallID: call.ID, Name: call.Name}, fmt.Errorf("thread history storage is unavailable")
	}
	threads, _, err := history.ListThreads(ctx, thread.UserID, false, 1, 200)
	if err != nil {
		return ToolResult{CallID: call.ID, Name: call.Name}, err
	}
	query := strings.ToLower(strings.TrimSpace(input.Query))
	type hit struct {
		ThreadID  string    `json:"thread_id"`
		Title     string    `json:"title"`
		Role      string    `json:"role,omitempty"`
		Snippet   string    `json:"snippet,omitempty"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	hits := make([]hit, 0, input.Limit)
	for _, candidate := range threads {
		if len(hits) >= input.Limit {
			break
		}
		items, loadErr := history.LoadRollout(ctx, candidate.ID)
		if loadErr != nil {
			continue
		}
		if query == "" {
			preview := firstUserPreview(items)
			hits = append(hits, hit{ThreadID: candidate.UUID, Title: candidate.Title, Snippet: preview, UpdatedAt: candidate.UpdatedAt})
			continue
		}
		for _, item := range items {
			if item.Kind != model.RolloutUserMessage && item.Kind != model.RolloutAssistantFinal {
				continue
			}
			var payload struct {
				Content string `json:"content"`
			}
			_ = json.Unmarshal(item.Payload, &payload)
			if !strings.Contains(strings.ToLower(payload.Content), query) {
				continue
			}
			role := "assistant"
			if item.Kind == model.RolloutUserMessage {
				role = "user"
			}
			hits = append(hits, hit{ThreadID: candidate.UUID, Title: candidate.Title, Role: role, Snippet: textSnippet(payload.Content, query, 180), UpdatedAt: candidate.UpdatedAt})
			break
		}
	}
	encoded, _ := json.Marshal(map[string]any{"success": true, "mode": map[bool]string{true: "recent", false: "search"}[query == ""], "query": input.Query, "count": len(hits), "results": hits})
	return ToolResult{CallID: call.ID, Name: call.Name, Content: string(encoded)}, nil
}

func firstUserPreview(items []model.RolloutItem) string {
	for _, item := range items {
		if item.Kind != model.RolloutUserMessage {
			continue
		}
		var payload struct {
			Content string `json:"content"`
		}
		_ = json.Unmarshal(item.Payload, &payload)
		if payload.Content != "" {
			return textSnippet(payload.Content, "", 120)
		}
	}
	return ""
}

func textSnippet(value, query string, limit int) string {
	value = strings.TrimSpace(value)
	if len([]rune(value)) <= limit {
		return value
	}
	runes := []rune(value)
	start := 0
	if query != "" {
		lower := strings.ToLower(value)
		if byteIndex := strings.Index(lower, query); byteIndex > 0 {
			start = len([]rune(value[:byteIndex])) - limit/3
			if start < 0 {
				start = 0
			}
		}
	}
	end := start + limit
	if end > len(runes) {
		end = len(runes)
		start = end - limit
	}
	prefix, suffix := "", ""
	if start > 0 {
		prefix = "..."
	}
	if end < len(runes) {
		suffix = "..."
	}
	return prefix + string(runes[start:end]) + suffix
}
