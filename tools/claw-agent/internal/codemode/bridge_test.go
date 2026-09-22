package codemode

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

/*
`tools` 上取错名字的两条路。

真实案例：模型把描述里那行 `- tools.corpus__4(...)` 整串抄进了表，
然后 `tools[name]` 拿到 undefined，调用报「undefined is not a function」，
它 try/catch 收掉之后判定「语料库坏了」，又花三轮去试探。
*/

func namedTool(name string) Tool {
	return Tool{
		Name: name, Ident: Identifier(name), Description: "假的",
		Schema: json.RawMessage(`{"type":"object"}`),
		Call:   func(_ context.Context, _ json.RawMessage) (string, error) { return "命中 " + name, nil },
	}
}

func TestToolNameMayCarryTheToolsPrefix(t *testing.T) {
	// 模型的意思毫无歧义，认下来即可——它已经为此浪费过三轮。
	out, err := run(t, []Tool{namedTool("corpus__4")}, `
		const name = "tools.corpus__4";
		return await tools[name]({ keyword: "毛利率" });
	`)
	if err != nil {
		t.Fatalf("带前缀的名字应该照样能调用：%v", err)
	}
	if !strings.Contains(out, "命中 corpus__4") {
		t.Fatalf("没调到那个工具：%q", out)
	}
}

func TestUnknownToolNameSaysWhichNameIsWrong(t *testing.T) {
	// 关键是异常里出现那个名字：报「undefined is not a function」的话，
	// 模型会去查调用点，而错的是名字。
	_, err := run(t, []Tool{namedTool("corpus__4")}, `
		return await tools.corpus__99({});
	`)
	if err == nil {
		t.Fatal("调一个不存在的工具应该报错")
	}
	if !strings.Contains(err.Error(), "corpus__99") {
		t.Fatalf("报错里没说是哪个名字：%v", err)
	}
	if !strings.Contains(err.Error(), "corpus__4") {
		t.Fatalf("报错里应该给出最接近的候选：%v", err)
	}
}

func TestToolsObjectIsNotThenable(t *testing.T) {
	// `then` 不能当成工具名解释：返回一个函数的话，任何 await 到 tools
	// 的地方都会永远挂着，而那种挂起最难查。
	out, err := run(t, []Tool{namedTool("corpus__4")}, `
		const value = await tools;
		return typeof value;
	`)
	if err != nil {
		t.Fatalf("await tools 不该出错：%v", err)
	}
	if !strings.Contains(out, "object") {
		t.Fatalf("await tools 应该原样拿回那个对象：%q", out)
	}
}

func TestToolsObjectCanBeEnumerated(t *testing.T) {
	// 模型会写 `for (const name of Object.keys(tools))`。
	out, err := run(t, []Tool{namedTool("corpus__4"), namedTool("corpus__9")}, `
		return Object.keys(tools).sort().join(",");
	`)
	if err != nil {
		t.Fatalf("枚举 tools 不该出错：%v", err)
	}
	if out != "corpus__4,corpus__9" {
		t.Fatalf("枚举结果不对：%q", out)
	}
}
