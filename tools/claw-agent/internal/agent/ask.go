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
		Description: "向用户提一个问题并等他回答，这一轮停在这里、拿到回答后接着做。" +
			"只在**需要用户拍板、而你无法从上下文判断**时用：要删哪些、用哪个方案、范围多大、" +
			"有歧义的需求。能自己查到的不要问；一次只问一个问题。尽量给 2–4 个选项（用户点一下就行），" +
			"用户也可以不选、自己写，或者跳过——跳过时按你的最佳判断继续，并说明你的假设。",
		Schema: schemaOf(map[string]any{
			"question": map[string]any{"type": "string", "description": "要问的问题，一句话说清楚"},
			"options": map[string]any{
				"type":        "array",
				"description": "可选的回答，2–4 个为宜；不给就是开放式问题",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"label":       map[string]any{"type": "string", "description": "选项本身，简短"},
						"description": map[string]any{"type": "string", "description": "选了它意味着什么（可选）"},
					},
					"required": []string{"label"},
				},
			},
			"multiSelect": map[string]any{"type": "boolean", "description": "可以选多个时为 true"},
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
				return "", errors.New("没有进行中的轮次，无法提问")
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
		return protocol.UserInputRequestParams{}, errors.New("参数不是合法 JSON 对象")
	}
	question := strings.TrimSpace(args.Question)
	if question == "" {
		return protocol.UserInputRequestParams{}, errors.New("question 不能为空")
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
		return "用户跳过了这个问题，没有回答。按你的最佳判断继续，并在回复里说明你做了什么假设。"
	}
	var parts []string
	if len(response.Selected) > 0 {
		parts = append(parts, fmt.Sprintf("用户选了：%s", strings.Join(response.Selected, "、")))
	}
	if text := strings.TrimSpace(response.Text); text != "" {
		parts = append(parts, fmt.Sprintf("用户写道：%s", text))
	}
	if len(parts) == 0 {
		return "用户没有给出回答。按你的最佳判断继续，并说明你的假设。"
	}
	return strings.Join(parts, "\n")
}
