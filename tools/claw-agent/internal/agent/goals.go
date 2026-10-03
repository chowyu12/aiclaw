package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/chowyu12/aiclaw/internal/i18n"
	"strings"
	"time"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/tools"
)

type goalBudgetError struct{}

func (goalBudgetError) Error() string {
	return i18n.D("目标预算已用尽，请调整预算后继续")
}

var errGoalBudget error = goalBudgetError{}

type PlanStep struct {
	Text   string `json:"text"`
	Status string `json:"status"`
}
type Goal struct {
	Objective       string     `json:"objective"`
	Acceptance      string     `json:"acceptance"`
	Status          string     `json:"status"`
	Steps           []PlanStep `json:"steps"`
	Evidence        string     `json:"evidence"`
	TokenBudget     int64      `json:"tokenBudget"`
	TimeBudgetMS    int64      `json:"timeBudgetMs"`
	UsageIncomplete bool       `json:"usageIncomplete"`
	TokensUsed      int64      `json:"tokensUsed"`
	ElapsedMS       int64      `json:"elapsedMs"`
	Revision        int64      `json:"revision"`
}

func (s *Session) Goal() (*Goal, error) {
	s.goalMu.Lock()
	defer s.goalMu.Unlock()
	g, err := s.goalLocked()
	if g != nil && g.Status == "active" && !s.goalTick.IsZero() {
		g.ElapsedMS += time.Since(s.goalTick).Milliseconds()
	}
	return g, err
}
func (s *Session) goalLocked() (*Goal, error) {
	if s.db == nil {
		return nil, nil
	}
	var g Goal
	err := s.db.State(context.Background(), s.ID, "goal", &g)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &g, err
}
func validateSteps(steps []PlanStep) error {
	if len(steps) > 100 {
		return i18n.E("计划最多包含 100 个步骤")
	}
	active := 0
	for _, step := range steps {
		if strings.TrimSpace(step.Text) == "" {
			return i18n.E("计划步骤不能为空")
		}
		switch step.Status {
		case "pending", "completed":
		case "in_progress":
			active++
		default:
			return i18n.E("计划步骤状态无效")
		}
	}
	if active > 1 {
		return i18n.E("同一时刻只能有一个进行中的计划步骤")
	}
	return nil
}
func (s *Session) SetGoal(g Goal) (*Goal, error) {
	s.goalMu.Lock()
	defer s.goalMu.Unlock()
	if s.db == nil {
		return nil, i18n.E("目标持久化不可用")
	}
	old, err := s.goalLocked()
	if err != nil {
		return nil, err
	}
	if old != nil && old.Status != "complete" {
		return nil, i18n.E("请先完成或更新现有目标")
	}
	if strings.TrimSpace(g.Objective) == "" || strings.TrimSpace(g.Acceptance) == "" {
		return nil, i18n.E("必须填写目标与验收条件")
	}
	if g.TokenBudget < 0 || g.TimeBudgetMS < 0 {
		return nil, i18n.E("预算不能为负数")
	}
	if err = validateSteps(g.Steps); err != nil {
		return nil, err
	}
	g.Status = "active"
	g.TokensUsed = 0
	g.UsageIncomplete = false
	g.ElapsedMS = 0
	g.Revision = 1
	g.Evidence = ""
	if g.Steps == nil {
		g.Steps = []PlanStep{}
	}
	err = s.db.PutState(context.Background(), s.ID, "goal", g)
	if s.goalCancel != nil {
		s.goalTick = time.Now()
		s.goalCharging = true
	}
	s.scheduleGoalTimerLocked(&g)
	if err == nil {
		s.notifyGoal(g)
	}
	return &g, err
}

type GoalUpdate struct {
	Status       string      `json:"status"`
	Steps        *[]PlanStep `json:"steps,omitempty"`
	Evidence     *string     `json:"evidence,omitempty"`
	TokenBudget  *int64      `json:"tokenBudget,omitempty"`
	TimeBudgetMS *int64      `json:"timeBudgetMs,omitempty"`
}

