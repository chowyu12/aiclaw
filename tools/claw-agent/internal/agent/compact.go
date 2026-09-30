package agent

import (
	"context"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/store"
	"strings"
	"time"

	"github.com/chowyu12/aiclaw/internal/i18n"
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
// 这是 Codex 压缩提示词的核心结构。
const summarizationPrompt = `You are performing a context checkpoint compaction. Write a handoff summary for another model that will pick up and finish this task.

Include:
- Current progress and the key decisions made so far
- Important context, constraints and user preferences
- What remains to be done (as clear next steps)
- Any critical data, examples, paths or references needed to continue

Be concise and structured; include only what helps the next model carry on with the work.`

// summaryPrefix 是摘要在新历史里的开场白，告诉模型这段是怎么来的。
const summaryPrefix = `Another model already started working on this task and left a summary of its progress. Build on the work it has done and don't repeat what is already finished. Here is the summary it left:`

// legacySummaryPrefix 是改成英文之前的开场白，只用来认出旧存档里的压缩摘要。
const legacySummaryPrefix = `上一个模型已经开始处理这个任务，并留下了它的思考过程摘要。`

// interruptMarker 在用户中断之后写进历史。
//
// 不写这句的话，模型下一轮看到的是一串没头没尾的工具结果，会以为那些操作
// 是正常完成的。措辞照 Codex：明确说明中断是用户故意的，并且被中止的命令
// **可能已经部分执行**——这一点直接影响模型要不要重做。
//
// 措辞要把「继续」与「换一件事」分开：早先写的是「继续之前请先确认当前状态」，
// 用户中断后发来一张没配文字的图，模型把它当成了「继续吧」，接着去做被中断的
// 那件事，对那张图一个字没提。**实际发生过。**
const interruptMarker = `The user interrupted the previous turn on purpose. Any tools or commands that were aborted may have partially executed: if you pick the work back up, verify the current state first and don't assume they had no effect. If the user's next message is about something else, follow the new request and don't go back to the interrupted task on your own.`

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
		return i18n.E("历史为空，无从压缩")
	}

	request := make([]llm.Message, 0, len(history)+1)
	request = append(request, history...)
	request = append(request, llm.Message{Role: llm.RoleUser, Content: summarizationPrompt})

	// 压缩这一次不给工具：要的是一段文字，给了工具反而可能又去读文件。
	started := time.Now()
	response, err := s.llm.Stream(ctx, llm.Request{
		Model:     s.config.Model.Model,
		Messages:  request,
		MaxTokens: s.config.Model.MaxTokens,
	}, nil)
	// 压缩也花 token，而且往往是最贵的一次（整段历史都要读一遍）。
	s.recordUsage(store.UsageEvent{
		Kind: store.UsageModel, Model: s.config.Model.Model, Detail: "compact",
		Input: response.Usage.InputTokens, Output: response.Usage.OutputTokens, Total: response.Usage.TotalTokens,
		Failed: err != nil, DurationMS: time.Since(started).Milliseconds(),
	})
	if err != nil {
		return err
	}
	summary := strings.TrimSpace(response.Content)
	if summary == "" {
		return i18n.E("模型没有给出摘要")
	}

	compacted := buildCompactedHistory(history, summary)
	s.mu.Lock()
	s.messages = compacted
	s.lastInputTokens = 0
	s.mu.Unlock()

	s.notify(emitter, turnID, i18n.D("上下文接近上限，已压缩历史：保留了近期的用户消息和一份进度摘要。"))
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
