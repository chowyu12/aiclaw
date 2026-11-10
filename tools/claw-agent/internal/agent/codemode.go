package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/mcpclient"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"strings"

	"github.com/chowyu12/aiclaw/internal/i18n"
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
	if s.codeModeBase != nil {
		existing = s.codeModeBase.List()
	} else {
		s.codeModeBase = tools.NewRegistry()
		_ = s.codeModeBase.Replace(existing)
	}
	if len(existing) == 0 {
		return 0
	}

	if s.codeRuntime == nil {
		s.codeRuntime = codemode.New(nil, codemode.DefaultLimits())
		s.codeRuntime.WorkerCommand = codemode.SelfWorkerCommand()
		s.codeRuntime.SaveOutput = s.saveToolOutput
		s.codeCells = codemode.NewCells(s.codeRuntime)
	}
	bridged := make([]codemode.Tool, 0, len(existing)+2)
	for _, tool := range existing {
		bridged = append(bridged, s.bridgeCodeTool(tool))
	}
	bridged = codemode.AssignIdentifiers(bridged)
	for _, discovery := range s.codeRuntime.DiscoveryTools() {
		native := discovery.Call
		tool := tools.Tool{Name: discovery.Name, Description: discovery.Description, Schema: discovery.Schema, Effect: tools.EffectRead,
			Handler: func(ctx context.Context, args json.RawMessage, _ *tools.Env) (string, error) {
				return native(ctx, args)
			}}
		discovery.Call = s.bridgeCodeTool(tool).Call
		bridged = append(bridged, discovery)
	}
	s.codeRuntime.UpdateTools(bridged)
	description := codemode.Description(s.codeRuntime.Tools())
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
				"title": {"type": "string", "minLength": 1, "description": "A short action title in the user's language describing what this step will do, e.g. check the current time or search for GDP data. Do not use code, variable declarations, or generic labels such as call run_command as the title."},
				"code": {"type": "string", "description": "The JavaScript to run. Top-level await and return are allowed."},
                "yield_time_ms":{"type":"integer","minimum":0,"maximum":60000,"description":"Return early with a cell_id if the script is still running; default 30000 ms."},
                "max_output_tokens":{"type":"integer","minimum":1,"maximum":6144,"description":"Approximate text output budget; default 6000. Media has a separate limit."}
			},
			"required": ["title", "code"],
			"additionalProperties": false
		}`),
		Handler: func(ctx context.Context, raw json.RawMessage, env *tools.Env) (string, error) {
			var args struct {
				Code      string `json:"code"`
				YieldMS   *int   `json:"yield_time_ms"`
				MaxTokens *int   `json:"max_output_tokens"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", errors.New(tools.ExplainBadArguments(string(raw), err))
			}
			if args.Code == "" {
				return "", i18n.E("code 不能为空")
			}
			ms, bytes, err := codeObservationLimits(args.YieldMS, args.MaxTokens)
			if err != nil {
				return "", err
			}
			lifetime := ctx
			if env != nil && env.RunContext != nil {
				lifetime = env.RunContext
			}
			observation, err := s.codeCells.Start(context.WithValue(ctx, envKey{}, env), lifetime, args.Code, ms, bytes)
			if err != nil {
				return "", err
			}
			return s.codeObservation(ctx, env, observation)
		},
	})
	if err != nil {
		// 注册一个工具失败只可能是名字冲突，而这里的注册表是刚建的。
		s.mcpStatus[i18n.D("代码模式")] = i18n.D("启用失败：{error}", "error", err)
		return 0
	}

	if err := replacement.Register(s.codeWaitTool()); err != nil {
		return 0
	}

	if len(s.folded) > 0 {
		for _, tool := range s.registry.List() {
			if tool.Name != "exec" && tool.Name != "wait" {
				_ = replacement.Register(tool)
			}
		}
	}
	_ = s.registry.Replace(replacement.List())
	s.folded = make([]string, 0, len(existing))
	for _, tool := range existing {
		s.folded = append(s.folded, tool.Name)
	}
	return len(existing)
}

// FoldedTools 返回代码模式下被收进 exec 的工具名；没开代码模式是空的。
func (s *Session) FoldedTools() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.folded...)
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
		// 两种语言都认（isMountedStatus）：状态文字跟着界面语言走。
		if !isMountedStatus(s.mcpStatus[name]) {
			continue
		}
		s.mcpStatus[name] = i18n.D("已挂载 {n} 个工具（已收进 exec，不单独占用上下文）", "n", mounted)
	}
}

func codeObservationLimits(ms, tokens *int) (int, int, error) {
	wait, budget := 30000, 6000
	if ms != nil {
		wait = *ms
	}
	if tokens != nil {
		budget = *tokens
	}
	if wait < 0 || wait > 60000 || budget < 1 || budget > 6144 {
		return 0, 0, errors.New("yield_time_ms must be 0..60000 and max_output_tokens 1..6144")
	}
	return wait, budget * 4, nil
}

