package agent

import (
	"regexp"
	"strings"

	openai "github.com/chowyu12/go-openai"
)

// url_gate.go 实现「URL 门控」策略：
// 某些内置工具（如 web_fetch）只有在对话上下文中出现了用户显式提供的 URL 时，
// 才应暴露给 LLM。这样可以避免 LLM 把 web_fetch 当作泛化的联网检索工具使用，
// 未提供 URL 的场景应交给内置联网搜索（web_search）。

// urlGatedTools 列出所有受 URL 门控的工具名。若上下文未发现 URL，则这些工具
// 不会出现在发给 LLM 的 tool definitions 里。
var urlGatedTools = map[string]struct{}{
	"web_fetch": {},
}

// urlRegexp 匹配 http/https 绝对 URL。
// 参考 RFC 3986 的常见子集：scheme://host[:port][/path][?query][#fragment]
// 末尾的常见标点不纳入 URL。
var urlRegexp = regexp.MustCompile(`(?i)https?://[^\s<>"'\x60\]\[(){}]+`)

// containsURL 判断文本中是否包含 http/https URL。
func containsURL(text string) bool {
	if text == "" {
		return false
	}
	return urlRegexp.MatchString(text)
}

// userMessagesHaveURL 扫描消息列表，判断是否存在"用户提供的 URL"。
// 只看 role == user 的消息（包含 MultiContent 中的 text 部分）；
// assistant / tool 的回复即便含 URL 也不算作用户提供。
func userMessagesHaveURL(messages []openai.ChatCompletionMessage) bool {
	for _, msg := range messages {
		if msg.Role != openai.ChatMessageRoleUser {
			continue
		}
		if containsURL(msg.Content) {
			return true
		}
		for _, part := range msg.MultiContent {
			if part.Type == openai.ChatMessagePartTypeText && containsURL(part.Text) {
				return true
			}
		}
	}
	return false
}

// filterURLGatedTools 过滤掉当前上下文不应暴露的 URL 门控工具。
// - 当 hasURL == true 时：原样返回 tools（全部保留）。
// - 当 hasURL == false 时：剔除 urlGatedTools 中的工具定义。
// 返回的 slice 是一个新的底层数组（当发生过滤时），不会修改入参。
func filterURLGatedTools(tools []openai.Tool, hasURL bool) []openai.Tool {
	if hasURL || len(tools) == 0 {
		return tools
	}
	filtered := make([]openai.Tool, 0, len(tools))
	for _, t := range tools {
		name := ""
		if t.Function != nil {
			name = strings.TrimSpace(t.Function.Name)
		}
		if _, gated := urlGatedTools[name]; gated {
			continue
		}
		filtered = append(filtered, t)
	}
	return filtered
}
