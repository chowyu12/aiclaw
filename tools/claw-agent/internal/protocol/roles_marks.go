package protocol

import "strings"

// 模型清单里的能力标记：`qwen3-vl-plus#vision,image`。
//
// 为什么附在名字后面而不是换成对象数组：旧版的清单是一个纯字符串数组，
// 换结构就要迁移所有人的配置库；而 `#` 不是模型名的合法字符，解析没有歧义。
// 没有 `#` 的名字就是「只做对话」——这正是旧记录的语义。

// ParseModelMark 把一条清单项拆成模型名与能力集。
func ParseModelMark(entry string) (string, []ModelRole) {
	name, marks, found := strings.Cut(strings.TrimSpace(entry), "#")
	name = strings.TrimSpace(name)
	if !found {
		return name, nil
	}
	var roles []ModelRole
	seen := map[ModelRole]bool{}
	for _, mark := range strings.Split(marks, ",") {
		role := ModelRole(strings.ToLower(strings.TrimSpace(mark)))
		if role == "" || seen[role] || !validRole(role) {
			continue
		}
		seen[role] = true
		roles = append(roles, role)
	}
	return name, roles
}

// FormatModelMark 把模型名与能力集拼回一条清单项。没有能力就只有名字。
func FormatModelMark(name string, roles []ModelRole) string {
	name = strings.TrimSpace(name)
	if len(roles) == 0 {
		return name
	}
	marks := make([]string, 0, len(roles))
	seen := map[ModelRole]bool{}
	// 按 KnownRoles 的顺序输出，让同一份配置每次写出来都一样。
	for _, known := range KnownRoles() {
		for _, role := range roles {
			if role == known && !seen[known] {
				seen[known] = true
				marks = append(marks, string(known))
			}
		}
	}
	if len(marks) == 0 {
		return name
	}
	return name + "#" + strings.Join(marks, ",")
}

// ModelHasRole 报一条清单项带不带某个能力。
func ModelHasRole(entry string, role ModelRole) bool {
	_, roles := ParseModelMark(entry)
	for _, item := range roles {
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
