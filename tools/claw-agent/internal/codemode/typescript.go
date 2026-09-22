// Package codemode 把「一堆工具」换成「一个能写 JavaScript 的工具」。
//
// 为什么要这么做，见 description.go 顶上的说明。这个文件只管一件事：
// 把 JSON Schema 翻成 TypeScript 类型声明。
//
// 为什么要翻：工具清单每次请求都整份重发、而且不参与上下文压缩，是开局就
// 占掉的固定地板。同样一个「按日期查销售」的入参，JSON Schema 要写
// {"type":"object","properties":{"date":{"type":"string","description":…}}}，
// TypeScript 只要 { date: string }——实测能省掉一半以上。
package codemode

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// maxRenderedSchema 是单个类型渲染出来的上限。
//
// 超了就退化成 unknown：一个几千字的类型声明对模型没有帮助（它读不完也记不住），
// 而那几千字每一轮都要付钱。codex 那边同样是超限退化，不是截断——截断出来的
// 类型是**语法错误的**，比 unknown 更糟。
const maxRenderedSchema = 4096

// RenderType 把一份 JSON Schema 渲染成 TypeScript 类型。
func RenderType(schema json.RawMessage) string {
	if len(schema) == 0 {
		return "unknown"
	}
	var parsed any
	if err := json.Unmarshal(schema, &parsed); err != nil {
		return "unknown"
	}
	rendered := render(parsed, 0)
	if len(rendered) > maxRenderedSchema {
		return "unknown"
	}
	return rendered
}

// render 递归渲染。depth 挡住自引用的 schema——JSON Schema 允许 $ref 指回自己，
// 那会让这里无限递归下去（栈溢出而不是报错，最难查的那种）。
func render(node any, depth int) string {
	if depth > 8 {
		return "unknown"
	}
	schema, ok := node.(map[string]any)
	if !ok {
		return "unknown"
	}

	// enum 优先：它比 type 更具体，"a" | "b" 比 string 有用得多。
	if values, ok := schema["enum"].([]any); ok && len(values) > 0 {
		return renderEnum(values)
	}
	if variants, ok := schema["oneOf"].([]any); ok {
		return renderUnion(variants, depth)
	}
	if variants, ok := schema["anyOf"].([]any); ok {
		return renderUnion(variants, depth)
	}

	switch typeOf(schema) {
	case "object":
		return renderObject(schema, depth)
	case "array":
		items, ok := schema["items"]
		if !ok {
			return "unknown[]"
		}
		inner := render(items, depth+1)
		if strings.ContainsAny(inner, "|{") {
			return "(" + inner + ")[]"
		}
		return inner + "[]"
	case "string":
		return "string"
	case "integer", "number":
		return "number"
	case "boolean":
		return "boolean"
	case "null":
		return "null"
	default:
		return "unknown"
	}
}

// typeOf 取 type 字段。它可能是数组（["string","null"]），取第一个非 null 的。
func typeOf(schema map[string]any) string {
	switch value := schema["type"].(type) {
	case string:
		return value
	case []any:
		for _, item := range value {
			if text, ok := item.(string); ok && text != "null" {
				return text
			}
		}
	}
	// 没写 type 但有 properties 的，按对象处理——真实的 schema 里很常见。
	if _, ok := schema["properties"]; ok {
		return "object"
	}
	return ""
}

func renderObject(schema map[string]any, depth int) string {
	properties, ok := schema["properties"].(map[string]any)
	if !ok || len(properties) == 0 {
		return "Record<string, unknown>"
	}
	required := map[string]bool{}
	if list, ok := schema["required"].([]any); ok {
		for _, item := range list {
			if name, ok := item.(string); ok {
				required[name] = true
			}
		}
	}
	// 字段按名字排序：schema 里的顺序来自 map，每次不一样的话，
	// 同一个工具的描述会在不同会话里长得不同，白白让上游的缓存失效。
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)

	fields := make([]string, 0, len(names))
	for _, name := range names {
		optional := ""
		if !required[name] {
			optional = "?"
		}
		fields = append(fields, fmt.Sprintf("%s%s: %s", quoteKey(name), optional, render(properties[name], depth+1)))
	}
	return "{ " + strings.Join(fields, "; ") + " }"
}

func renderEnum(values []any) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		switch typed := value.(type) {
		case string:
			parts = append(parts, strconv.Quote(typed))
		case float64:
			parts = append(parts, strconv.FormatFloat(typed, 'g', -1, 64))
		case bool:
			parts = append(parts, strconv.FormatBool(typed))
		case nil:
			parts = append(parts, "null")
		}
	}
	if len(parts) == 0 {
		return "unknown"
	}
	// enum 太长时只留前几个再加 string：一张一百列的表名清单渲染成联合类型
	// 有几千字，而模型需要的是「这里填列名」这个事实。
	const maxEnum = 24
	if len(parts) > maxEnum {
		return strings.Join(parts[:maxEnum], " | ") + fmt.Sprintf(" | string /* 共 %d 个取值 */", len(parts))
	}
	return strings.Join(parts, " | ")
}

func renderUnion(variants []any, depth int) string {
	parts := make([]string, 0, len(variants))
	seen := map[string]bool{}
	for _, variant := range variants {
		rendered := render(variant, depth+1)
		if seen[rendered] {
			continue
		}
		seen[rendered] = true
		parts = append(parts, rendered)
	}
	if len(parts) == 0 {
		return "unknown"
	}
	return strings.Join(parts, " | ")
}

// quoteKey 给不是合法标识符的字段名加引号。
func quoteKey(name string) string {
	if name == "" {
		return `""`
	}
	for index, char := range name {
		valid := char == '_' || char == '$' ||
			(char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(index > 0 && char >= '0' && char <= '9')
		if !valid {
			return strconv.Quote(name)
		}
	}
	return name
}
