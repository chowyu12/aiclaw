package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chowyu12/aiclaw/internal/i18n"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/tools"
)

// computer use：让模型看见并操作整个屏幕。
//
// **这是本应用里权限最大的一组工具**，比 run_command 还大：
//   - run_command 至少还受工作目录约束（路径解析收敛在里面）；
//   - 点击和按键不受任何目录约束，它能碰到的是这台电脑上**当前显示的一切**，
//     包括别的应用、系统设置、以及本应用自己的审批弹窗。
//
// 最后那一条是真问题：如果模型能点自己的审批弹窗上的「允许」，审批这道闸就
// 形同虚设——而没有沙箱之后，审批是仅剩的闸。所以：
//
//  1. 默认关闭，要用户在配置里明确打开；
//  2. 每个动作都走审批（Effect 是 exec，on-write 档位下也会问）；
//  3. 宿主在真正执行输入前会检查最前面的应用是不是本应用，是就拒绝——
//     computer use 是用来驱动**别的**应用的，不是用来操作自己的。
//     这一条在宿主侧实现，因为只有它知道自己是谁。
//
// 截屏与输入都委托给宿主：截屏要走 Electron 的屏幕录制授权，输入要按平台
// 合成事件，两样都不是内核该自己做的。内核只负责把工具调用翻译成请求。

