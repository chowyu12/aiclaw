package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"

	"github.com/chowyu12/aiclaw/internal/i18n"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/tools"
)

/*
在 goja 里跑一段模型写的 JavaScript，脚本里可以 `await tools.xxx(...)`。

**为什么不直接把工具挂给模型。** 两个实测数字：102 个数据 API 直挂约 99K
token、161 个 API 操作约 35K token，而工具清单每次请求都整份重发、压缩碰不到
它——那是开局就占掉的固定地板。更贵的是另一件事：「这 12 张表昨天有没有数据」
在直挂模式下是 12 轮来回，12 份完整返回全部进上下文；写成一段循环之后，
中间结果一次都不进上下文，只有最后那几行进。

**为什么是 goja。** 纯 Go，不引 cgo（`make nocgo` 守着那条线）。它没有任何
宿主能力——没有 require、没有 fs、没有 net——脚本能碰到的只有我们显式挂上去的
那几个函数。这一点很关键：脚本是模型写的，而模型读到的东西可能被注入。
*/

// Tool 是暴露给脚本的一个工具。
type Tool struct {
	// Only tools explicitly known to be read-only may overlap other reads.
	ParallelSafe bool
	// Name 是原始工具名（可能带 `-`、`__`，不是合法 JS 标识符）。
	Name string
	// Ident 是脚本里用的名字，由 Identifier 规范化而来。
	Ident       string
	Description string
	Schema      json.RawMessage
	// Call 执行它。参数是 JSON 对象；返回的字符串会被尝试解析成 JSON。
	Call func(ctx context.Context, arguments json.RawMessage) (string, error)
}

// Limits 是一次执行的上限。
type Limits struct {
	// Budget 是脚本自己能跑多久，**不含等工具的时间**。
	//
	// 分开算是必须的：一次工具调用可能在等用户点审批（最长半小时），
	// 把那段时间算进来的话，凡是需要确认的脚本都会被判超时。
	Budget time.Duration
	// MaxCalls 挡住「循环里调一万次」。
	MaxCalls int
	// MaxOutput 是回给模型的字节上限。
	MaxOutput int
}

// DefaultLimits 是一组够用的默认值。
func DefaultLimits() Limits {
	return Limits{Budget: 30 * time.Second, MaxCalls: 200, MaxOutput: 32 * 1024}
}

// Runtime 执行脚本。一个会话一个，store/load 的数据存在这里。
type Runtime struct {
	tools      []Tool
	limits     Limits
	SaveOutput tools.OutputSaver
	// stored 是脚本之间共享的键值，给 store()/load() 用。
	// 一段脚本查出来的东西，下一段还能接着用，不必重新查一遍。
	//
	// **必须加锁**：exec 标的是只读，所以模型一次给出两个 exec 调用时，
	// 内核会并行执行它们，而它们共用这一张表。Go 里并发写 map 不是竞态
	// 那么简单——是直接 fatal，整个内核没了。
	storeMu sync.Mutex
	stored  map[string]any
	gate    sync.RWMutex // Shared across cells: writes exclude every other tool call.
}

func New(tools []Tool, limits Limits) *Runtime {
	return &Runtime{tools: tools, limits: limits, stored: map[string]any{}}
}

// errExit 是 exit() 抛出来的哨兵，表示「正常提前结束」。
var errExit = errors.New("codemode: exit")

