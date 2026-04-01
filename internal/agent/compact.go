package agent

import (
	"unicode/utf8"

	"github.com/chowyu12/aiclaw/internal/model"
)

const (
	lightCompactTurns   = 5
	lightCompactMaxTool = 300
	lightCompactMaxText = 500
)

// estimateTokens 粗略估算文本对应的 token 数。
// CJK 文本约 1.5 字/token，ASCII 文本约 4 字符/token。
func estimateTokens(s string) int {
	n := utf8.RuneCountInString(s)
	if n == 0 {
		return 0
	}
	b := len(s)
	if b > n*2 {
		return max(n*2/3, 1)
	}
	return max(n/4, 1)
}

// compactTurnLight 对一轮对话做轻量压缩：保留消息结构（包含 tool_calls 和 tool 消息），
// 但截断过长的内容，从而在保留 "调了什么工具、大致结果" 的同时减少 token 开销。
func compactTurnLight(turn []model.Message) []model.Message {
	out := make([]model.Message, 0, len(turn))
	for _, msg := range turn {
		m := msg
		switch m.Role {
		case "tool":
			m.Content = truncateRunes(m.Content, lightCompactMaxTool)
		case "user", "assistant":
			m.Content = truncateRunes(m.Content, lightCompactMaxText)
		}
		out = append(out, m)
	}
	return out
}

func truncateRunes(s string, maxRunes int) string {
	rs := []rune(s)
	if len(rs) <= maxRunes {
		return s
	}
	return string(rs[:maxRunes]) + "\n...(truncated)"
}