// registerComputerTools 挂上 computer use 的工具。
//
// 在建会话时注册（系统提示词要列出工具名），但回调宿主要用的 emitter 是每轮
// 传进来的，所以 handler 里取当轮的那个。没有进行中的轮次时不该有工具在跑。
func (s *Session) registerComputerTools() error {
	if !s.config.EnableComputerUse {
		return nil
	}

	specs := []struct {
		name        string
		description string
		schema      json.RawMessage
		build       func(json.RawMessage) (protocol.ComputerRequestParams, error)
	}{
		{
			name: "computer_screenshot",
			description: "Take a screenshot of the current screen and show it to you. Take one before acting: you don't know what is on screen right now, " +
				"and clicking coordinates from memory will almost certainly miss. The result includes the screen's logical size; convert coordinates using it.",
			schema: emptySchema(),
			build: func(json.RawMessage) (protocol.ComputerRequestParams, error) {
				return protocol.ComputerRequestParams{Action: protocol.ComputerScreenshot}, nil
			},
		},
		{
			name:        "computer_click",
			description: "Left-click at screen coordinates. The origin is the top-left corner; units are logical pixels.",
			schema:      pointSchema(),
			build:       pointAction(protocol.ComputerClick),
		},
		{
			name:        "computer_double_click",
			description: "Double-click at screen coordinates.",
			schema:      pointSchema(),
			build:       pointAction(protocol.ComputerDoubleClick),
		},
		{
			name:        "computer_right_click",
			description: "Right-click at screen coordinates.",
			schema:      pointSchema(),
			build:       pointAction(protocol.ComputerRightClick),
		},
		{
			name:        "computer_move",
			description: "Move the mouse to screen coordinates without clicking, e.g. to trigger a hover.",
			schema:      pointSchema(),
			build:       pointAction(protocol.ComputerMove),
		},
		{
			name:        "computer_type",
			description: "Type text at the current focus. Make sure the focus is in the input you want first — this tool won't click for you.",
			schema: schemaOf(map[string]any{
				"text": map[string]any{"type": "string", "description": "Text to type"},
			}, "text"),
			build: func(raw json.RawMessage) (protocol.ComputerRequestParams, error) {
				var args struct {
					Text string `json:"text"`
				}
				if err := json.Unmarshal(raw, &args); err != nil {
					return protocol.ComputerRequestParams{}, i18n.E("参数不是合法 JSON 对象")
				}
				if args.Text == "" {
					return protocol.ComputerRequestParams{}, i18n.E("text 不能为空")
				}
				return protocol.ComputerRequestParams{Action: protocol.ComputerType, Text: args.Text}, nil
			},
		},
		{
			name: "computer_key",
			description: "Press a key or key combination, e.g. Return, Escape, Tab, cmd+s, ctrl+c. " +
				"Join combinations with a plus sign.",
			schema: schemaOf(map[string]any{
				"keys": map[string]any{"type": "string", "description": "Key combination, e.g. cmd+s"},
			}, "keys"),
			build: func(raw json.RawMessage) (protocol.ComputerRequestParams, error) {
				var args struct {
					Keys string `json:"keys"`
				}
				if err := json.Unmarshal(raw, &args); err != nil {
					return protocol.ComputerRequestParams{}, i18n.E("参数不是合法 JSON 对象")
				}
				if strings.TrimSpace(args.Keys) == "" {
					return protocol.ComputerRequestParams{}, i18n.E("keys 不能为空")
				}
				return protocol.ComputerRequestParams{Action: protocol.ComputerKey, Keys: args.Keys}, nil
			},
		},
		{
			name:        "computer_scroll",
			description: "Scroll at screen coordinates. Positive dy scrolls down; positive dx scrolls right.",
			schema: schemaOf(map[string]any{
				"x":  map[string]any{"type": "integer", "description": "X coordinate"},
				"y":  map[string]any{"type": "integer", "description": "Y coordinate"},
				"dx": map[string]any{"type": "integer", "description": "Horizontal scroll amount; positive scrolls right"},
				"dy": map[string]any{"type": "integer", "description": "Vertical scroll amount; positive scrolls down"},
			}, "x", "y"),
			build: func(raw json.RawMessage) (protocol.ComputerRequestParams, error) {
				var args struct {
					X, Y, DX, DY int
				}
				if err := json.Unmarshal(raw, &args); err != nil {
					return protocol.ComputerRequestParams{}, i18n.E("参数不是合法 JSON 对象")
				}
				return protocol.ComputerRequestParams{
					Action: protocol.ComputerScroll, X: args.X, Y: args.Y, DX: args.DX, DY: args.DY,
				}, nil
			},
		},
	}

	for _, spec := range specs {
		build := spec.build
		name := spec.name
		if err := s.registry.Register(tools.Tool{
			Name:        name,
			Description: spec.description,
			Schema:      spec.schema,
			// exec 级：on-write 档位下每个动作都问。点一下鼠标可能是「点开设置」，
			// 也可能是「确认转账」，从坐标上看不出区别。
			Effect: tools.EffectExec,
			Handler: func(ctx context.Context, args json.RawMessage, env *tools.Env) (string, error) {
				request, err := build(args)
				if err != nil {
					return "", err
				}
				request.SessionID = env.SessionID
				request.TurnID = env.TurnID
				emitter := s.currentEmitter()
				if emitter == nil {
					return "", i18n.E("没有进行中的轮次，无法执行屏幕操作")
				}
				return s.runComputerAction(ctx, env, request, emitter)
			},
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) runComputerAction(
	ctx context.Context,
	env *tools.Env,
	request protocol.ComputerRequestParams,
	emitter Emitter,
) (string, error) {
	// 截屏只是看，不改变任何东西，按只读处理；其余都要问。
	effect := tools.EffectExec
	if request.Action == protocol.ComputerScreenshot {
		effect = tools.EffectRead
	}
	if err := env.RequestApproval(
		ctx, effect, protocol.ApprovalExec,
		i18n.D("屏幕操作 {action}", "action", string(request.Action)), describeComputerAction(request),
		i18n.D("这个动作作用于整个屏幕，不受工作目录限制"),
	); err != nil {
		return "", err
	}

	result, err := emitter.RequestComputer(ctx, request)
	if err != nil {
		return "", err
	}
	if result.ImageBase64 != "" {
		image, err := base64.StdEncoding.DecodeString(result.ImageBase64)
		if err != nil {
			return "", fmt.Errorf("%s: %w", i18n.D("截屏数据损坏"), err)
		}
		// 图不能放进工具结果——Chat Completions 的 tool 消息必须是纯字符串。
		// 挂到附件上，由轮次循环在工具结果之后补一条带图的 user 消息。
		env.Attach(image)
	}
	if result.Text == "" {
		return i18n.D("已执行。"), nil
	}
	return result.Text, nil
}

func describeComputerAction(request protocol.ComputerRequestParams) string {
	switch request.Action {
	case protocol.ComputerScreenshot:
		return i18n.D("截取整个屏幕")
	case protocol.ComputerType:
		return i18n.D("输入文本：{text}", "text", request.Text)
	case protocol.ComputerKey:
		return i18n.D("按键：{keys}", "keys", request.Keys)
	case protocol.ComputerScroll:
		return i18n.D("在 ({x}, {y}) 滚动 dx={dx} dy={dy}", "x", request.X, "y", request.Y, "dx", request.DX, "dy", request.DY)
	default:
		return i18n.D("在屏幕坐标 ({x}, {y})", "x", request.X, "y", request.Y)
	}
}

func pointAction(
	action protocol.ComputerAction,
) func(json.RawMessage) (protocol.ComputerRequestParams, error) {
	return func(raw json.RawMessage) (protocol.ComputerRequestParams, error) {
		var args struct {
			X *int `json:"x"`
			Y *int `json:"y"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return protocol.ComputerRequestParams{}, i18n.E("参数不是合法 JSON 对象")
		}
		// 坐标用指针判断有没有给：0 是合法坐标（左上角），
		// 用零值判断会把「没给」和「给了 0」混在一起。
		if args.X == nil || args.Y == nil {
			return protocol.ComputerRequestParams{}, i18n.E("必须同时给出 x 与 y")
		}
		if *args.X < 0 || *args.Y < 0 {
			return protocol.ComputerRequestParams{}, i18n.E("坐标不能为负")
		}
		return protocol.ComputerRequestParams{Action: action, X: *args.X, Y: *args.Y}, nil
	}
}

func pointSchema() json.RawMessage {
	return schemaOf(map[string]any{
		"x": map[string]any{"type": "integer", "description": "X coordinate; origin at the top-left of the screen"},
		"y": map[string]any{"type": "integer", "description": "Y coordinate; origin at the top-left of the screen"},
	}, "x", "y")
}

func emptySchema() json.RawMessage {
	return schemaOf(map[string]any{})
}

func schemaOf(properties map[string]any, required ...string) json.RawMessage {
	body := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		body["required"] = required
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		panic(fmt.Sprintf("构造 computer schema 失败：%v", err))
	}
	return encoded
}
