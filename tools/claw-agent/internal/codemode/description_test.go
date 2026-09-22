package codemode

import (
	"encoding/json"
	"strings"
	"testing"
)

/*
exec 的描述——模型看到的全部「API 文档」。它替代的是原来那一长串工具定义，
所以两件事要同时成立：**说得清**（模型能照着写对代码）和**足够短**
（它每一轮都要重发一遍）。
*/

func tool(name, description, schema string) Tool {
	return Tool{Name: name, Description: description, Schema: json.RawMessage(schema)}
}

func TestIdentifiersAreValidJavaScript(t *testing.T) {
	// 工具名里有 - 和 __，直接写进 tools. 后面是语法错误。
	cases := map[string]string{
		"web-search__search": "web_search__search",
		// 中文字符本身不是合法标识符，会被替换成下划线再修掉两端。
		"数据-api":   "api",
		"sales_42": "sales_42",
		"9lives":   "lives",
	}
	for input, want := range cases {
		if got := Identifier(input); got != want {
			t.Errorf("Identifier(%q) = %q，期望 %q", input, got, want)
		}
	}
}

func TestCollidingNamesGetDistinctIdentifiers(t *testing.T) {
	// 撞名不处理的话，后一个会把前一个覆盖掉，而模型调用时**不会报错**
	// ——它调到了另一个工具。
	assigned := AssignIdentifiers([]Tool{
		tool("web-search", "", `{}`),
		tool("web_search", "", `{}`),
		tool("web.search", "", `{}`),
	})
	seen := map[string]bool{}
	for _, item := range assigned {
		if item.Ident == "" || seen[item.Ident] {
			t.Fatalf("标识符重复或为空：%+v", assigned)
		}
		seen[item.Ident] = true
	}
}

func TestDescriptionTeachesTheCallingConvention(t *testing.T) {
	text := Description(AssignIdentifiers([]Tool{
		tool("query_sales", "按日期查销售汇总", `{"type":"object","properties":{"date":{"type":"string"}},"required":["date"]}`),
	}))
	for _, want := range []string{"await tools.", "try/catch", "ALL_TOOLS", "tools.query_sales({ date: string })"} {
		if !strings.Contains(text, want) {
			t.Errorf("描述里缺 %q：\n%s", want, text)
		}
	}
	// 也要说清楚「别把中间结果全倒出来」——那是用它的全部理由。
	if !strings.Contains(text, "只把需要的结果交出去") {
		t.Errorf("没有强调只回必要结果：\n%s", text)
	}
}

func TestManyToolsAreNotAllDeclared(t *testing.T) {
	// 161 个接口全写签名，等于把原来那份工具清单又抄了一遍，省不下任何东西。
	many := make([]Tool, 0, 161)
	for i := 0; i < 161; i++ {
		many = append(many, tool(
			"op_"+string(rune('a'+i%26))+string(rune('0'+i/26)),
			"这是一个接口的说明，真实的说明还会更长一些，带着使用场景与注意事项",
			`{"type":"object","properties":{"id":{"type":"string"},"page":{"type":"integer"}}}`,
		))
	}
	text := Description(AssignIdentifiers(many))
	if !strings.Contains(text, "ALL_TOOLS 按名字或说明搜") {
		t.Errorf("没有引导模型去搜：\n%s", text[:400])
	}
	// 关键指标：整份描述要远小于把 161 个工具直挂的体积（实测约 110KB）。
	if len(text) > 12*1024 {
		t.Errorf("描述本身太大了：%d 字节", len(text))
	}
	t.Logf("161 个工具的描述共 %d 字节（直挂约 110KB）", len(text))
}

func TestLongToolDescriptionsAreTrimmed(t *testing.T) {
	// 服务那边的接口说明动辄几百字，还带着字面量的 \n。
	long := "阿里云联网搜索\\n调用阿里云信息查询服务（IQS）进行开放域联网搜索，" +
		strings.Repeat("还有很多说明文字。", 30)
	text := Description(AssignIdentifiers([]Tool{tool("web_search", long, `{}`)}))
	if strings.Contains(text, "还有很多说明文字。还有很多说明文字。还有很多说明文字。") {
		t.Errorf("长说明没有截断：\n%s", text)
	}
	if !strings.Contains(text, "阿里云联网搜索") {
		t.Errorf("第一行应当留着：\n%s", text)
	}
}
