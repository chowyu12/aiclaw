package agent

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/chowyu12/aiclaw/internal/i18n"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/tools"
)

// 定时任务：让模型替用户排「每天 9 点汇总邮件」「周五下午提醒我写周报」这类事。
//
// 调度器在宿主（桌面端）：到点时它用**当前的**应用配置开一个新会话，把任务的提示词
// 当作用户消息发出去——模型、MCP、技能都按那时的设置来，不用在任务里存一份会过期的配置。
// 应用没开着时不会跑；错过 12 小时以内的，下次打开时补跑一次。
//
// 审批：建、删每次都问——它是一条会在用户不在场时自己跑起来的指令，得让用户亲眼看一遍
// 它要做什么、什么时候做。列出来不问。

func (s *Session) registerScheduleTools() error {
	if !s.config.EnableSchedule {
		return nil
	}
	ruleSchema := map[string]any{
		"mode":                map[string]any{"type": "string", "enum": []string{"followup", "cron"}, "description": "Default followup continues this chat; cron opens a new chat each run"},
		"notification_policy": map[string]any{"type": "string", "enum": []string{"changes", "all"}, "description": "Followup defaults to notifying only changed results, errors, or completion"},
		"stop_when":           map[string]any{"type": "string", "description": "User-requested completion condition. Leave empty for ongoing monitoring"},
		"name":                map[string]any{"type": "string", "description": "Task name, kept short, e.g. \"Daily email digest\""},
		"prompt":              map[string]any{"type": "string", "description": "The complete instruction to run when the task fires, treated as a message the user sends at that time. Spell out what to do and how to deliver the result (e.g. \"Summarize today's unread emails and list the ones I need to reply to\"). Followup keeps the current chat context; cron runs in a new chat"},
		"kind": map[string]any{
			"type": "string", "enum": []string{"daily", "weekdays", "weekly", "interval", "once"},
			"description": "daily: every day; weekdays: every weekday (Monday to Friday); weekly: on chosen days each week; interval: every N minutes; once: run a single time",
		},
		"time":          map[string]any{"type": "string", "description": "For daily / weekdays / weekly: time of day, 24-hour HH:MM, in the user's local time"},
		"days":          map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "For weekly: days of the week, 0 = Sunday, 1 = Monday … 6 = Saturday"},
		"every_minutes": map[string]any{"type": "integer", "description": "For interval: number of minutes between runs, at least 5"},
		"at":            map[string]any{"type": "string", "description": "For once: when to run, ISO 8601 with a time zone (e.g. 2026-10-01T09:00:00+08:00)"},
		"use_workspace": map[string]any{"type": "boolean", "description": "Whether the task runs in the current chat's workspace (set true if it needs to read or write files there). Default true"},
	}
	register := func(tool tools.Tool) error { return s.registry.Register(tool) }

	if err := register(tools.Tool{
		Name: "schedule_create",
		Description: "Create a scheduled task. Default followup continues the current chat; mode cron opens a new chat on each run. " +
			"Use it when the user says things like \"every day\", \"every week\", \"remind me\", \"on a schedule\" or \"in an hour\". Tasks only run while the app is open; " +
			"the user is asked to confirm before the task is created. Times are in the user's local time zone.",
		Schema: schemaOf(ruleSchema, "name", "prompt", "kind"),
		Effect: tools.EffectExternal,
		Handler: func(ctx context.Context, raw json.RawMessage, env *tools.Env) (string, error) {
			var args struct {
				Mode               string `json:"mode"`
				NotificationPolicy string `json:"notification_policy"`
				StopWhen           string `json:"stop_when"`
				Name               string `json:"name"`
				Prompt             string `json:"prompt"`
				Kind               string `json:"kind"`
				Time               string `json:"time"`
				Days               []int  `json:"days"`
				EveryMinutes       int    `json:"every_minutes"`
				At                 string `json:"at"`
				UseWorkspace       *bool  `json:"use_workspace"`
			}
			if err := decodeEmailArgs(raw, &args); err != nil {
				return "", err
			}
			if strings.TrimSpace(args.Name) == "" || strings.TrimSpace(args.Prompt) == "" {
				return "", i18n.E("name 与 prompt 都要给")
			}
			task := &protocol.ScheduleTaskInput{
				Mode: args.Mode, NotificationPolicy: args.NotificationPolicy, StopWhen: args.StopWhen,
				Name: strings.TrimSpace(args.Name), Prompt: strings.TrimSpace(args.Prompt), Kind: args.Kind,
				Time: args.Time, Days: args.Days, EveryMinutes: args.EveryMinutes, At: args.At,
			}
			if task.Mode == "" {
				task.Mode = "followup"
			}
			if args.UseWorkspace == nil || *args.UseWorkspace {
				task.Workspace = env.Workspace
			}
			detail := i18n.D("名称：{value}", "value", task.Name) + "\n" +
				i18n.D("运行时间：{value}", "value", describeTaskTime(task)) + "\n" +
				i18n.D("工作区：{value}", "value", orDash(task.Workspace)) + "\n\n" +
				i18n.D("到点时执行：") + "\n" + task.Prompt + "\nMode: " + task.Mode + "\nStop when: " + task.StopWhen
			if err := env.RequestApproval(ctx, tools.EffectExternal, protocol.ApprovalTool, i18n.D("新建定时任务"), detail,
				i18n.D("到点时会在你不在场的情况下自动执行这条指令（执行中遇到要确认的操作仍会问你）")); err != nil {
				return "", err
			}
			return s.requestSchedule(ctx, env, protocol.ScheduleRequestParams{Action: "create", Task: task})
		},
	}); err != nil {
		return err
	}

	if err := register(tools.Tool{
		Name:        "schedule_list",
		Description: "List the user's scheduled tasks: id, name, schedule, next run and last result.",
		Schema:      emptySchema(),
		Effect:      tools.EffectRead,
		Handler: func(ctx context.Context, _ json.RawMessage, env *tools.Env) (string, error) {
			return s.requestSchedule(ctx, env, protocol.ScheduleRequestParams{Action: "list"})
		},
	}); err != nil {
		return err
	}

	if err := register(tools.Tool{
		Name: "schedule_report", Effect: tools.EffectRead,
		Description: "Report a scheduled follow-up check for the current run ONLY. Use task id and run_id supplied in its instruction. result_key must be a stable description of observed state, without timestamps or wording changes. Set complete only after verifying its explicit stop condition. The host compares state across runs and sends notifications only when appropriate.",
		Schema:      schemaOf(map[string]any{"id": map[string]any{"type": "string"}, "run_id": map[string]any{"type": "string"}, "result": map[string]any{"type": "string"}, "result_key": map[string]any{"type": "string"}, "complete": map[string]any{"type": "boolean"}}, "id", "run_id", "result", "result_key"),
		Handler: func(ctx context.Context, raw json.RawMessage, env *tools.Env) (string, error) {
			var args struct {
				ID        string `json:"id"`
				RunID     string `json:"run_id"`
				Result    string `json:"result"`
				ResultKey string `json:"result_key"`
				Complete  bool   `json:"complete"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", err
			}
			return s.requestSchedule(ctx, env, protocol.ScheduleRequestParams{Action: "report", ID: args.ID, RunID: args.RunID, Result: args.Result, ResultKey: args.ResultKey, Complete: args.Complete})
		},
	}); err != nil {
		return err
	}
	return register(tools.Tool{
		Name:        "schedule_delete",
		Description: "Delete a scheduled task (id from schedule_list). The user is asked to confirm before it is deleted.",
		Schema: schemaOf(map[string]any{
			"id": map[string]any{"type": "string", "description": "Task id from schedule_list"},
		}, "id"),
		Effect: tools.EffectExternal,
		Handler: func(ctx context.Context, raw json.RawMessage, env *tools.Env) (string, error) {
			var args struct {
				ID string `json:"id"`
			}
			if err := decodeEmailArgs(raw, &args); err != nil {
				return "", err
			}
			if strings.TrimSpace(args.ID) == "" {
				return "", i18n.E("必须给出定时任务的 id")
			}
			if err := env.RequestApproval(ctx, tools.EffectExternal, protocol.ApprovalTool, i18n.D("删除定时任务"), args.ID, ""); err != nil {
				return "", err
			}
			return s.requestSchedule(ctx, env, protocol.ScheduleRequestParams{Action: "delete", ID: strings.TrimSpace(args.ID)})
		},
	})
}

func (s *Session) requestSchedule(ctx context.Context, env *tools.Env, params protocol.ScheduleRequestParams) (string, error) {
	emitter := s.currentEmitter()
	if emitter == nil {
		return "", i18n.E("没有进行中的轮次，无法管理定时任务")
	}
	params.SessionID = env.SessionID
	result, err := emitter.RequestSchedule(ctx, params)
	if err != nil {
		return "", err
	}
	return result.Text, nil
}

// describeTaskTime 给审批框一句人话。精确的写法由宿主决定，这里只求用户一眼看懂。
func describeTaskTime(task *protocol.ScheduleTaskInput) string {
	names, separator := []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}, "、"
	if i18n.Default() == i18n.English {
		names, separator = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}, ", "
	}
	switch task.Kind {
	case "daily":
		return i18n.D("每天 {time}", "time", task.Time)
	case "weekdays":
		return i18n.D("每个工作日 {time}", "time", task.Time)
	case "weekly":
		var days []string
		for _, day := range task.Days {
			if day >= 0 && day < len(names) {
				days = append(days, names[day])
			}
		}
		return i18n.D("每{days} {time}", "days", strings.Join(days, separator), "time", task.Time)
	case "interval":
		return i18n.D("每 {minutes} 分钟", "minutes", task.EveryMinutes)
	case "once":
		return i18n.D("{when}（一次）", "when", task.At)
	}
	return task.Kind
}