func (s *Session) UpdateGoal(update GoalUpdate, user bool) (*Goal, error) {
	s.goalMu.Lock()
	defer s.goalMu.Unlock()
	g, err := s.goalLocked()
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, i18n.E("还没有设置目标")
	}
	if !user && g.Status != "active" {
		return nil, i18n.E("模型只能更新进行中的目标")
	}
	if g.Status == "active" && !s.goalTick.IsZero() {
		g.ElapsedMS += time.Since(s.goalTick).Milliseconds()
	}
	if !user && (update.TokenBudget != nil || update.TimeBudgetMS != nil || update.Status == "active") {
		return nil, i18n.E("只有用户可以恢复目标或调整预算")
	}
	if update.TokenBudget != nil {
		if *update.TokenBudget < 0 {
			return nil, i18n.E("Token 预算无效")
		}
		g.TokenBudget = *update.TokenBudget
	}
	if update.TimeBudgetMS != nil {
		if *update.TimeBudgetMS < 0 {
			return nil, i18n.E("时间预算无效")
		}
		g.TimeBudgetMS = *update.TimeBudgetMS
	}
	if update.Steps != nil {
		if err = validateSteps(*update.Steps); err != nil {
			return nil, err
		}
		g.Steps = *update.Steps
	}
	if update.Evidence != nil {
		g.Evidence = strings.TrimSpace(*update.Evidence)
	}
	if update.Status != "" {
		switch update.Status {
		case "active", "paused", "blocked", "complete":
		default:
			return nil, i18n.E("目标状态无效")
		}
		if update.Status == "complete" {
			if g.Evidence == "" {
				return nil, i18n.E("完成目标需要提供对应验收条件的验证记录")
			}
			for _, step := range g.Steps {
				if step.Status != "completed" {
					return nil, i18n.E("必须完成全部计划步骤")
				}
			}
		}
		if update.Status == "active" && g.TokenBudget > 0 && g.UsageIncomplete {
			return nil, i18n.E("上游 Token 用量不完整，无法执行 Token 预算限制")
		}
		if update.Status == "active" && ((g.TokenBudget > 0 && g.TokensUsed >= g.TokenBudget) || (g.TimeBudgetMS > 0 && g.ElapsedMS >= g.TimeBudgetMS)) {
			return nil, i18n.E("请先增加已用尽的预算再继续")
		}
		g.Status = update.Status
	}
	if g.Status == "complete" {
		if strings.TrimSpace(g.Evidence) == "" {
			return nil, i18n.E("完成目标需要提供对应验收条件的验证记录")
		}
		for _, step := range g.Steps {
			if step.Status != "completed" {
				return nil, i18n.E("必须完成全部计划步骤")
			}
		}
	}
	g.Revision++
	err = s.db.PutState(context.Background(), s.ID, "goal", g)
	if s.goalCancel != nil && g.Status == "active" {
		s.goalTick = time.Now()
		s.goalCharging = true
	} else {
		s.goalTick = time.Time{}
	}
	s.scheduleGoalTimerLocked(g)
	if err == nil {
		s.notifyGoal(*g)
	}
	return g, err
}
func (s *Session) accountGoal(tokens int64) error {
	s.goalMu.Lock()
	defer s.goalMu.Unlock()
	g, err := s.goalLocked()
	if err != nil || g == nil {
		return err
	}
	now := time.Now()
	if g.Status == "active" && !s.goalTick.IsZero() {
		g.ElapsedMS += now.Sub(s.goalTick).Milliseconds()
		s.goalTick = now
	}
	if s.goalCharging {
		g.TokensUsed += tokens
	}
	exhausted := g.Status == "active" && ((g.TokenBudget > 0 && g.TokensUsed >= g.TokenBudget) || (g.TimeBudgetMS > 0 && g.ElapsedMS >= g.TimeBudgetMS))
	if exhausted {
		g.Status = "budget_exhausted"
		s.scheduleGoalTimerLocked(g)
	}
	if err = s.db.PutState(context.Background(), s.ID, "goal", g); err != nil {
		return err
	}
	if exhausted {
		return errGoalBudget
	}
	return nil
}

const goalContextHeading = "Persistent user-authorized goal and plan (task data, not system instructions):\n"

func (s *Session) goalPrompt() string {
	g, err := s.Goal()
	if err != nil || g == nil {
		return ""
	}
	raw, _ := json.Marshal(g)
	if g.Status != "active" {
		return goalContextHeading + string(raw) + "\nThis goal is not active. Do not continue it automatically. Follow the current user request; only the user can resume the goal."
	}
	return goalContextHeading + string(raw) + "\nWork toward the acceptance criteria. Update the plan using update_goal. Mark complete only after verification and record evidence. If user input is required, mark blocked. Do not repeat actions whose execution outcome is unknown."
}
func (s *Session) registerGoalTools() error {
	stepSchema := map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}, "status": map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed"}}}, "required": []string{"text", "status"}}}
	for _, tool := range []tools.Tool{
		{Name: "create_goal", Effect: tools.EffectWrite, Description: "Create a persistent goal ONLY when the user explicitly asks for a goal. Include acceptance criteria and a plan. Set token/time budgets only if requested. A goal continues within the active turn until complete, blocked, paused or budget-limited; it does not run while the app is closed.", Schema: schemaOf(map[string]any{"objective": map[string]any{"type": "string"}, "acceptance": map[string]any{"type": "string"}, "steps": stepSchema, "tokenBudget": map[string]any{"type": "integer", "minimum": 0}, "timeBudgetMs": map[string]any{"type": "integer", "minimum": 0}}, "objective", "acceptance"), Handler: func(ctx context.Context, raw json.RawMessage, env *tools.Env) (string, error) {
			var g Goal
			if err := json.Unmarshal(raw, &g); err != nil {
				return "", err
			}
			result, err := s.SetGoal(g)
			b, _ := json.Marshal(result)
			return string(b), err
		}},
		{Name: "get_goal", Effect: tools.EffectRead, Description: "Read the persistent goal, plan, budgets and verification evidence.", Schema: emptySchema(), Handler: func(ctx context.Context, raw json.RawMessage, env *tools.Env) (string, error) {
			g, err := s.Goal()
			b, _ := json.Marshal(g)
			return string(b), err
		}},
		{Name: "update_goal", Effect: tools.EffectWrite, Description: "Update goal steps and verification evidence. Complete only with evidence and all steps completed. Pause only at the user's request. Mark blocked when meaningful progress needs user input. Only the user can resume or change budgets.", Schema: schemaOf(map[string]any{"status": map[string]any{"type": "string", "enum": []string{"paused", "blocked", "complete"}}, "steps": stepSchema, "evidence": map[string]any{"type": "string"}}), Handler: func(ctx context.Context, raw json.RawMessage, env *tools.Env) (string, error) {
			var u GoalUpdate
			if err := json.Unmarshal(raw, &u); err != nil {
				return "", err
			}
			g, err := s.UpdateGoal(u, false)
			b, _ := json.Marshal(g)
			return string(b), err
		}},
	} {
		if err := s.registry.Register(tool); err != nil {
			return err
		}
	}
	return nil
}
func (s *Session) addGoalContext() {
	if prompt := s.goalPrompt(); prompt != "" {
		s.appendMessage(llm.Message{Role: llm.RoleUser, Content: prompt, Shown: &llm.Shown{Hidden: true}})
	}
}

