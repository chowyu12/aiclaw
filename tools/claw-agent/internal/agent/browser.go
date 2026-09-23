package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/tools"
)

// 浏览器：让模型驾驭一个真正的浏览器窗口——按元素编号点、填、选，而不是按屏幕坐标。
//
// 这组工具对应的是 browser-use 那类框架的核心：把页面上**可交互的元素编号**
// （DOM indexing）交给模型，模型说「点 12 号」而不是「点 (412, 388)」。与 computer use
// 的关系是分工不是替代：computer use 能碰整个屏幕但只有坐标可用，浏览器工具只在
// 那一个窗口里做事但精确、便宜（一份编号列表几百 token，一张截图几千）。
//
// 浏览器由宿主开（Electron 自带 Chromium，不用另装 Playwright）：一个独立的窗口、
// 独立的持久分区——登录态跨会话留着，用户随时能在那个窗口里接手。内核这边只把
// 工具调用翻译成请求。
//
// **页面内容是不可信的外部资料。** 编号列表与抽出来的正文都来自网页，网页里可以写
// 任何话，包括冲着模型说的。宿主在返回里加了边界标记，这里的描述也再说一次。
//
// 审批：打开网址要问（它是出网，而且目标由模型决定）；页面内的点、填、滚默认不问——
// 一次点击可能是「下一页」也可能是「确认下单」，从元素上看不出来，逐个问会把用户
// 训练成闭眼点允许。它们标成 EffectWrite（会改变页面状态）：on-write 档位放过、
// 严格档位（always）逐个问。快照、抽正文、截图只是看，任何档位都不问。