// Run 执行一段脚本，返回给模型看的输出。
func (r *Runtime) Run(parent context.Context, source string) (string, error) {
	ctx, cancel := context.WithCancel(parent)
	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	output := tools.OutputBuffer{Complete: true}
	appendOutput := func(text string) { _, _ = output.Write([]byte(text)) }
	preview := func() string {
		return tools.OutputPreview(parent, output.String(), min(r.limits.MaxOutput, tools.MaxOutputPreview), output.Complete, r.SaveOutput)
	}
	var calls atomic.Int64
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	// Only this goroutine touches the VM. Workers send settlements to this loop.
	completions := make(chan func() error, r.limits.MaxCalls)
	pending := 0
	schedule := func(tool Tool, arguments json.RawMessage, resolve, reject func(any) error) {
		pending++
		workers.Add(1)
		go func() {
			defer workers.Done()
			if tool.ParallelSafe {
				r.gate.RLock()
			} else {
				r.gate.Lock()
			}
			result, callErr := "", ctx.Err()
			if callErr == nil {
				result, callErr = tool.Call(ctx, arguments)
			}
			if tool.ParallelSafe {
				r.gate.RUnlock()
			} else {
				r.gate.Unlock()
			}
			warning := ""
			if callErr == nil && len(result) > tools.MaxOutputPreview && r.SaveOutput != nil {
				id, err := r.SaveOutput(ctx, result, true)
				if err != nil {
					warning = "Partial evidence: oversized tool result could not be retained: " + err.Error() + "\n"
				} else {
					warning = fmt.Sprintf("Retained oversized result from tools.%s: output_id=%s; use read_tool_output to inspect omitted fields. Retention is capped at 8 MiB; inspect complete before assuming this is the whole source.\n", tool.Ident, id)
				}
			}
			settle := func() error {
				pending--
				if callErr != nil {
					return reject(vm.ToValue(callErr.Error()))
				}
				appendOutput(warning)
				return resolve(toolValue(vm, result))
			}
			select {
			case completions <- settle:
			case <-ctx.Done():
			}
		}()
	}
	if err := r.install(ctx, vm, appendOutput, &calls, schedule); err != nil {
		return "", err
	}
	// Charge only VM execution, not wall time waiting for concurrent tools/approval.
	var spent, active atomic.Int64
	runVM := func(fn func()) {
		started := time.Now()
		active.Store(started.UnixNano())
		defer func() { active.Store(0); spent.Add(int64(time.Since(started))) }()
		fn()
	}
	done := make(chan struct{})
	budgetExceeded := make(chan struct{})
	budgetError := i18n.E("脚本运行超过 {budget}，已中断", "budget", r.limits.Budget)
	defer close(done)
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				vm.Interrupt(i18n.D("上层已取消"))
				return
			case <-ticker.C:
				elapsed := time.Duration(spent.Load())
				if at := active.Load(); at != 0 {
					elapsed += time.Since(time.Unix(0, at))
				}
				if elapsed > r.limits.Budget {
					close(budgetExceeded)
					vm.Interrupt(budgetError)
					return
				}
			}
		}
	}()
	var value goja.Value
	var err error
	runVM(func() { value, err = vm.RunString("(async () => {\n" + StripFences(source) + "\n})()") })
	if ctx.Err() != nil {
		return preview(), ctx.Err()
	}
	if err != nil {
		return preview(), scriptError(err)
	}
	promise, ok := value.Export().(*goja.Promise)
	if !ok {
		return preview(), i18n.E("脚本没有返回预期的结果")
	}
	// Drain admitted calls even if the script forgot to await them; none may outlive Run.
	for pending > 0 && promise.State() != goja.PromiseStateRejected {
		select {
		case <-budgetExceeded:
			return preview(), budgetError
		case <-ctx.Done():
			return preview(), ctx.Err()
		case settle := <-completions:
			if ctx.Err() != nil {
				return preview(), ctx.Err()
			}
			runVM(func() { err = settle() })
			if ctx.Err() != nil {
				return preview(), ctx.Err()
			}
			if err != nil {
				return preview(), scriptError(err)
			}
		}
	}
	switch promise.State() {
	case goja.PromiseStateRejected:
		reason := promise.Result()
		if isExit(reason) {
			return finish(preview(), calls.Load()), nil
		}
		return preview(), i18n.E("脚本抛出异常：{error}", "error", describe(reason))
	case goja.PromiseStatePending:
		return preview(), i18n.E("脚本结束时仍有未完成的等待，可能 await 了一个不会返回的东西")
	}
	if result := promise.Result(); result != nil && !goja.IsUndefined(result) && !goja.IsNull(result) {
		appendOutput(stringify(result))
	}
	return finish(preview(), calls.Load()), nil
}