func (s *Session) codeWaitTool() tools.Tool {
	return tools.Tool{Name: "wait", Effect: tools.EffectRead,
		Description: "Continue observing a yielded exec cell without rerunning it. Only new output is returned. Use only with the cell_id from exec. terminate=true cancels the cell and its nested tools. Cells are session-local and stop at the end of their originating turn.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"cell_id":{"type":"string"},"yield_time_ms":{"type":"integer","minimum":0,"maximum":60000},"max_output_tokens":{"type":"integer","minimum":1,"maximum":6144},"terminate":{"type":"boolean"}},"required":["cell_id"],"additionalProperties":false}`),
		Handler: func(ctx context.Context, raw json.RawMessage, env *tools.Env) (string, error) {
			var args struct {
				ID        string `json:"cell_id"`
				YieldMS   *int   `json:"yield_time_ms"`
				MaxTokens *int   `json:"max_output_tokens"`
				Terminate bool   `json:"terminate"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", err
			}
			if args.YieldMS == nil {
				v := 10000
				args.YieldMS = &v
			}
			ms, bytes, err := codeObservationLimits(args.YieldMS, args.MaxTokens)
			if err != nil {
				return "", err
			}
			observation, err := s.codeCells.Wait(ctx, args.ID, ms, bytes, args.Terminate)
			if err != nil {
				return "", err
			}
			return s.codeObservation(ctx, env, observation)
		},
	}
}

func (s *Session) codeObservation(ctx context.Context, env *tools.Env, o codemode.Observation) (string, error) {
	text := o.Text
	for _, item := range o.Media {
		if env == nil {
			return text, errors.New("media output requires a tool environment")
		}
		tools.EmitContent(ctx, protocol.ToolContent{Type: item.Type, URL: item.URL, Hint: item.Hint})
		prefix, encoded, _ := strings.Cut(item.URL, ",")
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return text, err
		}
		if item.Type != "audio" {
			env.Attach(data)
		}
		if item.Type == "generatedImage" || item.Type == "audio" {
			kind, ext := "image", detectImageExt(data)
			if item.Type == "audio" {
				kind = "audio"
				ext = "wav"
				switch {
				case strings.Contains(prefix, "mpeg"):
					ext = "mp3"
				case strings.Contains(prefix, "ogg"):
					ext = "ogg"
				case strings.Contains(prefix, "mp4"):
					ext = "m4a"
				case strings.Contains(prefix, "webm"):
					ext = "webm"
				}
			}
			effect := tools.EffectWrite
			_, inside, err := env.ResolveWrite(generatedDir + "/output." + ext)
			if err != nil {
				return text, err
			}
			if !inside {
				effect = tools.EffectWriteOutside
			}
			if err := env.RequestApproval(ctx, effect, protocol.ApprovalWrite, "Save code-mode media", generatedDir, "Save explicitly emitted media as a local file"); err != nil {
				return text, err
			}
			path, err := saveGenerated(env, kind+"-"+newID("media"), ext, data)
			if err != nil {
				return text, err
			}
			tools.Produce(ctx, path)
			text += fmt.Sprintf("\n%s output: %s", kind, path)
			if item.Hint != "" {
				text += "\n" + item.Hint
			}
		}
	}
	if o.Running {
		text += "\nScript running with cell ID " + o.ID + "; use wait with cell_id to continue. Do not rerun this script."
	}
	return strings.TrimSpace(text), o.Error
}

// All nested entry points, including metadata discovery, share the same execution boundary.
func (s *Session) bridgeCodeTool(tool tools.Tool) codemode.Tool {
	return codemode.Tool{ParallelSafe: tool.ParallelSafe(), Name: tool.Name, Description: tool.Description, Schema: tool.Schema,
		Call: func(ctx context.Context, args json.RawMessage) (string, error) {
			env, ok := ctx.Value(envKey{}).(*tools.Env)
			if !ok || env == nil {
				return "", i18n.E("内部错误：脚本里拿不到执行环境")
			}
			scope, _ := ctx.Value(toolExecutionKey{}).(*toolExecutionScope)
			_, output, err := s.executeRecorded(ctx, llm.ToolCall{Name: tool.Name, Arguments: string(args)}, env, scope, "exec", func(ctx context.Context) (string, error) {
				return invokeToolHandler(mcpclient.WithStructuredOutput(ctx), tool, args, env)
			})
			return output, err
		},
	}
}

// KeepCodeModeState preserves live chat data when idle settings remount its tools.
func (s *Session) KeepCodeModeState(previous *Session) {
	if previous != nil && previous.ID == s.ID && previous.codeRuntime != nil && s.codeRuntime != nil {
		s.codeRuntime.CopyStateFrom(previous.codeRuntime)
	}
}
