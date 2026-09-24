package agent

import (
	"context"
	"errors"
	"strings"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

// 压缩阈值。两个数都照抄 Codex：
//   - 用量到窗口的 90% 就压，留出的余量要够装下一次模型回答；
//   - 压缩后保留的用户消息总量上限 20000 token，再多就挤掉摘要本身的位置。
const (
	autoCompactPercent          = 90
	compactUserMessageMaxTokens = 20000
)

// summarizationPrompt 是让模型自己总结这段对话的指令。
//
// 写成「交接给另一个模型」而不是「总结一下」：后者会得到一段面向人的复述，
// 前者才会把决定、约束、下一步这些接着干活需要的东西写出来。
// 这是 Codex 压缩提示词的核心结构，换成中文以与本应用其余提示词一致。
const summarizationPrompt = `现在要做一次上下文压缩。请写一份交接摘要，交给另一个模型接着完成这个任务。

需要包含：
- 目前的进展，以及已经做出的关键决定
- 重要的上下文、约束条件、用户偏好
- 还剩什么没做（写成明确的下一步）
- 继续工作所必需的关键数据、示例、路径、引用

要简洁、有结构，只写对「接着干下去」有用的内容。`

// summaryPrefix 是摘要在新历史里的开场白，告诉模型这段是怎么来的。
const summaryPrefix = `上一个模型已经开始处理这个任务，并留下了它的思考过程摘要。你可以接着它的工作往下做，不要重复已经完成的部分。以下是它留下的摘要：`

// interruptMarker 在用户中断之后写进历史。
//
// 不写这句的话，模型下一轮看到的是一串没头没尾的工具结果，会以为那些操作
// 是正常完成的。措辞照 Codex：明确说明中断是用户故意的，并且被中止的命令
// **可能已经部分执行**——这一点直接影响模型要不要重做。
const interruptMarker = `用户主动中断了上一轮。被中止的工具或命令可能已经部分执行过，继续之前请先确认当前的实际状态，不要假设它们没有生效。`

// needsCompaction 报告当前历史是否已经逼近上下文窗口。
//
// 窗口未配置时返回 false：这时只能等上游报错再被动压缩，见 sample。
func (s *Session) needsCompaction() bool {
	window := s.config.Model.ContextWindow
	if window <= 0 {
		return false
	}
	s.mu.Lock()
	used := s.lastInputTokens
	s.mu.Unlock()
	return used > 0 && used >= window*autoCompactPercent/100
}

// compact 让模型总结当前历史，并用「系统提示词 + 近期用户消息 + 摘要」替换它。
//
// 为什么保留用户消息而不是最近几轮对话：用户说过的话是任务本身，摘要复述
// 容易失真；而助手消息和工具结果是过程，摘要就是用来替代它们的。
// 顺带一个必须的性质——重建出来的历史里没有任何 tool_calls，也就不可能留下
// 「有调用没有结果」的孤儿，那在 Chat Completions 上是 400。
func (s *Session) compact(ctx context.Context, turnID string, emitter Emitter) error {
	history := s.snapshotMessages()
	if len(history) <= 1 {
		return errors.New("历史为空，无从压缩")
	}

	request := make([]llm.Message, 0, len(history)+1)
	request = append(request, history...)
	request = append(request, llm.Message{Role: llm.RoleUser, Content: summarizationPrompt})

	// 压缩这一次不给工具：要的是一段文字，给了工具反而可能又去读文件。
	response, err := s.llm.Stream(ctx, llm.Request{
		Model:     s.config.Model.Model,
		Messages:  request,
		MaxTokens: s.config.Model.MaxTokens,
	}, nil)
	if err != nil {
		return err
	}
	summary := strings.TrimSpace(response.Content)
	if summary == "" {
		return errors.New("模型没有给出摘要")
	}

	compacted := buildCompactedHistory(history, summary)
	s.mu.Lock()
	s.messages = compacted
	s.lastInputTokens = 0
	s.mu.Unlock()

	s.notify(emitter, turnID, "上下文接近上限，已压缩历史：保留了近期的用户消息和一份进度摘要。")
	return nil
}

// buildCompactedHistory 拼出压缩后的新历史。
func buildCompactedHistory(history []llm.Message, summary string) []llm.Message {
	out := make([]llm.Message, 0, 8)
	if len(history) > 0 && history[0].Role == llm.RoleSystem {
		out = append(out, history[0])
	}

	// 从新往旧收用户消息，收到预算用完为止，再翻回时间顺序。
	// 反向收是因为越近的要求越可能仍然有效；正向收会在长会话里
	// 留下一堆早就被推翻的旧指令。
	var kept []llm.Message
	budget := compactUserMessageMaxTokens
	for i := len(history) - 1; i >= 0; i-- {
		message := history[i]
		if message.Role != llm.RoleUser {
			continue
		}
		cost := approxTokens(message.Content)
		if cost > budget {
			break
		}
		budget -= cost
		kept = append(kept, message)
	}
	for i := len(kept) - 1; i >= 0; i-- {
		// 图片不带进压缩后的历史。压缩发生的原因就是上下文装不下了，
		// 而一张图往往比它前后所有文字加起来还贵；那时候要保住的是
		// 「用户要求过什么」，摘要里有。留着图会让压缩救不回这个会话。
		message := kept[i]
		message.Images = nil
		out = append(out, message)
	}

	return append(out, llm.Message{
		Role:    llm.RoleUser,
		Content: summaryPrefix + "\n\n" + summary,
		Shown:   &llm.Shown{Hidden: true},
	})
}

// dropOldest 丢掉最老的一条非系统消息，返回是否真的丢掉了。
//
// 压缩之后还报超窗时的最后手段（Codex 的 remove_first_item）。从头上丢而不是
// 从中间挑：前缀不变才能命中上游的 prompt cache，从中间挖洞会让整段缓存失效。
func (s *Session) dropOldest() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	start := 0
	if len(s.messages) > 0 && s.messages[0].Role == llm.RoleSystem {
		start = 1
	}
	if len(s.messages) <= start+1 {
		return false
	}
	s.messages = append(s.messages[:start], s.messages[start+1:]...)
	// 丢掉一条带 tool_calls 的 assistant 之后，紧跟着的 tool 结果就成了孤儿，
	// 一并丢掉——留着它们上游会直接 400。
	for len(s.messages) > start && s.messages[start].Role == llm.RoleTool {
		s.messages = append(s.messages[:start], s.messages[start+1:]...)
	}
	return true
}

// notify 给宿主发一条内核通知条目。
func (s *Session) notify(emitter Emitter, turnID, text string) {
	emitter.Notify(protocol.NotifyItemCompleted, protocol.ItemNotification{
		SessionID: s.ID, TurnID: turnID,
		Item: protocol.Item{ID: newID("notice"), Kind: protocol.ItemNotice, Text: text},
	})
}