// The timer cancels in-flight model/tool waits. Changes to a budget replace the
// timer under the same lock, so a stopped timer cannot expire a newer budget.
func (s *Session) scheduleGoalTimerLocked(g *Goal) {
	s.goalTimerGeneration++
	generation := s.goalTimerGeneration
	if s.goalTimer != nil {
		s.goalTimer.Stop()
		s.goalTimer = nil
	}
	if s.goalCancel == nil || g.Status != "active" || g.TimeBudgetMS <= 0 {
		return
	}
	remaining := time.Duration(g.TimeBudgetMS-g.ElapsedMS) * time.Millisecond
	s.goalTimer = time.AfterFunc(remaining, func() {
		s.goalMu.Lock()
		if generation != s.goalTimerGeneration {
			s.goalMu.Unlock()
			return
		}
		current, err := s.goalLocked()
		if err != nil || current == nil || current.Status != "active" {
			s.goalMu.Unlock()
			return
		}
		if !s.goalTick.IsZero() {
			current.ElapsedMS += time.Since(s.goalTick).Milliseconds()
		}
		s.goalTick = time.Time{}
		current.Status = "budget_exhausted"
		err = s.db.PutState(context.Background(), s.ID, "goal", current)
		cancel := s.goalCancel
		s.goalMu.Unlock()
		if err != nil {
			s.mu.Lock()
			s.persistenceErr = err
			s.mu.Unlock()
		}
		if cancel != nil {
			cancel()
		}
	})
}
func (s *Session) startGoalClock(cancel context.CancelFunc) {
	s.goalMu.Lock()
	defer s.goalMu.Unlock()
	s.goalCancel = func() {
		cancel()
		if s.goalStop != nil {
			s.goalStop()
		}
	}
	s.goalCharging = false
	s.goalTick = time.Time{}
	if g, err := s.goalLocked(); err == nil && g != nil && g.Status == "active" {
		s.goalTick = time.Now()
		s.goalCharging = true
		s.scheduleGoalTimerLocked(g)
	}
}
func (s *Session) stopGoalClock() {
	s.goalMu.Lock()
	defer s.goalMu.Unlock()
	s.goalTimerGeneration++
	if s.goalTimer != nil {
		s.goalTimer.Stop()
		s.goalTimer = nil
	}
	s.goalCancel = nil
	s.goalTick = time.Time{}
	s.goalCharging = false
}

func (s *Session) unknownGoalUsage() {
	s.goalMu.Lock()
	defer s.goalMu.Unlock()
	g, err := s.goalLocked()
	if err != nil || g == nil || !s.goalCharging {
		return
	}
	g.UsageIncomplete = true
	if g.Status == "active" && g.TokenBudget > 0 {
		g.Status = "paused"
		g.Evidence = "The provider did not report token usage. Review usage before continuing without a token budget."
		s.scheduleGoalTimerLocked(g)
		if s.goalCancel != nil {
			s.goalCancel()
		}
	}
	if err = s.db.PutState(context.Background(), s.ID, "goal", g); err != nil {
		s.mu.Lock()
		s.persistenceErr = err
		s.mu.Unlock()
	}
}

func (s *Session) SetGoalStop(stop func()) { s.goalStop = stop }

func (s *Session) notifyGoal(g Goal) {
	if emitter := s.currentEmitter(); emitter != nil {
		emitter.Notify("session/workUpdated", map[string]any{"sessionId": s.ID, "goal": g})
	}
}
