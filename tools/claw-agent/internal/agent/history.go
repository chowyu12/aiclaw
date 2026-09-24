package agent

import (
	"strings"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

// History 把会话历史还原成宿主能直接渲染的时间线条目。
//
// 内核存的是给模型看的消息，宿主要的是给人看的时间线，两者形状不同：
// 一条带 tool_calls 的 assistant 消息，在时间线上是「一句话 + 若干个工具步骤」。
//
// 几条刻意的取舍：
//   - 系统提示词不出现。它是内核拼的，不是对话的一部分，显示出来只会让用户困惑；
//   - 工具结果按 call id 回填到对应的步骤上，顺序以 assistant 消息为准，
//     而不是 tool 消息在数组里的位置——并行执行时后者的顺序没有意义；
//   - 压缩产生的摘要是 user 消息，会照常显示。这是对的：历史被压缩过
//     是用户应当看见的事实，藏起来会让他以为模型无缘无故忘了事；
//   - **每条 assistant 消息还原成一个采样步骤**。一条 assistant 消息就是模型被
//     调用了一次，所以执行过程的形状是「采样 → 它决定调的那几个工具 → 再采样」。
//     不还原的话，重开一个会话看到的步骤里只有工具，没有「谁决定调它们」，
//     与这一轮正在跑时看到的对不上。
//
// 还原不出来的是耗时、TTFT 与 token：那些只存在于当轮的事件流里，存档里存的是
// 给模型看的消息，没有这些数字。所以历史步骤不带时间——**宁可不显示，也不要
// 编一个**，界面上那几个数字是用来判断「慢在等模型还是慢在跑工具」的。
func (s *Session) History() []protocol.Item {
	messages := s.snapshotMessages()
	modelName := s.config.Model.Model

	// 先把工具结果按 call id 建索引，下面按 assistant 里的顺序取用。
	results := make(map[string]string, len(messages))
	for _, message := range messages {
		if message.Role == llm.RoleTool {
			results[message.ToolCallID] = message.Content
		}
	}

	items := make([]protocol.Item, 0, len(messages))
	// 序号与采样轮次都是**轮内**的，碰到 user 消息就归零——界面上写的是
	// 「这一轮的第几步」，跨轮累加的话第二轮会从 7 开始。
	seq, round := 0, 0
	for index, message := range messages {
		switch message.Role {
		case llm.RoleSystem:
			continue
		case llm.RoleTool:
			// 已经作为步骤挂在对应的 assistant 下面了，不再单独列一条。
			continue
		case llm.RoleUser:
			// 有 Shown 的按用户发的那一版还原（见 llm.Message.Shown）。
			text, images := message.Content, message.Images
			if shown := message.Shown; shown != nil {
				if shown.Hidden {
					continue
				}
				text, images = shown.Text, shown.Images
			} else if legacySynthetic(message.Content) {
				continue
			}
			seq, round = 0, 0
			items = append(items, protocol.Item{
				ID:     historyID("user", index),
				Kind:   protocol.ItemUserMessage,
				Text:   text,
				Images: images,
				At:     message.At,
			})
		case llm.RoleAssistant:
			// 采样步骤排在它引发的工具之前：那才是实际发生的顺序，
			// 也是「谁决定调这些工具」这个问题的答案。
			seq++
			round++
			items = append(items, protocol.Item{
				ID:        historyID("llm", index),
				Kind:      protocol.ItemLLM,
				Summary:   modelName,
				Model:     modelName,
				Seq:       seq,
				Round:     round,
				ToolCalls: len(message.ToolCalls),
			})
			if strings.TrimSpace(message.Content) != "" {
				items = append(items, protocol.Item{
					ID:   historyID("msg", index),
					Kind: protocol.ItemAgentMessage,
					Text: message.Content,
					At:   message.At,
				})
			}
			for callIndex, call := range message.ToolCalls {
				seq++
				output, answered := results[call.ID]
				items = append(items, protocol.Item{
					ID:       historyID("tool", index*1000+callIndex),
					Kind:     protocol.ItemToolCall,
					ToolName: call.Name,
					ToolArgs: call.Arguments,
					// 没有结果说明那次调用没跑完（中断过）。标成失败而不是留空，
					// 免得看起来像执行成功但什么都没返回。
					ToolResult: output,
					ToolFailed: !answered || strings.HasPrefix(output, "错误："),
					Summary:    summarizeCall(llm.ToolCall{Name: call.Name, Arguments: call.Arguments}),
					Seq:        seq,
				})
			}
		}
	}
	return items
}

// legacySynthetic 认出 Shown 出现之前存下的合成消息：中断标记、压缩摘要、截屏
// 画面。它们以 user 消息送给模型，但不是用户说的；旧存档里没有 Hidden 标记，
// 只能按开头认。中断标记改过措辞，所以按不变的第一句认。
func legacySynthetic(content string) bool {
	return strings.HasPrefix(content, "用户主动中断了上一轮。") ||
		strings.HasPrefix(content, summaryPrefix) ||
		content == "（上一步截屏的画面）"
}

// historyID 给还原出来的条目一个稳定 id。
//
// 稳定很重要：宿主按 id 去重与定位，每次还原都换一批 id 的话，
// 切走再切回来会得到一堆重复条目。
func historyID(prefix string, index int) string {
	return prefix + "_h" + itoa(index)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits [20]byte
	pos := len(digits)
	for n > 0 {
		pos--
		digits[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(digits[pos:])
}