func finish(text string, calls int64) string {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return i18n.D("（脚本执行完成，没有输出；调用了 {calls} 次工具）", "calls", calls)
	}
	return text
}

// install 把 tools 与几个辅助函数挂进 VM。
func (r *Runtime) install(
	ctx context.Context,
	vm *goja.Runtime,
	appendOutput func(string),
	calls *atomic.Int64,
	schedule func(Tool, json.RawMessage, func(any) error, func(any) error),
) error {
	bridge := newToolBridge(vm)
	catalog := make([]any, 0, len(r.tools))

	for _, tool := range r.tools {
		tool := tool
		catalog = append(catalog, map[string]any{"name": tool.Ident, "description": tool.Description})
		bridge.add(tool.Ident, vm.ToValue(func(call goja.FunctionCall) goja.Value {
			promise, resolve, reject := vm.NewPromise()

			if calls.Add(1) > int64(r.limits.MaxCalls) {
				_ = reject(vm.ToValue(i18n.D("工具调用次数超过上限 {max}", "max", r.limits.MaxCalls)))
				return vm.ToValue(promise)
			}

			arguments, err := encodeArguments(vm, call.Argument(0))
			if err != nil {
				_ = reject(vm.ToValue(err.Error()))
				return vm.ToValue(promise)
			}

			if err := ctx.Err(); err != nil {
				_ = reject(vm.ToValue(err.Error()))
			} else {
				schedule(tool, arguments, resolve, reject)
			}
			return vm.ToValue(promise)
		}))
	}
	if err := vm.Set("tools", vm.NewDynamicObject(bridge)); err != nil {
		return err
	}
	if err := vm.Set("ALL_TOOLS", catalog); err != nil {
		return err
	}

	helpers := map[string]any{
		"text": func(call goja.FunctionCall) goja.Value {
			appendOutput(stringify(call.Argument(0)) + "\n")
			return goja.Undefined()
		},
		"exit": func(goja.FunctionCall) goja.Value {
			panic(vm.NewGoError(errExit))
		},
		"store": func(call goja.FunctionCall) goja.Value {
			r.storeMu.Lock()
			r.stored[call.Argument(0).String()] = call.Argument(1).Export()
			r.storeMu.Unlock()
			return goja.Undefined()
		},
		"load": func(call goja.FunctionCall) goja.Value {
			r.storeMu.Lock()
			value, ok := r.stored[call.Argument(0).String()]
			r.storeMu.Unlock()
			if !ok {
				return goja.Undefined()
			}
			return vm.ToValue(value)
		},
	}
	for name, fn := range helpers {
		if err := vm.Set(name, fn); err != nil {
			return err
		}
	}
	return nil
}

// encodeArguments 把脚本传进来的参数变成工具要的 JSON。
func encodeArguments(vm *goja.Runtime, value goja.Value) (json.RawMessage, error) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return json.RawMessage(`{}`), nil
	}
	// 传字符串也收：模型有时会把参数写成 JSON 文本（codex 那边也是两种都认）。
	// 只有既不是对象、又不是一段对象 JSON 的，才报错。
	if text, ok := value.Export().(string); ok {
		trimmed := strings.TrimSpace(text)
		if strings.HasPrefix(trimmed, "{") && json.Valid([]byte(trimmed)) {
			return json.RawMessage(trimmed), nil
		}
		return nil, i18n.E("工具参数要传一个对象，收到的是字符串：{text}", "text", firstChars(text, 40))
	}
	encoded, err := json.Marshal(value.Export())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.D("参数没法转成 JSON"), err)
	}
	if len(encoded) == 0 || encoded[0] != '{' {
		return nil, i18n.E("工具参数必须是一个对象")
	}
	return encoded, nil
}

