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
	builder.WriteString(`执行一段 JavaScript 来调用工具并组合结果。

- 代码在一个隔离的 JS 环境里跑：没有 require、没有文件系统、没有网络、没有 console，
  **也没有 setTimeout 之类的定时器**（需要等待就直接 await 工具，别自己造轮询）。
  能用的只有下面列出的工具与辅助函数。需要读写文件或执行命令时，用对应的工具。
- 直接给 JavaScript 源码，不要包 markdown 代码围栏。
- 所有工具挂在全局 tools 上，都返回 Promise：const r = await tools.某个工具({ ... })。
  参数传一个对象。返回值是对象时可以直接取字段，否则是字符串。
- 名字放进变量时写 tools[名字]，**名字里不带 tools. 前缀**——下面列的是调用写法
  tools.某个工具(...)，其中工具名只是「某个工具」那一段。
- 工具失败会抛异常，可以用 try/catch 接住并改用别的做法。
- **只把需要的结果交出去**：中间数据留在脚本里，不要整份 text() 出来。
  这正是用它的理由——十几次查询只回最后那几行。

辅助函数：
- text(值)：追加一行输出。对象会被 JSON 化。
- exit()：立刻结束（相当于提前 return）。
- store(键, 值) / load(键)：在同一会话的多次执行之间存取数据。
- ALL_TOOLS：[{ name, description }]，可以自己 filter 找工具。

写法示例：
  const rows = [];
  for (const table of ["a", "b"]) {
    const result = await tools.某个查询工具({ table });
    rows.push(table + "=" + result.count);
  }
  return rows.join(", ");

`)

	sorted := append([]Tool(nil), tools...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Ident < sorted[j].Ident })

	builder.WriteString(fmt.Sprintf("可用工具（共 %d 个）：\n", len(sorted)))
	for index, tool := range sorted {
		if index >= maxDeclared {
			builder.WriteString(fmt.Sprintf(
				"…还有 %d 个没有列出签名，用 ALL_TOOLS 按名字或说明搜，再直接调用。\n",
				len(sorted)-maxDeclared,
			))
			break
		}
		summary := firstLine(tool.Description)
		builder.WriteString(fmt.Sprintf("- tools.%s(%s)", tool.Ident, RenderType(tool.Schema)))
		if summary != "" {
			builder.WriteString(" — " + summary)
		}
		builder.WriteString("\n")
	}
	return builder.String()
}

// firstLine 取描述的第一行并截断。
//
// 工具描述动辄几百字（内部平台那边的接口说明带着使用场景），全铺进来就等于把
// 原来那份工具清单又抄了一遍，省不下任何东西。
func firstLine(text string) string {
	// **先按字面量的 \n 切，再按真换行切。** 内部平台存的说明里换行是转义过的
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
