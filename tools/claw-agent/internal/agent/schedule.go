package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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
		"name":   map[string]any{"type": "string", "description": "任务名，短一点，比如「每日邮件汇总」"},
		"prompt": map[string]any{"type": "string", "description": "到点时要执行的完整指令，当作用户那时发来的一条消息。要写清楚做什么、结果怎么给（比如「汇总今天的未读邮件，列出要我回复的」），不要依赖当前对话的上下文——那时是一个新会话"},
		"kind": map[string]any{
			"type": "string", "enum": []string{"daily", "weekdays", "weekly", "interval", "once"},
			"description": "daily 每天；weekdays 每个工作日（周一到周五）；weekly 每周指定几天；interval 每隔 N 分钟；once 只跑一次",
		},
		"time":          map[string]any{"type": "string", "description": "daily / weekdays / weekly 用：几点，24 小时制 HH:MM，按用户本机时间"},
		"days":          map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "weekly 用：星期几，0 = 周日，1 = 周一 … 6 = 周六"},
		"every_minutes": map[string]any{"type": "integer", "description": "interval 用：每隔多少分钟，至少 5"},
		"at":            map[string]any{"type": "string", "description": "once 用：什么时候，ISO 8601，带时区（比如 2026-10-01T09:00:00+08:00）"},
		"use_workspace": map[string]any{"type": "boolean", "description": "任务是否在当前会话的工作区里跑（要读写这里的文件时设 true）。默认 true"},
	}
	register := func(tool tools.Tool) error { return s.registry.Register(tool) }

	if err := register(tools.Tool{
		Name: "schedule_create",
		Description: "建一个定时任务：到点时 AIClaw 自动开一个新会话，把 prompt 当作用户的消息执行。" +
			"用户说「每天」「每周」「提醒我」「定时」「过一小时再」这类话时用。应用要开着才会跑；" +
			"建之前会请用户确认。时间按用户本机时区。",
		Schema: schemaOf(ruleSchema, "name", "prompt", "kind"),
		Effect: tools.EffectExternal,
		Handler: func(ctx context.Context, raw json.RawMessage, env *tools.Env) (string, error) {
			var args struct {
				Name         string `json:"name"`
				Prompt       string `json:"prompt"`
				Kind         string `json:"kind"`
				Time         string `json:"time"`
				Days         []int  `json:"days"`
				EveryMinutes int    `json:"every_minutes"`
				At           string `json:"at"`
				UseWorkspace *bool  `json:"use_workspace"`
			}
			if err := decodeEmailArgs(raw, &args); err != nil {
				return "", err
			}
			if strings.TrimSpace(args.Name) == "" || strings.TrimSpace(args.Prompt) == "" {
				return "", errors.New("name 与 prompt 都要给")
			}
			task := &protocol.ScheduleTaskInput{
				Name: strings.TrimSpace(args.Name), Prompt: strings.TrimSpace(args.Prompt), Kind: args.Kind,
				Time: args.Time, Days: args.Days, EveryMinutes: args.EveryMinutes, At: args.At,
			}
			if args.UseWorkspace == nil || *args.UseWorkspace {
				task.Workspace = env.Workspace
			}
			detail := fmt.Sprintf("名称：%s\n时间：%s\n工作区：%s\n\n到点时执行：\n%s",
				task.Name, describeTaskTime(task), orDash(task.Workspace), task.Prompt)
			if err := env.RequestApproval(ctx, tools.EffectExternal, protocol.ApprovalTool, "新建定时任务", detail,
				"到点时会在你不在场的情况下自动执行这条指令（执行中遇到要确认的操作仍会问你）"); err != nil {
				return "", err
			}
			return s.requestSchedule(ctx, env, protocol.ScheduleRequestParams{Action: "create", Task: task})
		},
	}); err != nil {
		return err
	}

	if err := register(tools.Tool{
		Name:        "schedule_list",
		Description: "列出用户的定时任务：编号、名称、时间规则、下次运行、上次结果。",
		Schema:      emptySchema(),
		Effect:      tools.EffectRead,
		Handler: func(ctx context.Context, _ json.RawMessage, env *tools.Env) (string, error) {
			return s.requestSchedule(ctx, env, protocol.ScheduleRequestParams{Action: "list"})
		},
	}); err != nil {
		return err
	}

	return register(tools.Tool{
		Name:        "schedule_delete",
		Description: "删掉一个定时任务（编号来自 schedule_list）。删之前会请用户确认。",
		Schema: schemaOf(map[string]any{
			"id": map[string]any{"type": "string", "description": "schedule_list 给的编号"},
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
				return "", errors.New("必须给出 id")
			}
			if err := env.RequestApproval(ctx, tools.EffectExternal, protocol.ApprovalTool, "删除定时任务", args.ID, ""); err != nil {
				return "", err
			}
			return s.requestSchedule(ctx, env, protocol.ScheduleRequestParams{Action: "delete", ID: strings.TrimSpace(args.ID)})
		},
	})
}

func (s *Session) requestSchedule(ctx context.Context, env *tools.Env, params protocol.ScheduleRequestParams) (string, error) {
	emitter := s.currentEmitter()
	if emitter == nil {
		return "", errors.New("没有进行中的轮次，无法管理定时任务")
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
	names := []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}
	switch task.Kind {
	case "daily":
		return "每天 " + task.Time
	case "weekdays":
		return "每个工作日 " + task.Time
	case "weekly":
		var days []string
		for _, day := range task.Days {
			if day >= 0 && day < len(names) {
				days = append(days, names[day])
			}
		}
		return "每" + strings.Join(days, "、") + " " + task.Time
	case "interval":
		return fmt.Sprintf("每 %d 分钟", task.EveryMinutes)
	case "once":
		return task.At + "（一次）"
	}
	return task.Kind
}
