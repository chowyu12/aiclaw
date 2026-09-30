package codemode

import (
	"fmt"
	"sort"
	"strings"
)

/*
`exec` 这个工具的描述——也就是模型看到的全部「API 文档」。

它替代的是原来那一长串工具定义。内容分三段：怎么写脚本、有哪些辅助函数、
有哪些工具（TypeScript 声明）。工具太多时只列前面一部分，剩下的让模型自己
去 `ALL_TOOLS` 里搜——那一栏只有名字和一句话，比完整签名便宜得多。
*/

// maxDeclared 是描述里最多写几个工具的完整签名。
//
// 超过之后只给名字与一句话。这个数字按「一屏能看完」定：写太多，模型在做
// 一件与九成工具无关的事时，也要每一轮都把它们重读一遍。
const maxDeclared = 40

// Identifier 把工具名规范成合法的 JS 标识符。
//
// 工具名里有 `-`（web-search）和 `__`（server 前缀），直接写进 `tools.` 后面
// 是语法错误。规范化必须是**确定的**：同一个工具每次都得到同一个名字，
// 否则模型上一轮记下的名字下一轮就调不通了。
func Identifier(name string) string {
	var builder strings.Builder
	for index, char := range name {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char == '_':
			builder.WriteRune(char)
		case char >= '0' && char <= '9' && index > 0:
			builder.WriteRune(char)
		default:
			builder.WriteByte('_')
		}
	}
	result := strings.Trim(builder.String(), "_")
	if result == "" {
		return "tool"
	}
	return result
}

// AssignIdentifiers 给一组工具分配脚本里用的名字，撞名的加后缀。
//
// 撞名是真会发生的：`web-search__x` 与 `web_search__x` 规范化之后一样。
// 不处理的话后一个会把前一个覆盖掉，而模型调用时**不会报错**——它调到了
// 另一个工具。
func AssignIdentifiers(tools []Tool) []Tool {
	used := map[string]bool{}
	out := make([]Tool, 0, len(tools))
	for _, tool := range tools {
		ident := Identifier(tool.Name)
		if used[ident] {
			for suffix := 2; ; suffix++ {
				candidate := fmt.Sprintf("%s_%d", ident, suffix)
				if !used[candidate] {
					ident = candidate
					break
				}
			}
		}
		used[ident] = true
		tool.Ident = ident
		out = append(out, tool)
	}
	return out
}

// Description 组出 exec 工具的描述。
func Description(tools []Tool) string {
	var builder strings.Builder
	builder.WriteString(`Run a snippet of JavaScript that calls tools and combines their results.

- The code runs in an isolated JS environment: no require, no file system, no network, no console,
  **and no timers such as setTimeout** (to wait, just await the tool; don't build your own polling).
  Only the tools and helpers listed below are available. To read or write files or run commands, use the corresponding tools.
- Pass raw JavaScript source; don't wrap it in markdown code fences.
- Every tool lives on the global tools object and returns a Promise: const r = await tools.some_tool({ ... }).
  Pass the arguments as one object.
- Return values: if a tool outputs JSON you get an object/array (use r.results directly); otherwise a string. String(r) and
  string concatenation give the raw JSON text; to .slice it, JSON.stringify(r) first. run_command returns a string with no .stdout.
- When the name is in a variable, write tools[name], **without the tools. prefix in the name** — the list below shows the call form
  tools.some_tool(...), where the tool name is only the "some_tool" part.
- A failing tool throws an exception; catch it with try/catch and try another approach.
- **Return only the results you need**: keep intermediate data inside the script instead of text()-ing all of it.
  That is the whole point of exec — a dozen queries, and only the last few lines come back.

Helpers:
- text(value): append a line of output. Objects are JSON-encoded.
- exit(): stop immediately (like an early return).
- store(key, value) / load(key): save and load data across runs within the same chat.
- ALL_TOOLS: [{ name, description }]; filter it yourself to find tools.

Example:
  const rows = [];
  for (const table of ["a", "b"]) {
    const result = await tools.some_query_tool({ table });
    rows.push(table + "=" + result.count);
  }
  return rows.join(", ");

`)

	sorted := append([]Tool(nil), tools...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Ident < sorted[j].Ident })

	builder.WriteString(fmt.Sprintf("Available tools (%d in total):\n", len(sorted)))
	for index, tool := range sorted {
		if index >= maxDeclared {
			break
		}
		summary := firstLine(tool.Description)
		builder.WriteString(fmt.Sprintf("- tools.%s(%s)", tool.Ident, RenderType(tool.Schema)))
		if summary != "" {
			builder.WriteString(" — " + summary)
		}
		builder.WriteString("\n")
	}
	if hidden := sorted[min(maxDeclared, len(sorted)):]; len(hidden) > 0 {
		// 没列签名的那部分要**露出名字**，而且要匀着取样。模型不会去搜一个它不知道
		// 存在的东西：只写「还有 101 个」，它就断定没有；而按字母序取前几个，看到的
		// 全是同一个前缀（一串「企业 xx 查询」），另一头的整块工具照样看不见。
		builder.WriteString(fmt.Sprintf(
			"…and %d more without signatures listed, e.g. %s. Search ALL_TOOLS by name or description to find the full name, then call it directly.\n",
			len(hidden), strings.Join(sampleIdents(hidden, sampledHidden), ", "),
		))
	}
	return builder.String()
}

// sampledHidden 是没列签名的工具里举几个名字。
const sampledHidden = 10

// sampleIdents 在一串排好序的工具里均匀取 n 个名字，首尾都取到。
func sampleIdents(tools []Tool, n int) []string {
	if len(tools) <= n {
		names := make([]string, 0, len(tools))
		for _, tool := range tools {
			names = append(names, "tools."+tool.Ident)
		}
		return names
	}
	names := make([]string, 0, n)
	for i := 0; i < n; i++ {
		index := i * (len(tools) - 1) / (n - 1)
		names = append(names, "tools."+tools[index].Ident)
	}
	return names
}

// firstLine 取描述的第一行并截断。
//
// 工具描述动辄几百字（服务那边的接口说明带着使用场景），全铺进来就等于把
// 原来那份工具清单又抄了一遍，省不下任何东西。
func firstLine(text string) string {
	// **先按字面量的 \n 切，再按真换行切。** 服务存的说明里换行是转义过的
	// 两个字符，只按真换行切的话第一行就是整段几百字。
	line := text
	if index := strings.Index(line, `\n`); index >= 0 {
		line = line[:index]
	}
	if index := strings.IndexAny(line, "\n\r"); index >= 0 {
		line = line[:index]
	}
	line = strings.TrimSpace(line)
	const limit = 90
	if len([]rune(line)) > limit {
		return string([]rune(line)[:limit]) + "…"
	}
	return line
}
