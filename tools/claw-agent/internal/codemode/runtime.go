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
	tools  []Tool
	limits Limits
	// stored 是脚本之间共享的键值，给 store()/load() 用。
	// 一段脚本查出来的东西，下一段还能接着用，不必重新查一遍。
	//
	// **必须加锁**：exec 标的是只读，所以模型一次给出两个 exec 调用时，
	// 内核会并行执行它们，而它们共用这一张表。Go 里并发写 map 不是竞态
	// 那么简单——是直接 fatal，整个内核没了。
	storeMu sync.Mutex
	stored  map[string]any
}

func New(tools []Tool, limits Limits) *Runtime {
	return &Runtime{tools: tools, limits: limits, stored: map[string]any{}}
}

// errExit 是 exit() 抛出来的哨兵，表示「正常提前结束」。
var errExit = errors.New("codemode: exit")

// Run 执行一段脚本，返回给模型看的输出。
func (r *Runtime) Run(ctx context.Context, source string) (string, error) {
	vm := goja.New()
	// 严格模式下不允许隐式全局变量。模型手写的脚本里 `x = 1` 这种笔误很常见，
	// 严格模式会直接报错，比默默建个全局变量好查。
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))

	var output strings.Builder
	truncated := false
	appendOutput := func(text string) {
		if truncated {
			return
		}
		if output.Len()+len(text) > r.limits.MaxOutput {
			output.WriteString(text[:max(0, r.limits.MaxOutput-output.Len())])
			output.WriteString("\n…（输出过长已截断）")
			truncated = true
			return
		}
		output.WriteString(text)
	}

	var calls atomic.Int64
	// toolNanos 是**已经完成**的工具调用累计耗时；inflight 是当前那一次的
	// 开始时刻（没有就是 0）。两个都要：只记已完成的话，一次正在等审批的
	// 调用期间看门狗看到的就是「脚本跑了很久」，于是把它打断——
	// 凡是需要确认的脚本都会这么死掉。
	var toolNanos atomic.Int64
	var inflight atomic.Int64

	if err := r.install(ctx, vm, appendOutput, &calls, &toolNanos, &inflight); err != nil {
		return "", err
	}

	// 看门狗：只按「脚本自己跑的时间」算，等工具的时间不算。
	done := make(chan struct{})
	defer close(done)
	go func() {
		started := time.Now()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				vm.Interrupt("上层已取消")
				return
			case <-ticker.C:
				waiting := time.Duration(0)
				if at := inflight.Load(); at != 0 {
					waiting = time.Since(time.Unix(0, at))
				}
				spent := time.Since(started) - time.Duration(toolNanos.Load()) - waiting
				if spent > r.limits.Budget {
					vm.Interrupt(fmt.Sprintf("脚本运行超过 %s，已中断", r.limits.Budget))
					return
				}
			}
		}
	}()

	// 包成异步立即执行函数：这样顶层就能 await，而模型写的就是这个形态。
	wrapped := "(async () => {\n" + StripFences(source) + "\n})()"
	value, err := vm.RunString(wrapped)
	if err != nil {
		return output.String(), scriptError(err)
	}

	promise, ok := value.Export().(*goja.Promise)
	if !ok {
		// 理论上不会：上面包成了 async 函数。真出现就如实说，别装作成功。
		return output.String(), errors.New("脚本没有返回预期的结果")
	}
	switch promise.State() {
	case goja.PromiseStateRejected:
		reason := promise.Result()
		if isExit(reason) {
			return finish(output.String(), calls.Load()), nil
		}
		return output.String(), fmt.Errorf("脚本抛出异常：%s", describe(reason))
	case goja.PromiseStatePending:
		// await 了一个永远不会完成的东西。说清楚，别让模型以为是工具的问题。
		return output.String(), errors.New("脚本结束时仍有未完成的等待，可能 await 了一个不会返回的东西")
	}

	if result := promise.Result(); result != nil && !goja.IsUndefined(result) && !goja.IsNull(result) {
		appendOutput(stringify(result))
	}
	return finish(output.String(), calls.Load()), nil
}

func finish(text string, calls int64) string {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return fmt.Sprintf("（脚本执行完成，没有输出；调用了 %d 次工具）", calls)
	}
	return text
}

// install 把 tools 与几个辅助函数挂进 VM。
func (r *Runtime) install(
	ctx context.Context,
	vm *goja.Runtime,
	appendOutput func(string),
	calls *atomic.Int64,
	toolNanos *atomic.Int64,
	inflight *atomic.Int64,
) error {
	bridge := newToolBridge(vm)
	catalog := make([]any, 0, len(r.tools))

	for _, tool := range r.tools {
		tool := tool
		catalog = append(catalog, map[string]any{"name": tool.Ident, "description": tool.Description})
		bridge.add(tool.Ident, vm.ToValue(func(call goja.FunctionCall) goja.Value {
			promise, resolve, reject := vm.NewPromise()

			if calls.Add(1) > int64(r.limits.MaxCalls) {
				_ = reject(vm.ToValue(fmt.Sprintf("工具调用次数超过上限 %d", r.limits.MaxCalls)))
				return vm.ToValue(promise)
			}

			arguments, err := encodeArguments(vm, call.Argument(0))
			if err != nil {
				_ = reject(vm.ToValue(err.Error()))
				return vm.ToValue(promise)
			}

			// 等工具的时间不算进脚本预算：里面可能在等用户点审批。
			started := time.Now()
			inflight.Store(started.UnixNano())
			result, callErr := tool.Call(ctx, arguments)
			inflight.Store(0)
			toolNanos.Add(int64(time.Since(started)))

			if callErr != nil {
				// 工具失败**在脚本里是个异常**，模型可以 try/catch 接住换个做法——
				// 这正是写代码比一次次单独调用强的地方。
				_ = reject(vm.ToValue(callErr.Error()))
				return vm.ToValue(promise)
			}
			_ = resolve(toolValue(vm, result))
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
		return nil, fmt.Errorf("工具参数要传一个对象，收到的是字符串：%s", firstChars(text, 40))
	}
	encoded, err := json.Marshal(value.Export())
	if err != nil {
		return nil, fmt.Errorf("参数没法转成 JSON：%w", err)
	}
	if len(encoded) == 0 || encoded[0] != '{' {
		return nil, errors.New("工具参数必须是一个对象")
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
		_ = object.DefineDataProperty("toString",
			vm.ToValue(func(goja.FunctionCall) goja.Value { return vm.ToValue(raw) }),
			goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE)
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
		return "未知错误"
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
		return fmt.Errorf("脚本出错：%s", exception.String())
	}
	return fmt.Errorf("脚本无法执行：%w", err)
}
