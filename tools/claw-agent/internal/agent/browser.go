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

// 浏览器：让模型驾驭一个真正的浏览器——按元素编号点、填、选，而不是按屏幕坐标。
//
// 浏览器有两种（宿主按设置选，内核不知道也不必知道）：AIClaw 自带的窗口，或者用户
// 自己的 Chrome / Edge（经「AIClaw 浏览器助手」扩展，在后台标签页里、用用户的登录态，
// 不抢鼠标）。后一种多了 browser_tabs / browser_use_tab：接管用户已经打开的页面。
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
			description: "Open a URL (http/https) in the browser. Once it loads, returns the page title and a numbered list of interactive elements; " +
				"then act on them by number with browser_click / browser_type. When the user's own browser is in use, the page opens in a background AIClaw tab " +
				"and doesn't switch away from the page the user is looking at.",
			schema: schemaOf(map[string]any{
				"url": map[string]any{"type": "string", "description": "Full URL, including https://"},
			}, "url"),
			effect: tools.EffectExternal,
			build: func(raw json.RawMessage) (protocol.BrowserRequestParams, error) {
				var args struct {
					URL string `json:"url"`
				}
				if err := json.Unmarshal(raw, &args); err != nil {
					return protocol.BrowserRequestParams{}, i18n.E("参数不是合法 JSON 对象")
				}
				url := strings.TrimSpace(args.URL)
				if url == "" {
					return protocol.BrowserRequestParams{}, i18n.E("url 不能为空")
				}
				return protocol.BrowserRequestParams{Action: protocol.BrowserNavigate, URL: url}, nil
			},
		},
		{
			name: "browser_snapshot",
			description: "Re-read the current page: title, URL, scroll position, and the numbered list of interactive elements " +
				"(links, buttons, inputs, selects). After the page changes (a click, a scroll, finished loading), take a fresh snapshot before acting again.",
			schema: emptySchema(),
			effect: tools.EffectRead,
			build: func(json.RawMessage) (protocol.BrowserRequestParams, error) {
				return protocol.BrowserRequestParams{Action: protocol.BrowserSnapshot}, nil
			},
		},
		{
			name:        "browser_click",
			description: "Click an element from the numbered list. Returns the page state after the click.",
			schema:      indexSchema(),
			effect:      tools.EffectWrite,
			build:       indexAction(protocol.BrowserClick),
		},
		{
			name:        "browser_type",
			description: "Type text into an input (by number): clears it first, then types. With submit=true, presses Enter afterwards to submit.",
			schema: schemaOf(map[string]any{
				"index":  map[string]any{"type": "integer", "description": "Element number"},
				"text":   map[string]any{"type": "string", "description": "Text to enter"},
				"submit": map[string]any{"type": "boolean", "description": "Press Enter after typing"},
			}, "index", "text"),
			effect: tools.EffectWrite,
			build: func(raw json.RawMessage) (protocol.BrowserRequestParams, error) {
				var args struct {
					Index  *int   `json:"index"`
					Text   string `json:"text"`
					Submit bool   `json:"submit"`
				}
				if err := json.Unmarshal(raw, &args); err != nil {
					return protocol.BrowserRequestParams{}, i18n.E("参数不是合法 JSON 对象")
				}
				if args.Index == nil {
					return protocol.BrowserRequestParams{}, i18n.E("必须给出 index")
				}
				return protocol.BrowserRequestParams{
					Action: protocol.BrowserType, Index: *args.Index, Text: args.Text, Submit: args.Submit,
				}, nil
			},
		},
		{
			name:        "browser_select",
			description: "Choose an option in a select (by number), matched by option text or value.",
			schema: schemaOf(map[string]any{
				"index": map[string]any{"type": "integer", "description": "Number of the select"},
				"value": map[string]any{"type": "string", "description": "Text or value of the option to choose"},
			}, "index", "value"),
			effect: tools.EffectWrite,
			build: func(raw json.RawMessage) (protocol.BrowserRequestParams, error) {
				var args struct {
					Index *int   `json:"index"`
					Value string `json:"value"`
				}
				if err := json.Unmarshal(raw, &args); err != nil {
					return protocol.BrowserRequestParams{}, i18n.E("参数不是合法 JSON 对象")
				}
				if args.Index == nil || strings.TrimSpace(args.Value) == "" {
					return protocol.BrowserRequestParams{}, i18n.E("必须给出 index 与 value")
				}
				return protocol.BrowserRequestParams{Action: protocol.BrowserSelect, Index: *args.Index, Value: args.Value}, nil
			},
		},
		{
			name:        "browser_scroll",
			description: "Scroll the page. Pass dy (pixels, positive scrolls down; one screen is about 800), or pass index to scroll to an element.",
			schema: schemaOf(map[string]any{
				"dy":    map[string]any{"type": "integer", "description": "Vertical scroll amount; positive scrolls down"},
				"index": map[string]any{"type": "integer", "description": "Scroll to the element with this number"},
			}),
			effect: tools.EffectWrite,
			build: func(raw json.RawMessage) (protocol.BrowserRequestParams, error) {
				var args struct {
					DY    int  `json:"dy"`
					Index *int `json:"index"`
				}
				if err := json.Unmarshal(raw, &args); err != nil {
					return protocol.BrowserRequestParams{}, i18n.E("参数不是合法 JSON 对象")
				}
				request := protocol.BrowserRequestParams{Action: protocol.BrowserScroll, DY: args.DY, Index: -1}
				if args.Index != nil {
					request.Index = *args.Index
				}
				if request.DY == 0 && args.Index == nil {
					return protocol.BrowserRequestParams{}, i18n.E("给 dy 或 index 其中一个")
				}
				return request, nil
			},
		},
		{
			name:        "browser_back",
			description: "Go back one page in the browser.",
			schema:      emptySchema(),
			effect:      tools.EffectWrite,
			build: func(json.RawMessage) (protocol.BrowserRequestParams, error) {
				return protocol.BrowserRequestParams{Action: protocol.BrowserBack}, nil
			},
		},
		{
			name:        "browser_key",
			description: "Press a key at the page's current focus: Enter, Escape, Tab, ArrowDown, PageDown, etc.",
			schema: schemaOf(map[string]any{
				"key": map[string]any{"type": "string", "description": "Key name"},
			}, "key"),
			effect: tools.EffectWrite,
			build: func(raw json.RawMessage) (protocol.BrowserRequestParams, error) {
				var args struct {
					Key string `json:"key"`
				}
				if err := json.Unmarshal(raw, &args); err != nil {
					return protocol.BrowserRequestParams{}, i18n.E("参数不是合法 JSON 对象")
				}
				if strings.TrimSpace(args.Key) == "" {
					return protocol.BrowserRequestParams{}, i18n.E("key 不能为空")
				}
				return protocol.BrowserRequestParams{Action: protocol.BrowserKey, Keys: args.Key}, nil
			},
		},
		{
			name: "browser_extract",
			description: "Extract the main content of the current page as plain text (navigation and scripts removed), for reading articles, tables and search results. " +
				"Long pages are cut off after the first part; to read further, scroll first and extract again.",
			schema: emptySchema(),
			effect: tools.EffectRead,
			build: func(json.RawMessage) (protocol.BrowserRequestParams, error) {
				return protocol.BrowserRequestParams{Action: protocol.BrowserExtract}, nil
			},
		},
		{
			name: "browser_tabs",
			description: "List the tabs open in the user's browser (number, title, URL, with the one the user is viewing marked). " +
				"When the user says \"on the page I'm on\" or \"the form I have open\", use this to find that tab first, then call browser_use_tab.",
			schema: emptySchema(),
			effect: tools.EffectRead,
			build: func(json.RawMessage) (protocol.BrowserRequestParams, error) {
				return protocol.BrowserRequestParams{Action: protocol.BrowserTabs}, nil
			},
		},
		{
			name: "browser_use_tab",
			description: "Take over a tab already open in the user's browser; subsequent snapshots, clicks and typing act on it. " +
				"It is the user's own page, signed in with their accounts — use this only when the user explicitly asks you to work on it. To open a URL normally, use browser_navigate, " +
				"which opens it in AIClaw's own background tab without disturbing the user.",
			schema: schemaOf(map[string]any{
				"tabId": map[string]any{"type": "integer", "description": "Tab number from browser_tabs"},
			}, "tabId"),
			// 接管的是用户自己的页面（带着他的登录）：与打开网址一样，每次都问。
			effect: tools.EffectExternal,
			build: func(raw json.RawMessage) (protocol.BrowserRequestParams, error) {
				var args struct {
					TabID *int `json:"tabId"`
				}
				if err := json.Unmarshal(raw, &args); err != nil {
					return protocol.BrowserRequestParams{}, i18n.E("参数不是合法 JSON 对象")
				}
				if args.TabID == nil || *args.TabID <= 0 {
					return protocol.BrowserRequestParams{}, i18n.E("必须给出 browser_tabs 列出的 tabId")
				}
				return protocol.BrowserRequestParams{Action: protocol.BrowserUseTab, TabID: *args.TabID, Index: -1}, nil
			},
		},
		{
			name:        "browser_screenshot",
			description: "Take a screenshot of the browser's current page. Use it when the numbered list can't show layout, charts or CAPTCHAs; otherwise browser_snapshot is cheaper.",
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
					return "", i18n.E("没有进行中的轮次，无法操作浏览器")
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
		i18n.D("浏览器 {action}", "action", string(request.Action)), describeBrowserAction(request),
		browserApprovalReason(request),
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
			return "", fmt.Errorf("%s: %w", i18n.D("截图数据损坏"), err)
		}
		env.Attach(image)
	}
	if result.Text == "" {
		return i18n.D("已执行。"), nil
	}
	return result.Text, nil
}

