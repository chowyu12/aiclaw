// Package i18n 是内核里「给人看的文字」的国际化：执行步骤的提示、审批框的标题与说明、
// 步骤摘要、返回给界面的报错。与桌面端 apps/desktop/src/shared/i18n.ts 同一套做法：
//
//   - **以中文原文为键**：写 i18n.T(locale, "已中断")，英文放在 en_*.go；源码照旧读得懂。
//   - 漏翻的回退成中文，不会在界面上露出一串键名。
//   - 参数用 {name} 占位：i18n.T(locale, "子 agent {path} 做完了", "path", node.path)。
//   - i18n_test.go 会扫一遍内核源码，所有 i18n.T / D / E 的中文键都要有英文。
//
// 发给模型的文字（系统提示词、工具说明）不走这里：那部分直接用英文写。
package i18n

import (
	"errors"
	"strings"
	"sync/atomic"
)

// 语言。与桌面端的取值一致。
const (
	English = "en"
	Chinese = "zh-CN"
)

// Normalize 只认两种取值，其余一律当英文（桌面端新装默认英文）。
func Normalize(locale string) string {
	if locale == Chinese {
		return Chinese
	}
	return English
}

// fallback 是和会话无关的文字（插件配置、邮箱测试的报错）用的语言：桌面端在启动与
// 切换语言时告诉内核（config/locale）。没收到之前按中文——内核单独跑（测试、命令行）时
// 与从前的行为一致。
var fallback atomic.Value

func init() { fallback.Store(Chinese) }

// SetDefault 设和会话无关的文字用的语言。
func SetDefault(locale string) { fallback.Store(Normalize(locale)) }

// Default 是和会话无关的文字用的语言。
func Default() string { return fallback.Load().(string) }

// T 按语言翻译一条文字并填参数。args 是成对的「名字, 值」。locale 为空时用 Default()。
func T(locale, source string, args ...any) string {
	if locale == "" {
		locale = Default()
	}
	text := source
	if Normalize(locale) == English {
		if translated, ok := en[source]; ok {
			text = translated
		}
	}
	for i := 0; i+1 < len(args); i += 2 {
		name, _ := args[i].(string)
		text = strings.ReplaceAll(text, "{"+name+"}", toString(args[i+1]))
	}
	return text
}

// D 用和会话无关的默认语言翻译。
func D(source string, args ...any) string { return T("", source, args...) }

// E 是 errors.New(D(…))：返回给界面的报错大多是这一种。要包一层原始错误时写
// fmt.Errorf("%s: %w", i18n.D("…"), err)。
func E(source string, args ...any) error { return errors.New(D(source, args...)) }

// en 是英文词典：中文原文 → 英文。按模块分文件（en_*.go），各自在 init 里 register。
var en = map[string]string{}

// register 并入一组词条。同一个键两处都写了、而英文不一样，说明同一句中文在两个地方
// 该有两种说法——这是写错了，直接 panic，测试第一时间就能发现。
func register(entries map[string]string) {
	for source, target := range entries {
		if existing, ok := en[source]; ok && existing != target {
			panic("i18n: 词条重复且英文不一致：" + source)
		}
		en[source] = target
	}
}

func toString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case error:
		return v.Error()
	case interface{ String() string }:
		return v.String()
	}
	return strings.TrimSpace(sprint(value))
}
