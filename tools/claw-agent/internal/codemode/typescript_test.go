package codemode

import (
	"encoding/json"
	"strings"
	"testing"
)

/*
JSON Schema → TypeScript。

这一层的价值是省 token：工具清单每次请求整份重发、不参与压缩，是开局就占掉的
固定地板。同样的信息，TS 声明比 JSON Schema 短一半以上。所以下面既钉正确性，
也钉「确实更短」。
*/

func ts(t *testing.T, raw string) string {
	t.Helper()
	return RenderType(json.RawMessage(raw))
}

func TestRendersOrdinaryShapes(t *testing.T) {
	cases := map[string]string{
		`{"type":"object","properties":{"date":{"type":"string"}},"required":["date"]}`: "{ date: string }",
		`{"type":"object","properties":{"limit":{"type":"integer"}}}`:                   "{ limit?: number }",
		`{"type":"array","items":{"type":"string"}}`:                                    "string[]",
		`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"]}`:    "{ ok: boolean }",
		`{"type":"string","enum":["24h","7d"]}`:                                         `"24h" | "7d"`,
	}
	for schema, want := range cases {
		if got := ts(t, schema); got != want {
			t.Errorf("schema %s\n  得到 %s\n  期望 %s", schema, got, want)
		}
	}
}

func TestOptionalAndRequiredAreDistinguished(t *testing.T) {
	// 必填与选填的区别要传达给模型，否则它要么漏参数、要么每次都把选填的填满。
	got := ts(t, `{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"}},"required":["a"]}`)
	if got != "{ a: string; b?: string }" {
		t.Errorf("得到 %s", got)
	}
}

func TestFieldsAreSortedSoTheDescriptionIsStable(t *testing.T) {
	// schema 里的字段顺序来自 map，每次不一样的话同一个工具在不同会话里
	// 描述都不同，白白让上游的 prompt cache 失效。
	schema := `{"type":"object","properties":{"z":{"type":"string"},"a":{"type":"string"},"m":{"type":"string"}}}`
	first := ts(t, schema)
	for i := 0; i < 20; i++ {
		if ts(t, schema) != first {
			t.Fatalf("同一份 schema 渲染出了不同结果：%s", first)
		}
	}
	if !strings.HasPrefix(first, "{ a?") {
		t.Errorf("应当按字段名排序：%s", first)
	}
}

func TestLongEnumsAreCappedButStillSayWhatTheyAre(t *testing.T) {
	// 一张一百列的表渲染成联合类型有几千字，而模型需要的只是「这里填列名」。
	names := make([]string, 0, 120)
	for i := 0; i < 120; i++ {
		names = append(names, `"col_`+string(rune('a'+i%26))+string(rune('0'+i/26))+`"`)
	}
	schema := `{"type":"string","enum":[` + strings.Join(names, ",") + `]}`
	got := ts(t, schema)
	if !strings.Contains(got, "| string") || !strings.Contains(got, "共 120 个取值") {
		t.Errorf("超长 enum 应当截断并说明总数：%s", got)
	}
	if len(got) > 800 {
		t.Errorf("截断之后还是太长：%d 字", len(got))
	}
}

func TestSelfReferencingSchemaDoesNotRecurseForever(t *testing.T) {
	// JSON Schema 允许结构套自己。无限递归的表现是栈溢出而不是报错——
	// 整个内核直接没了，最难查的那种。
	deep := `{"type":"object","properties":{"child":`
	for i := 0; i < 40; i++ {
		deep += `{"type":"object","properties":{"child":`
	}
	deep += `{"type":"string"}`
	for i := 0; i < 41; i++ {
		deep += `}}`
	}
	got := ts(t, deep)
	if got == "" {
		t.Error("不该渲染成空")
	}
}

func TestBrokenSchemaFallsBackInsteadOfBlowingUp(t *testing.T) {
	for _, raw := range []string{``, `not json`, `[]`, `null`} {
		if got := RenderType(json.RawMessage(raw)); got != "unknown" {
			t.Errorf("%q 应当退化成 unknown，得到 %s", raw, got)
		}
	}
}

func TestTypeScriptIsActuallyShorterThanTheSchema(t *testing.T) {
	// 这一层存在的全部理由。真实形状：一个数据 API 的入参，带列名与说明。
	schema := `{"type":"object","properties":{
      "table":{"type":"string","description":"要查的表名，必须是已授权的表"},
      "date":{"type":"string","description":"查询日期，格式 YYYY-MM-DD"},
      "columns":{"type":"array","items":{"type":"string"},"description":"要返回的列"},
      "limit":{"type":"integer","description":"最多返回多少行，默认 100"}
    },"required":["table","date"]}`
	rendered := RenderType(json.RawMessage(schema))
	if len(rendered)*2 > len(schema) {
		t.Errorf("没省下多少：schema %d 字，TS %d 字", len(schema), len(rendered))
	}
	t.Logf("schema %d 字 → TS %d 字：%s", len(schema), len(rendered), rendered)
}