// toolValue 把工具返回的文本变成脚本里的值。
//
// 能解析成 JSON 的给对象（脚本可以直接 r.results），否则给字符串。这个分叉
// 是必要的，但它让模型反复栽在一个地方：拿到对象之后写 String(r) 或 "…" + r，
// 得到 "[object Object]"，再花两三轮才想明白。所以给出去的对象带一个不可枚举的
// toString，返回原始 JSON 文本——String(r)、模板字符串、字符串拼接都得到能读的
// 内容，而 JSON.stringify(r) 与取字段不受影响（toString 不可枚举，不会出现在序列化里）。
func toolValue(vm *goja.Runtime, result string) goja.Value {
	if _, isObject := decodeResult(result).(string); isObject {
		return vm.ToValue(result)
	}
	raw := strings.TrimSpace(result)
	// 用 VM 自己的 JSON.parse 建对象，而不是 vm.ToValue(map)：后者给出的是
	// 包着 Go map 的代理对象，在它上面定义属性不生效。
	parse, ok := goja.AssertFunction(vm.Get("JSON").ToObject(vm).Get("parse"))
	if !ok {
		return vm.ToValue(decodeResult(result))
	}
	value, err := parse(goja.Undefined(), vm.ToValue(raw))
	if err != nil {
		return vm.ToValue(result)
	}
	if object, isObject := value.(*goja.Object); isObject {
		// 参数顺序是 writable, configurable, enumerable——enumerable 必须是 FALSE，
		// 否则 Object.keys(r) 里会冒出 toString，而模型常拿 keys 判断返回有哪些字段。
		_ = object.DefineDataProperty("toString",
			vm.ToValue(func(goja.FunctionCall) goja.Value { return vm.ToValue(raw) }),
			goja.FLAG_FALSE, goja.FLAG_TRUE, goja.FLAG_FALSE)
	}
	return value
}

// decodeResult 尽量把工具返回的文本解析成对象，让脚本能直接取字段。
// 解不出来就原样给字符串——大量工具返回的本来就是纯文本。
func decodeResult(result string) any {
	trimmed := strings.TrimSpace(result)
	if trimmed == "" {
		return ""
	}
	if trimmed[0] != '{' && trimmed[0] != '[' {
		return result
	}
	var parsed any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return result
	}
	return parsed
}

// firstChars 截一小段用于报错，按 rune 切避免把中文劈开。
func firstChars(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}

// StripFences 去掉模型有时会加上的 markdown 代码围栏。
//
// 工具描述里写了「传原始 JavaScript，不要加围栏」，但模型照样会加——
// 它平时写代码就是那么写的。不处理的话第一行 ``` 就是语法错误，
// 而模型看到「语法错误」往往会去改自己的逻辑，改不到点子上。
func StripFences(code string) string {
	trimmed := strings.TrimSpace(code)
	if !strings.HasPrefix(trimmed, "```") {
		return code
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) < 2 {
		return code
	}
	lines = lines[1:]
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

func stringify(value goja.Value) string {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return ""
	}
	exported := value.Export()
	if text, ok := exported.(string); ok {
		return text
	}
	if encoded, err := json.Marshal(exported); err == nil {
		return string(encoded)
	}
	return value.String()
}

func describe(value goja.Value) string {
	if value == nil {
		return i18n.D("未知错误")
	}
	if object, ok := value.Export().(map[string]any); ok {
		if message, ok := object["message"].(string); ok {
			return message
		}
	}
	return value.String()
}

func isExit(value goja.Value) bool {
	return value != nil && strings.Contains(value.String(), errExit.Error())
}

// scriptError 把 goja 的错误翻成模型能据此改代码的话。
func scriptError(err error) error {
	var interrupted *goja.InterruptedError
	if errors.As(err, &interrupted) {
		return fmt.Errorf("%v", interrupted.Value())
	}
	var exception *goja.Exception
	if errors.As(err, &exception) {
		if isExit(exception.Value()) {
			return nil
		}
		// 带上 goja 的栈：模型要靠它定位自己写错的那一行。
		return i18n.E("脚本出错：{error}", "error", exception.String())
	}
	return fmt.Errorf("%s: %w", i18n.D("脚本无法执行"), err)
}
