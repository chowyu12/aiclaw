package agent

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/chowyu12/aiclaw/internal/i18n"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/tools"
)

// ask_user：干活途中向用户提一个问题，给几个选项，用户选、写或跳过。
//
// 为什么要一个专门的工具而不是让模型在回复里问：在回复里问就得结束这一轮，
// 用户答完再从头来一轮——前面读过的文件、查过的结果都要靠历史重新拼起来，
// 而且模型常常「问完不等」，自己挑一个继续做。这里是在工具调用里等：这一轮
// 停在原地，拿到回答接着干（Codex 0.154 起的 request_user_input 是同一个思路）。
//
// 选项让回答变便宜：用户点一下，而不是打一段字；也让模型把问题想清楚——列得出
// 选项的问题才值得问。任何时候都可以不选、自己写，或者跳过。
//
// 通道会话（微信、企业微信）与「无人值守」档位不注册：那里没有人能点卡片。

const (
	maxAskOptions  = 6
	maxAskQuestion = 500
)

func (s *Session) registerAskTool() error {
	if s.config.ApprovalPolicy == protocol.ApprovalNever {
		return nil
	}
	return s.registry.Register(tools.Tool{
		Name: "ask_user",
		Description: "Ask the user a question and wait for the answer; the turn pauses here and continues once the answer arrives. " +
			"Use it only when **the user has to decide and you cannot tell from context**: what to delete, which approach to take, how broad the scope is, " +
			"or an ambiguous request. Don't ask about things you can look up yourself; ask one question at a time. Offer 2–4 options where possible (the user just clicks one); " +
			"the user may also write their own answer or skip — if they skip, proceed with your best judgment and state your assumptions.",
		Schema: schemaOf(map[string]any{
			"question": map[string]any{"type": "string", "description": "The question, stated clearly in one sentence"},
			"options": map[string]any{
				"type":        "array",
				"description": "Suggested answers, ideally 2–4; omit for an open-ended question",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"label":       map[string]any{"type": "string", "description": "The option itself, short"},
						"description": map[string]any{"type": "string", "description": "What choosing it means (optional)"},
					},
					"required": []string{"label"},
				},
			},
			"multiSelect": map[string]any{"type": "boolean", "description": "true if multiple options can be selected"},
		}, "question"),
		// 只是问，不改任何东西：任何档位都不需要先审批。
		Effect: tools.EffectRead,
		Handler: func(ctx context.Context, raw json.RawMessage, env *tools.Env) (string, error) {
			request, err := parseAsk(raw)
			if err != nil {
				return "", err
			}
			emitter := s.currentEmitter()
			if emitter == nil {
				return "", i18n.E("没有进行中的轮次，无法提问")
			}
			request.SessionID = env.SessionID
			request.TurnID = env.TurnID
			response, err := emitter.RequestUserInput(ctx, request)
			if err != nil {
				return "", err
			}
			return describeAnswer(request, response), nil
		},
	})
}

func parseAsk(raw json.RawMessage) (protocol.UserInputRequestParams, error) {
	var args struct {
		Question    string                     `json:"question"`
		Options     []protocol.UserInputOption `json:"options"`
		MultiSelect bool                       `json:"multiSelect"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return protocol.UserInputRequestParams{}, i18n.E("提问的参数不是合法 JSON 对象")
	}
	question := strings.TrimSpace(args.Question)
	if question == "" {
		return protocol.UserInputRequestParams{}, i18n.E("问题（question）不能为空")
	}
	if len([]rune(question)) > maxAskQuestion {
		question = string([]rune(question)[:maxAskQuestion]) + "…"
	}
	var options []protocol.UserInputOption
	seen := map[string]bool{}
	for _, option := range args.Options {
		label := strings.TrimSpace(option.Label)
		if label == "" || seen[label] {
			continue
		}
		seen[label] = true
		options = append(options, protocol.UserInputOption{Label: label, Description: strings.TrimSpace(option.Description)})
		if len(options) == maxAskOptions {
			break
		}
	}
	return protocol.UserInputRequestParams{
		Question:    question,
		Options:     options,
		MultiSelect: args.MultiSelect && len(options) > 1,
	}, nil
}

// describeAnswer 把回答写成模型读得懂的一句话。
func describeAnswer(request protocol.UserInputRequestParams, response protocol.UserInputResponse) string {
	if response.Skipped {
		return i18n.D("用户跳过了这个问题，没有回答。按你的最佳判断继续，并在回复里说明你做了什么假设。")
	}
	var parts []string
	if len(response.Selected) > 0 {
		// 「用户选了：」也是 scripts/smoke-kernel.ts 认的标记（它按默认的中文跑）。
		parts = append(parts, i18n.D("用户选了：{options}", "options", strings.Join(response.Selected, i18n.D("、"))))
	}
	if text := strings.TrimSpace(response.Text); text != "" {
		parts = append(parts, i18n.D("用户写道：{text}", "text", text))
	}
	if len(parts) == 0 {
		return i18n.D("用户没有给出回答。按你的最佳判断继续，并说明你的假设。")
	}
	return strings.Join(parts, "\n")
}