// registerBrowserTools 挂上浏览器工具。
func (s *Session) registerBrowserTools() error {
	if !s.config.EnableBrowser {
		return nil
	}

	type spec struct {
		name        string
		description string
		schema      json.RawMessage
		effect      tools.Effect
		build       func(json.RawMessage) (protocol.BrowserRequestParams, error)
	}
	specs := []spec{
		{
			name: "browser_navigate",
			description: "在浏览器窗口里打开一个网址（http/https），加载完返回页面标题与可交互元素的编号列表。" +
				"之后用 browser_click / browser_type 按编号操作。",
			schema: schemaOf(map[string]any{
				"url": map[string]any{"type": "string", "description": "完整网址，带 https://"},
			}, "url"),
			effect: tools.EffectExternal,
			build: func(raw json.RawMessage) (protocol.BrowserRequestParams, error) {
				var args struct {
					URL string `json:"url"`
				}
				if err := json.Unmarshal(raw, &args); err != nil {
					return protocol.BrowserRequestParams{}, errors.New("参数不是合法 JSON 对象")
				}
				url := strings.TrimSpace(args.URL)
				if url == "" {
					return protocol.BrowserRequestParams{}, errors.New("url 不能为空")
				}
				return protocol.BrowserRequestParams{Action: protocol.BrowserNavigate, URL: url}, nil
			},
		},
		{
			name: "browser_snapshot",
			description: "重新读一遍当前页面：标题、网址、滚动位置，以及可交互元素的编号列表" +
				"（链接、按钮、输入框、下拉框）。页面变了（点了、滚了、等它加载完）之后再操作前先取一次。",
			schema: emptySchema(),
			effect: tools.EffectRead,
			build: func(json.RawMessage) (protocol.BrowserRequestParams, error) {
				return protocol.BrowserRequestParams{Action: protocol.BrowserSnapshot}, nil
			},
		},
		{
			name:        "browser_click",
			description: "点击编号列表里的某个元素。返回点击后的页面状态。",
			schema:      indexSchema(),
			effect:      tools.EffectWrite,
			build:       indexAction(protocol.BrowserClick),
		},
		{
			name:        "browser_type",
			description: "往某个输入框（按编号）里填文本：先清空再输入。submit 为 true 时填完按回车提交。",
			schema: schemaOf(map[string]any{
				"index":  map[string]any{"type": "integer", "description": "元素编号"},
				"text":   map[string]any{"type": "string", "description": "要填的文本"},
				"submit": map[string]any{"type": "boolean", "description": "填完是否按回车"},
			}, "index", "text"),
			effect: tools.EffectWrite,
			build: func(raw json.RawMessage) (protocol.BrowserRequestParams, error) {
				var args struct {
					Index  *int   `json:"index"`
					Text   string `json:"text"`
					Submit bool   `json:"submit"`
				}
				if err := json.Unmarshal(raw, &args); err != nil {
					return protocol.BrowserRequestParams{}, errors.New("参数不是合法 JSON 对象")
				}
				if args.Index == nil {
					return protocol.BrowserRequestParams{}, errors.New("必须给出 index")
				}
				return protocol.BrowserRequestParams{
					Action: protocol.BrowserType, Index: *args.Index, Text: args.Text, Submit: args.Submit,
				}, nil
			},
		},
		{
			name:        "browser_select",
			description: "在下拉框（按编号）里选一项，按选项文字或值匹配。",
			schema: schemaOf(map[string]any{
				"index": map[string]any{"type": "integer", "description": "下拉框的编号"},
				"value": map[string]any{"type": "string", "description": "要选的选项文字或值"},
			}, "index", "value"),
			effect: tools.EffectWrite,
			build: func(raw json.RawMessage) (protocol.BrowserRequestParams, error) {
				var args struct {
					Index *int   `json:"index"`
					Value string `json:"value"`
				}
				if err := json.Unmarshal(raw, &args); err != nil {
					return protocol.BrowserRequestParams{}, errors.New("参数不是合法 JSON 对象")
				}
				if args.Index == nil || strings.TrimSpace(args.Value) == "" {
					return protocol.BrowserRequestParams{}, errors.New("必须给出 index 与 value")
				}
				return protocol.BrowserRequestParams{Action: protocol.BrowserSelect, Index: *args.Index, Value: args.Value}, nil
			},
		},
		{
			name:        "browser_scroll",
			description: "滚动页面。给 dy（正数向下，单位像素，一屏约 800）；或给 index 滚到某个元素处。",
			schema: schemaOf(map[string]any{
				"dy":    map[string]any{"type": "integer", "description": "纵向滚动量，正数向下"},
				"index": map[string]any{"type": "integer", "description": "滚到这个编号的元素处"},
			}),
			effect: tools.EffectWrite,
			build: func(raw json.RawMessage) (protocol.BrowserRequestParams, error) {
				var args struct {
					DY    int  `json:"dy"`
					Index *int `json:"index"`
				}
				if err := json.Unmarshal(raw, &args); err != nil {
					return protocol.BrowserRequestParams{}, errors.New("参数不是合法 JSON 对象")
				}
				request := protocol.BrowserRequestParams{Action: protocol.BrowserScroll, DY: args.DY, Index: -1}
				if args.Index != nil {
					request.Index = *args.Index
				}
				if request.DY == 0 && args.Index == nil {
					return protocol.BrowserRequestParams{}, errors.New("给 dy 或 index 其中一个")
				}
				return request, nil
			},
		},
		{
			name:        "browser_back",
			description: "浏览器后退一页。",
			schema:      emptySchema(),
			effect:      tools.EffectWrite,
			build: func(json.RawMessage) (protocol.BrowserRequestParams, error) {
				return protocol.BrowserRequestParams{Action: protocol.BrowserBack}, nil
			},
		},
		{
			name:        "browser_key",
			description: "在页面当前焦点处按一个键：Enter、Escape、Tab、ArrowDown、PageDown 等。",
			schema: schemaOf(map[string]any{
				"key": map[string]any{"type": "string", "description": "按键名"},
			}, "key"),
			effect: tools.EffectWrite,
			build: func(raw json.RawMessage) (protocol.BrowserRequestParams, error) {
				var args struct {
					Key string `json:"key"`
				}
				if err := json.Unmarshal(raw, &args); err != nil {
					return protocol.BrowserRequestParams{}, errors.New("参数不是合法 JSON 对象")
				}
				if strings.TrimSpace(args.Key) == "" {
					return protocol.BrowserRequestParams{}, errors.New("key 不能为空")
				}
				return protocol.BrowserRequestParams{Action: protocol.BrowserKey, Keys: args.Key}, nil
			},
		},
		{
			name: "browser_extract",
			description: "把当前页面的正文抽成纯文本交给你（去掉导航、脚本），用来读文章、表格、搜索结果。" +
				"页面很长时只给前面一部分，需要后面的先滚动再抽。",
			schema: emptySchema(),
			effect: tools.EffectRead,
			build: func(json.RawMessage) (protocol.BrowserRequestParams, error) {
				return protocol.BrowserRequestParams{Action: protocol.BrowserExtract}, nil
			},
		},
		{
			name:        "browser_screenshot",
			description: "把浏览器窗口当前画面截图给你看。编号列表看不出布局、图表、验证码时用；平时用 browser_snapshot 更省。",
			schema:      emptySchema(),
			effect:      tools.EffectRead,
			build: func(json.RawMessage) (protocol.BrowserRequestParams, error) {
				return protocol.BrowserRequestParams{Action: protocol.BrowserScreenshot}, nil
			},
		},
	}

	for _, item := range specs {
		item := item
		if err := s.registry.Register(tools.Tool{
			Name:        item.name,
			Description: item.description,
			Schema:      item.schema,
			Effect:      item.effect,
			Handler: func(ctx context.Context, args json.RawMessage, env *tools.Env) (string, error) {
				request, err := item.build(args)
				if err != nil {
					return "", err
				}
				request.SessionID = env.SessionID
				request.TurnID = env.TurnID
				emitter := s.currentEmitter()
				if emitter == nil {
					return "", errors.New("没有进行中的轮次，无法操作浏览器")
				}
				return s.runBrowserAction(ctx, env, request, item.effect, emitter)
			},
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) runBrowserAction(
	ctx context.Context,
	env *tools.Env,
	request protocol.BrowserRequestParams,
	effect tools.Effect,
	emitter Emitter,
) (string, error) {
	if err := env.RequestApproval(
		ctx, effect, protocol.ApprovalTool,
		"浏览器 "+string(request.Action), describeBrowserAction(request),
		"在应用自带的浏览器窗口里操作",
	); err != nil {
		return "", err
	}
	result, err := emitter.RequestBrowser(ctx, request)
	if err != nil {
		return "", err
	}
	if result.ImageBase64 != "" {
		image, err := base64.StdEncoding.DecodeString(result.ImageBase64)
		if err != nil {
			return "", fmt.Errorf("截图数据损坏：%w", err)
		}
		env.Attach(image)
	}
	if result.Text == "" {
		return "已执行。", nil
	}
	return result.Text, nil
}

func describeBrowserAction(request protocol.BrowserRequestParams) string {
	switch request.Action {
	case protocol.BrowserNavigate:
		return "打开 " + request.URL
	case protocol.BrowserClick:
		return fmt.Sprintf("点击 %d 号元素", request.Index)
	case protocol.BrowserType:
		return fmt.Sprintf("在 %d 号元素里输入：%s", request.Index, request.Text)
	case protocol.BrowserSelect:
		return fmt.Sprintf("在 %d 号下拉框里选：%s", request.Index, request.Value)
	case protocol.BrowserScroll:
		if request.Index >= 0 {
			return fmt.Sprintf("滚到 %d 号元素", request.Index)
		}
		return fmt.Sprintf("滚动 %d 像素", request.DY)
	case protocol.BrowserKey:
		return "按键：" + request.Keys
	default:
		return string(request.Action)
	}
}

func indexAction(action protocol.BrowserAction) func(json.RawMessage) (protocol.BrowserRequestParams, error) {
	return func(raw json.RawMessage) (protocol.BrowserRequestParams, error) {
		var args struct {
			Index *int `json:"index"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return protocol.BrowserRequestParams{}, errors.New("参数不是合法 JSON 对象")
		}
		if args.Index == nil || *args.Index < 0 {
			return protocol.BrowserRequestParams{}, errors.New("必须给出编号列表里的 index")
		}
		return protocol.BrowserRequestParams{Action: action, Index: *args.Index}, nil
	}
}

func indexSchema() json.RawMessage {
	return schemaOf(map[string]any{
		"index": map[string]any{"type": "integer", "description": "元素编号，来自 browser_snapshot / browser_navigate 的列表"},
	}, "index")
}
