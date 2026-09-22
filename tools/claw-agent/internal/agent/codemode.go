package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/codemode"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/tools"
)

/*
代码模式：把注册表里那一堆工具收进一个 `exec` 工具，模型改成写 JavaScript 来调。

两个实测数字说明它为什么存在：102 个数据 API 直挂约 99K token、161 个 API 操作
约 35K token（≈110KB 的工具定义），而工具清单**每次请求都整份重发、压缩碰不到
它**。换成 exec 之后，同样 161 个工具的描述是 7.4KB。

更值钱的是第二件事：组合调用不再经过模型。「这 12 张表昨天有没有数据」在直挂
模式下是 12 轮来回、12 份完整返回全进上下文；写成一段循环之后，中间结果一次
都不进上下文。

**审批没有被绕过。** 脚本里的每一次 tools.xxx() 走的还是同一个 handler、同一个
Env，该弹的框照弹——只是现在它们发生在一段脚本执行的过程中。
*/

// installCodeMode 用一个 exec 工具替换掉注册表里的全部工具。
//
// 返回替换掉的工具数量，给挂载状态显示用。
func (s *Session) installCodeMode() int {
	existing := s.registry.List()
	if len(existing) == 0 {
		return 0
	}

	bridged := make([]codemode.Tool, 0, len(existing))
	for _, tool := range existing {
		tool := tool
		bridged = append(bridged, codemode.Tool{
			Name:        tool.Name,
			Description: tool.Description,
			Schema:      tool.Schema,
			Call: func(ctx context.Context, arguments json.RawMessage) (string, error) {
				// env 由 exec 的 handler 在调用时塞进来——它是每轮新建的，
				// 不能在这里捕获。
				env, ok := ctx.Value(envKey{}).(*tools.Env)
				if !ok {
					return "", fmt.Errorf("内部错误：脚本里拿不到执行环境")
				}
				return tool.Handler(ctx, arguments, env)
			},
		})
	}
	bridged = codemode.AssignIdentifiers(bridged)

	runtime := codemode.New(bridged, codemode.DefaultLimits())
	description := codemode.Description(bridged)
	before, after := codeModeSavings(existing, description)
	s.codeModeTokens = [2]int{before, after}

	replacement := tools.NewRegistry()
	err := replacement.Register(tools.Tool{
		Name:        "exec",
		Description: description,
		// exec 自己没有副作用——副作用在它调用的那些工具里，那些工具各自
		// 按自己的 Effect 走审批。给它标成写或执行的话，每跑一段脚本都会
		// 先弹一个「要执行吗」，而那个框里什么信息都没有。
		Effect: tools.EffectRead,
		Schema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"code": {"type": "string", "description": "要执行的 JavaScript。顶层可以直接用 await 和 return。"}
			},
			"required": ["code"],
			"additionalProperties": false
		}`),
		Handler: func(ctx context.Context, raw json.RawMessage, env *tools.Env) (string, error) {
			var args struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("参数不是合法 JSON 对象：%w", err)
			}
			if args.Code == "" {
				return "", fmt.Errorf("code 不能为空")
			}
			return runtime.Run(context.WithValue(ctx, envKey{}, env), args.Code)
		},
	})
	if err != nil {
		// 注册一个工具失败只可能是名字冲突，而这里的注册表是刚建的。
		s.mcpStatus["代码模式"] = "启用失败：" + err.Error()
		return 0
	}

	*s.registry = *replacement
	return len(bridged)
}

// codeModeSavings 估一下换成 exec 省了多少上下文。
//
// 为什么要算：挂载状态那一栏是**唯一**能让用户看见「工具占了多少上下文」的
// 地方。代码模式下各个 server 那几行「已挂载 N 个工具（约占 X token）」已经
// 不成立了——那些工具不再作为工具发给模型。不把这件事说清楚，那一栏就在说谎。
func codeModeSavings(replaced []tools.Tool, description string) (before, after int) {
	for _, tool := range replaced {
		before += approxTokens(tool.Name) + approxTokens(tool.Description) + approxTokens(string(tool.Schema))
	}
	return before, approxTokens(description)
}

// envKey 是把 Env 传进脚本桥接层的 context key。
//
// 不用闭包捕获：Env 是每轮新建的（带着这一轮的审批通道与 turnID），
// 捕获住第一轮那个，之后每一轮的审批都会发到一个已经结束的轮次上。
type envKey struct{}

// foldStatusIntoExec 把各个 server 那行状态里的 token 占用改掉。
//
// 代码模式装完之后，那些工具不再作为工具发给模型——「已挂载 5 个工具
// （约占 1.1K token 上下文）」这句话两半都还在，但后半句已经是假的。
// 只在「代码模式」那一行说明「下面的不再计入」不够：状态栏是一行一行读的，
// 用户看到的是 aihot 那一行，不会回头把上面那行当成它的脚注。
//
// 只改成功挂载的那几行——挂载失败、重名被跳过的原文要留着，那才是要看的。
func (s *Session) foldStatusIntoExec() {
	for name, mounted := range s.mcpMounted {
		if !strings.HasPrefix(s.mcpStatus[name], "已挂载 ") {
			continue
		}
		s.mcpStatus[name] = fmt.Sprintf("已挂载 %d 个工具（已收进 exec，不单独占用上下文）", mounted)
	}
}