func browserApprovalReason(request protocol.BrowserRequestParams) string {
	if request.Action == protocol.BrowserUseTab {
		return i18n.D("接管的是你自己打开的页面：之后的点击、输入都作用在它上面，用的是你的登录")
	}
	return i18n.D("在浏览器里操作（设置 → 浏览器里选的那个：AIClaw 自带的窗口，或你自己的浏览器）")
}

func describeBrowserAction(request protocol.BrowserRequestParams) string {
	switch request.Action {
	case protocol.BrowserNavigate:
		return i18n.D("打开 {url}", "url", request.URL)
	case protocol.BrowserClick:
		return i18n.D("点击 {index} 号元素", "index", request.Index)
	case protocol.BrowserType:
		return i18n.D("在 {index} 号元素里输入：{text}", "index", request.Index, "text", request.Text)
	case protocol.BrowserSelect:
		return i18n.D("在 {index} 号下拉框里选：{value}", "index", request.Index, "value", request.Value)
	case protocol.BrowserScroll:
		if request.Index >= 0 {
			return i18n.D("滚到 {index} 号元素", "index", request.Index)
		}
		return i18n.D("滚动 {dy} 像素", "dy", request.DY)
	case protocol.BrowserKey:
		return i18n.D("按键：{keys}", "keys", request.Keys)
	case protocol.BrowserUseTab:
		return i18n.D("接管你浏览器里的标签页 {tab}", "tab", request.TabID)
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
			return protocol.BrowserRequestParams{}, i18n.E("参数不是合法 JSON 对象")
		}
		if args.Index == nil || *args.Index < 0 {
			return protocol.BrowserRequestParams{}, i18n.E("必须给出编号列表里的 index")
		}
		return protocol.BrowserRequestParams{Action: action, Index: *args.Index}, nil
	}
}

func indexSchema() json.RawMessage {
	return schemaOf(map[string]any{
		"index": map[string]any{"type": "integer", "description": "Element number from the browser_snapshot / browser_navigate list"},
	}, "index")
}
