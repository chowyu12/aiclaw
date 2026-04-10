package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type scheduleArgs struct {
	Action      string `json:"action"`
	Name        string `json:"name"`
	Expression  string `json:"expression"`
	Type        string `json:"type"`
	AgentUUID   string `json:"agent_uuid"`
	Prompt      string `json:"prompt"`
	Command     string `json:"command"`
	JobID       string `json:"job_id"`
	Enabled     *bool  `json:"enabled,omitempty"`
	MaxRuns     int    `json:"max_runs"`
	Description string `json:"description"`
	Limit       int    `json:"limit"`
}

type scheduleResult struct {
	Success bool   `json:"success"`
	Action  string `json:"action"`
	Message string `json:"message,omitempty"`
	Data    any    `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
}

type contextKey struct{}

// WithScheduler 注入调度器到 context。
func WithScheduler(ctx context.Context, s *Scheduler) context.Context {
	return context.WithValue(ctx, contextKey{}, s)
}

// FromContext 从 context 中提取调度器。
func FromContext(ctx context.Context) *Scheduler {
	if s, ok := ctx.Value(contextKey{}).(*Scheduler); ok {
		return s
	}
	return nil
}

// ToolHandler 是 in-process 调度器的工具 handler。
func ToolHandler(ctx context.Context, args string) (string, error) {
	s := FromContext(ctx)
	if s == nil {
		return errJSON("schedule", "scheduler not initialized"), nil
	}

	var p scheduleArgs
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	switch p.Action {
	case "add":
		return handleAdd(ctx, s, p)
	case "list":
		return handleList(s, p)
	case "remove":
		return handleRemove(s, p)
	case "toggle":
		return handleToggle(ctx, s, p)
	case "logs":
		return handleLogs(s, p)
	default:
		return errJSON(p.Action, fmt.Sprintf("unknown action %q, supported: add, list, remove, toggle, logs", p.Action)), nil
	}
}

func handleAdd(ctx context.Context, s *Scheduler, p scheduleArgs) (string, error) {
	if p.Expression == "" {
		return errJSON("add", "expression is required"), nil
	}
	if p.Name == "" {
		p.Name = fmt.Sprintf("task_%d", time.Now().UnixMilli())
	}

	jobType := JobTypePrompt
	if p.Type == "command" {
		jobType = JobTypeCommand
	}

	switch jobType {
	case JobTypePrompt:
		if p.Prompt == "" {
			return errJSON("add", "prompt is required for prompt-type jobs"), nil
		}
	case JobTypeCommand:
		if p.Command == "" {
			return errJSON("add", "command is required for command-type jobs"), nil
		}
	}

	job := &Job{
		Name:        p.Name,
		Expression:  p.Expression,
		Type:        jobType,
		AgentUUID:   p.AgentUUID,
		Prompt:      p.Prompt,
		Command:     p.Command,
		MaxRuns:     p.MaxRuns,
		Description: p.Description,
	}

	if err := s.AddJob(ctx, job); err != nil {
		return errJSON("add", err.Error()), nil
	}

	return okJSON("add", fmt.Sprintf("任务 '%s' 已创建，schedule: %s, 下次执行: %s",
		job.Name, job.Expression, job.NextRunAt.Format(time.DateTime)), job), nil
}

func handleList(s *Scheduler, _ scheduleArgs) (string, error) {
	jobs := s.ListJobs()
	if len(jobs) == 0 {
		return okJSON("list", "没有定时任务。", nil), nil
	}

	type jobView struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Expression  string `json:"expression"`
		Type        string `json:"type"`
		Enabled     bool   `json:"enabled"`
		RunCount    int    `json:"run_count"`
		MaxRuns     int    `json:"max_runs,omitempty"`
		LastRunAt   string `json:"last_run_at,omitempty"`
		NextRunAt   string `json:"next_run_at,omitempty"`
		Description string `json:"description,omitempty"`
	}

	views := make([]jobView, 0, len(jobs))
	for _, j := range jobs {
		v := jobView{
			ID:          j.ID,
			Name:        j.Name,
			Expression:  j.Expression,
			Type:        string(j.Type),
			Enabled:     j.Enabled,
			RunCount:    j.RunCount,
			MaxRuns:     j.MaxRuns,
			Description: j.Description,
		}
		if !j.LastRunAt.IsZero() {
			v.LastRunAt = j.LastRunAt.Format(time.DateTime)
		}
		if !j.NextRunAt.IsZero() {
			v.NextRunAt = j.NextRunAt.Format(time.DateTime)
		}
		views = append(views, v)
	}

	return okJSON("list", fmt.Sprintf("共 %d 个定时任务。", len(views)), views), nil
}

func handleRemove(s *Scheduler, p scheduleArgs) (string, error) {
	if p.JobID == "" {
		return errJSON("remove", "job_id is required"), nil
	}
	if err := s.RemoveJob(p.JobID); err != nil {
		return errJSON("remove", err.Error()), nil
	}
	return okJSON("remove", fmt.Sprintf("任务 %s 已删除。", p.JobID), nil), nil
}

func handleToggle(ctx context.Context, s *Scheduler, p scheduleArgs) (string, error) {
	if p.JobID == "" {
		return errJSON("toggle", "job_id is required"), nil
	}
	if p.Enabled == nil {
		return errJSON("toggle", "enabled is required"), nil
	}
	if err := s.ToggleJob(ctx, p.JobID, *p.Enabled); err != nil {
		return errJSON("toggle", err.Error()), nil
	}
	state := "已启用"
	if !*p.Enabled {
		state = "已禁用"
	}
	return okJSON("toggle", fmt.Sprintf("任务 %s %s。", p.JobID, state), nil), nil
}

func handleLogs(s *Scheduler, p scheduleArgs) (string, error) {
	if p.JobID == "" {
		return errJSON("logs", "job_id is required"), nil
	}
	limit := p.Limit
	if limit <= 0 {
		limit = 10
	}
	records := s.ListLogs(p.JobID, limit)
	if len(records) == 0 {
		return okJSON("logs", "该任务暂无执行记录。", nil), nil
	}

	type logView struct {
		RunAt    string `json:"run_at"`
		Duration string `json:"duration"`
		Status   string `json:"status"`
		Output   string `json:"output,omitempty"`
		Error    string `json:"error,omitempty"`
	}

	views := make([]logView, 0, len(records))
	for _, r := range records {
		output := r.Output
		if len(output) > 500 {
			output = output[:500] + "..."
		}
		views = append(views, logView{
			RunAt:    r.RunAt.Format(time.DateTime),
			Duration: r.Duration,
			Status:   r.Status,
			Output:   output,
			Error:    r.Error,
		})
	}
	return okJSON("logs", fmt.Sprintf("最近 %d 条执行记录。", len(views)), views), nil
}

func okJSON(action, message string, data any) string {
	r := scheduleResult{Success: true, Action: action, Message: message, Data: data}
	out, _ := json.Marshal(r)
	return string(out)
}

func errJSON(action, msg string) string {
	r := scheduleResult{Success: false, Action: action, Error: msg}
	out, _ := json.Marshal(r)
	return string(out)
}

// ParseNaturalInterval 将自然语言间隔描述转换为 cron 表达式。
func ParseNaturalInterval(desc string) (string, error) {
	desc = strings.TrimSpace(strings.ToLower(desc))
	switch {
	case desc == "every minute":
		return "0 * * * * *", nil
	case desc == "every hour":
		return "0 0 * * * *", nil
	case desc == "daily", desc == "every day":
		return "0 0 9 * * *", nil
	case desc == "weekly", desc == "every week":
		return "0 0 9 * * 1", nil
	case desc == "monthly", desc == "every month":
		return "0 0 9 1 * *", nil
	case strings.HasPrefix(desc, "every "):
		return "", fmt.Errorf("cannot parse interval %q, please use standard cron expression", desc)
	default:
		return desc, nil
	}
}
