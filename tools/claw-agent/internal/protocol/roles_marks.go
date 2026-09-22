package protocol

import (
	"strconv"
	"strings"
)

// 模型清单项的写法：`名字[#能力,能力][@上下文窗口]`。
//
//	qwen3-vl-plus#vision@131072
//	gpt-5.4#vision,image
//	deepseek-chat
//
// 为什么把元数据附在名字后面而不是换成对象数组：旧版的清单是一个纯字符串
// 数组，换结构就要迁移所有人的配置库；而 `#` 与 `@` 都不是模型名的合法字符，
// 解析没有歧义。两段都省掉的名字就是「只做对话、窗口未知」——那正是旧记录
// 的语义，所以旧清单原样可读。

// ParsedModel 是一条清单项拆开之后的样子。
type ParsedModel struct {
	Name  string
	Roles []ModelRole
	// Context 是上下文窗口（token）。0 表示不知道——那时内核只能等上游报
	// 「超出上下文」再被动压缩，白花一次请求。
	Context int
}

// ParseModelMark 把一条清单项拆成模型名、能力集与上下文窗口。
func ParseModelMark(entry string) ParsedModel {
	rest := strings.TrimSpace(entry)

	// 窗口先切：`@` 一定在最后一段，而模型名里不会有它。
	parsed := ParsedModel{}
	if head, tail, found := strings.Cut(rest, "@"); found {
		rest = head
		if value, err := strconv.Atoi(strings.TrimSpace(tail)); err == nil && value > 0 {
			parsed.Context = value
		}
	}

	name, marks, found := strings.Cut(rest, "#")
	parsed.Name = strings.TrimSpace(name)
	if !found {
		return parsed
	}
	seen := map[ModelRole]bool{}
	for _, mark := range strings.Split(marks, ",") {
		role := ModelRole(strings.ToLower(strings.TrimSpace(mark)))
		if role == "" || seen[role] || !validRole(role) {
			continue
		}
		seen[role] = true
		parsed.Roles = append(parsed.Roles, role)
	}
	return parsed
}

// FormatModelMark 把拆开的部分拼回一条清单项。
//
// 顺序固定（能力按 KnownRoles 排、窗口在最后），同一份配置每次写出来都一样——
// 否则每次同步都会让配置库无谓地变动。
func FormatModelMark(parsed ParsedModel) string {
	entry := strings.TrimSpace(parsed.Name)
	if entry == "" {
		return ""
	}
	marks := make([]string, 0, len(parsed.Roles))
	seen := map[ModelRole]bool{}
	for _, known := range KnownRoles() {
		for _, role := range parsed.Roles {
			if role == known && !seen[known] {
				seen[known] = true
				marks = append(marks, string(known))
			}
		}
	}
	if len(marks) > 0 {
		entry += "#" + strings.Join(marks, ",")
	}
	if parsed.Context > 0 {
		entry += "@" + strconv.Itoa(parsed.Context)
	}
	return entry
}

// ModelHasRole 报一条清单项带不带某个能力。
func ModelHasRole(entry string, role ModelRole) bool {
	for _, item := range ParseModelMark(entry).Roles {
		if item == role {
			return true
		}
	}
	return false
}

func validRole(role ModelRole) bool {
	for _, known := range KnownRoles() {
		if role == known {
			return true
		}
	}
	return false
}
