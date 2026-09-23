package codemode

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

/*
脚本运行时。

这一层的价值在两处，测的也是这两处：
  - **组合调用不经过模型**：一段循环里调十几次工具，只有最后几行进上下文；
  - **失败是脚本里的异常**：模型能 try/catch 接住换个做法，而不是每错一次
    就回到模型再想一轮。

另外几条是「不能让它把内核拖垮」：死循环、调用次数、输出体积。
*/

func fakeTool(name string, fn func(json.RawMessage) (string, error)) Tool {
	return Tool{
		Name: name, Ident: Identifier(name), Description: "假的",
		Schema: json.RawMessage(`{"type":"object"}`),
		Call: func(_ context.Context, arguments json.RawMessage) (string, error) {
			return fn(arguments)
		},
	}
}

func run(t *testing.T, tools []Tool, source string) (string, error) {
	t.Helper()
	return New(tools, DefaultLimits()).Run(context.Background(), source)
}

func TestLoopsOverToolsAndOnlyReturnsWhatItWants(t *testing.T) {
	// 这就是 code mode 存在的理由：12 份完整返回留在脚本里，
	// 进上下文的只有最后那一行。
	tool := fakeTool("query_table", func(arguments json.RawMessage) (string, error) {
		var args struct {
			Table string `json:"table"`
		}
		_ = json.Unmarshal(arguments, &args)
		// 假装每份返回都很大。
		return fmt.Sprintf(`{"table":%q,"rows":%d,"payload":%q}`,
			args.Table, len(args.Table), strings.Repeat("x", 2000)), nil
	})

	out, err := run(t, []Tool{tool}, `
		const tables = ["a", "bb", "ccc"];
		let total = 0;
		for (const table of tables) {
			const result = await tools.query_table({ table });
			total += result.rows;
		}
		return "总行数 " + total;
	`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out != "总行数 6" {
		t.Errorf("输出 = %q", out)
	}
	if strings.Contains(out, "xxxx") {
		t.Error("中间结果不该进输出")
	}
}

func TestToolFailureIsCatchableInsideTheScript(t *testing.T) {
	// 失败在脚本里是异常，模型可以接住换个做法——这正是写代码比逐次调用强的地方。
	tool := fakeTool("flaky", func(arguments json.RawMessage) (string, error) {
		if strings.Contains(string(arguments), "bad") {
			return "", fmt.Errorf("这个参数不行")
		}
		return `{"ok":true}`, nil
	})
	out, err := run(t, []Tool{tool}, `
		try {
			await tools.flaky({ mode: "bad" });
			return "不该走到这里";
		} catch (error) {
			const retry = await tools.flaky({ mode: "good" });
			return "第一次失败：" + error + "；重试 ok=" + retry.ok;
		}
	`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "这个参数不行") || !strings.Contains(out, "ok=true") {
		t.Errorf("输出 = %q", out)
	}
}

func TestTextAndReturnBothReachTheModel(t *testing.T) {
	out, err := run(t, nil, `
		text("第一行");
		text({ a: 1 });
		return "最后一行";
	`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "第一行\n{\"a\":1}\n最后一行" {
		t.Errorf("输出 = %q", out)
	}
}

func TestInfiniteLoopIsInterrupted(t *testing.T) {
	// 死循环不能把内核卡死。这是把「模型写的代码」放进来之后最直接的风险。
	runtime := New(nil, Limits{Budget: 300 * time.Millisecond, MaxCalls: 10, MaxOutput: 1024})
	started := time.Now()
	_, err := runtime.Run(context.Background(), `while (true) {}`)
	if err == nil {
		t.Fatal("死循环应当被中断")
	}
	if !strings.Contains(err.Error(), "超过") {
		t.Errorf("要说清是超时：%v", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Errorf("中断来得太慢：%s", elapsed)
	}
}

func TestWaitingForApprovalDoesNotCountAgainstTheScriptBudget(t *testing.T) {
	// 工具里可能在等用户点审批（最长半小时）。把那段算进脚本预算的话，
	// 凡是需要确认的脚本都会被判超时。
	slow := fakeTool("slow", func(json.RawMessage) (string, error) {
		time.Sleep(600 * time.Millisecond)
		return `{"ok":true}`, nil
	})
	runtime := New([]Tool{slow}, Limits{Budget: 300 * time.Millisecond, MaxCalls: 10, MaxOutput: 1024})
	out, err := runtime.Run(context.Background(), `
		await tools.slow({});
		return "熬过来了";
	`)
	if err != nil {
		t.Fatalf("等工具的时间不该算超时：%v", err)
	}
	if out != "熬过来了" {
		t.Errorf("输出 = %q", out)
	}
}

func TestCallCountIsCapped(t *testing.T) {
	var count atomic.Int64
	tool := fakeTool("ping", func(json.RawMessage) (string, error) {
		count.Add(1)
		return `{}`, nil
	})
	runtime := New([]Tool{tool}, Limits{Budget: 5 * time.Second, MaxCalls: 5, MaxOutput: 1024})
	_, err := runtime.Run(context.Background(), `
		for (let i = 0; i < 100; i++) { await tools.ping({}); }
		return "跑完了";
	`)
	if err == nil {
		t.Fatal("超过上限应当失败")
	}
	if count.Load() > 5 {
		t.Errorf("上限之后还在真的调用：%d 次", count.Load())
	}
}

func TestOutputIsCapped(t *testing.T) {
	runtime := New(nil, Limits{Budget: 5 * time.Second, MaxCalls: 10, MaxOutput: 200})
	out, err := runtime.Run(context.Background(), `
		for (let i = 0; i < 1000; i++) { text("这一行有点长，用来把输出撑满"); }
		return "结束";
	`)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > 400 {
		t.Errorf("输出没有被截住：%d 字节", len(out))
	}
	if !strings.Contains(out, "已截断") {
		t.Errorf("截断要说出来：%q", out)
	}
}

func TestSyntaxErrorTellsTheModelWhereItIs(t *testing.T) {
	// 脚本是模型写的，它得能据此改自己的代码。
	_, err := run(t, nil, `const x = ;`)
	if err == nil {
		t.Fatal("语法错误应当报错")
	}
	if !strings.Contains(err.Error(), "脚本") {
		t.Errorf("报错要说清是脚本的问题：%v", err)
	}
}

func TestStoreAndLoadSurviveBetweenScripts(t *testing.T) {
	// 一段脚本查出来的东西，下一段不必重新查一遍。
	runtime := New(nil, DefaultLimits())
	if _, err := runtime.Run(context.Background(), `store("tables", ["a","b"]); return "存好了";`); err != nil {
		t.Fatal(err)
	}
	out, err := runtime.Run(context.Background(), `return "读到 " + load("tables").length + " 个";`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "读到 2 个" {
		t.Errorf("输出 = %q", out)
	}
}

func TestAllToolsIsBrowsable(t *testing.T) {
	// 工具多的时候描述里不会全列出来，模型要能自己在 ALL_TOOLS 里找。
	tools := []Tool{
		fakeTool("sales_daily", func(json.RawMessage) (string, error) { return "{}", nil }),
		fakeTool("hr_headcount", func(json.RawMessage) (string, error) { return "{}", nil }),
	}
	out, err := run(t, tools, `
		return ALL_TOOLS.filter((t) => t.name.startsWith("sales")).map((t) => t.name).join(",");
	`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "sales_daily" {
		t.Errorf("输出 = %q", out)
	}
}

func TestScriptHasNoHostCapabilities(t *testing.T) {
	// 脚本是模型写的，而模型读到的东西可能被注入。它能碰到的只该是我们
	// 显式挂上去的那几个函数——没有 require、没有 fs、没有网络。
	for _, probe := range []string{"require", "process", "fetch", "globalThis.XMLHttpRequest"} {
		out, err := run(t, nil, fmt.Sprintf(`return typeof %s;`, probe))
		if err != nil {
			t.Fatalf("%s: %v", probe, err)
		}
		if out != "undefined" {
			t.Errorf("%s 不该存在，得到 %q", probe, out)
		}
	}
}

// ---------- review 时补的几条 ----------

func TestParallelScriptsDoNotCorruptTheStore(t *testing.T) {
	// exec 标的是只读，所以模型一次给出两个 exec 调用时内核会并行执行它们，
	// 而它们共用同一张 store 表。Go 里并发写 map 不是竞态那么简单——
	// 是直接 fatal，整个内核没了。（用 -race 跑时这条才有意义。）
	runtime := New(nil, DefaultLimits())
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, err := runtime.Run(context.Background(),
				fmt.Sprintf(`store("k%d", %d); return load("k%d");`, n, n, n))
			if err != nil {
				t.Errorf("第 %d 个脚本失败：%v", n, err)
			}
		}(i)
	}
	wg.Wait()
}

func TestMarkdownFencesAreStripped(t *testing.T) {
	// 工具描述里写了「别加围栏」，但模型照样会加——它平时写代码就那么写。
	// 不处理的话第一行 ``` 就是语法错误，而模型看到语法错误往往会去改
	// 自己的逻辑，改不到点子上。
	for _, source := range []string{
		"```js\nreturn 1 + 1;\n```",
		"```javascript\nreturn 1 + 1;\n```",
		"```\nreturn 1 + 1;\n```",
	} {
		out, err := run(t, nil, source)
		if err != nil {
			t.Errorf("%q: %v", source, err)
			continue
		}
		if out != "2" {
			t.Errorf("%q 得到 %q", source, out)
		}
	}
	// 正常代码里出现的 ``` 不能被动（比如写进文件的内容）。
	out, err := run(t, nil, "const md = \"```\"; return md.length;")
	if err != nil || out != "3" {
		t.Errorf("正常代码被改坏了：%q %v", out, err)
	}
}

func TestToolArgumentsMayBeAJsonString(t *testing.T) {
	// 模型有时把参数写成 JSON 文本而不是对象。codex 那边两种都认。
	var got string
	tool := fakeTool("echo", func(arguments json.RawMessage) (string, error) {
		got = string(arguments)
		return `{"ok":true}`, nil
	})
	if _, err := run(t, []Tool{tool}, `await tools.echo('{"a":1}'); return "ok";`); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got != `{"a":1}` {
		t.Errorf("工具收到的参数 = %s", got)
	}

	// 不是对象的字符串要报清楚，别让工具收到一段它不认识的东西。
	_, err := run(t, []Tool{tool}, `try { await tools.echo("随便一句话"); return "没报错"; } catch (e) { return "" + e; }`)
	if err != nil {
		t.Fatal(err)
	}
}

func TestTimersAreDocumentedAsAbsent(t *testing.T) {
	// goja 没有定时器。脚本用了会抛错，所以描述里必须提前说清楚，
	// 否则模型会写出一个「轮询等待」然后在第一行就挂掉。
	out, err := run(t, nil, `return typeof setTimeout;`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "undefined" {
		t.Skip("这个 goja 版本带了定时器，描述可以相应放开")
	}
	if !strings.Contains(Description(nil), "没有 setTimeout") {
		t.Error("描述里要说明没有定时器")
	}
}

// 工具返回 JSON 时脚本拿到对象；但 String(r) 不能是 "[object Object]"——
// 两个真实会话里模型都在这里白跑了几轮。
func TestJsonResultsStringifyToTheirSource(t *testing.T) {
	tools := []Tool{
		fakeTool("search", func(json.RawMessage) (string, error) {
			return `{"results":[{"title":"甲"},{"title":"乙"}]}`, nil
		}),
		fakeTool("shell", func(json.RawMessage) (string, error) { return "plain output", nil }),
	}
	out, err := run(t, tools, `
		const r = await tools.search({});
		const s = await tools.shell({});
		text(r.results.length);
		text(String(r).slice(0, 12));
		text(`+"`${r}`"+`.startsWith("{"));
		text(JSON.stringify(r).includes("toString"));
		text(Object.keys(r).join(","));
		text(typeof s + " " + s.length);
	`)
	if err != nil {
		t.Fatal(err)
	}
	want := "2\n{\"results\":[\ntrue\nfalse\nresults\nstring 12"
	if out != want {
		t.Errorf("输出 = %q，想要 %q", out, want)
	}
}
