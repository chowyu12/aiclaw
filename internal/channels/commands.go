package channels

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/agent"
	"github.com/chowyu12/aiclaw/internal/model"
)

// 内置斜杠命令：在 LLM 调用前拦截，让用户可以在任何 channel 中切换 / 续接会话。
//
// 支持：
//
//	/new           开启新会话（解绑当前 thread → 下一次进入时自动建新 conversation）
//	/continue      列出最近的会话归档
//	/continue N    切换 thread 绑定到第 N 个归档对应的 conversation
//	/archives      同 /continue（不带数字）
//	/help          列出所有可用命令
const commandPrefix = "/"

func (b *Bridge) tryHandleCommand(ctx context.Context, ch *model.Channel, cc ChannelConfig, in *Inbound, ad WebhookDriver, text string) bool {
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, commandPrefix) {
		return false
	}

	parts := strings.Fields(t)
	cmd := strings.ToLower(parts[0])
	args := parts[1:]

	switch cmd {
	case "/new", "/reset":
		b.handleNewCommand(ctx, ch, cc, in, ad)
		return true
	case "/continue", "/archives":
		b.handleContinueCommand(ctx, ch, cc, in, ad, args)
		return true
	case "/help":
		b.handleHelpCommand(ctx, cc, in, ad)
		return true
	}
	return false
}

func (b *Bridge) handleNewCommand(ctx context.Context, ch *model.Channel, cc ChannelConfig, in *Inbound, ad WebhookDriver) {
	keys := threadLookupKeys(in)

	prevConvUUID := ""
	for _, k := range keys {
		if row, err := b.store.GetChannelThread(ctx, ch.ID, k); err == nil && row != nil {
			prevConvUUID = strings.TrimSpace(row.ConversationUUID)
			if prevConvUUID != "" {
				break
			}
		}
	}

	if prevConvUUID != "" {
		if err := b.store.DeleteChannelThreadsByConversation(ctx, ch.ID, prevConvUUID); err != nil {
			log.WithError(err).WithField("conv", prevConvUUID).Warn("[Channel] /new unbind failed")
		}
	}

	conv := &model.Conversation{
		UserID:    channelUserID(ch, in),
		AgentUUID: ch.AgentUUID,
		Title:     "New Conversation",
	}
	if err := b.store.CreateConversation(ctx, conv); err != nil {
		_ = b.sendChannelReply(ctx, ad, cc, in, fmt.Sprintf("创建新会话失败: %s", err), nil)
		return
	}
	if err := b.bindThreadKeys(ctx, ch.ID, keys, conv.UUID); err != nil {
		log.WithError(err).Warn("[Channel] /new bind keys failed")
	}
	log.WithFields(log.Fields{
		"channel_id":   ch.ID,
		"conv_uuid":    conv.UUID,
		"prev_conv":    prevConvUUID,
		"thread_keys":  keys,
	}).Info("[Channel] /new — created fresh conversation")

	msg := "已开启新会话。"
	if prevConvUUID != "" {
		msg += "（旧会话仍可通过 /continue 找回）"
	}
	_ = b.sendChannelReply(ctx, ad, cc, in, msg, nil)
}

func (b *Bridge) handleContinueCommand(ctx context.Context, ch *model.Channel, cc ChannelConfig, in *Inbound, ad WebhookDriver, args []string) {
	userID := channelUserID(ch, in)
	archives, err := agent.ListArchives(ctx, b.store, b.executor.Workspace(), ch.AgentUUID, userID, 10)
	if err != nil {
		_ = b.sendChannelReply(ctx, ad, cc, in, fmt.Sprintf("查询归档失败: %s", err), nil)
		return
	}
	if len(archives) == 0 {
		_ = b.sendChannelReply(ctx, ad, cc, in, "还没有归档的历史会话。继续聊天，达到一定轮次后会自动归档。", nil)
		return
	}

	if len(args) == 0 {
		var sb strings.Builder
		sb.WriteString("最近的会话归档（用 /continue N 续接）:\n")
		for i, a := range archives {
			title := strings.TrimSpace(a.Title)
			if title == "" {
				title = "(无标题)"
			}
			sb.WriteString(fmt.Sprintf("%d. %s — %s\n", i+1, title, a.UpdatedAt.Format("01-02 15:04")))
		}
		_ = b.sendChannelReply(ctx, ad, cc, in, sb.String(), nil)
		return
	}

	n, err := strconv.Atoi(args[0])
	if err != nil || n < 1 || n > len(archives) {
		_ = b.sendChannelReply(ctx, ad, cc, in, fmt.Sprintf("无效编号 %q，请输入 1-%d。", args[0], len(archives)), nil)
		return
	}
	target := archives[n-1]

	keys := threadLookupKeys(in)

	// 解绑旧 conversation（若存在），然后绑定到目标 conv
	for _, k := range keys {
		if row, err := b.store.GetChannelThread(ctx, ch.ID, k); err == nil && row != nil {
			if cur := strings.TrimSpace(row.ConversationUUID); cur != "" && cur != target.ConvUUID {
				_ = b.store.DeleteChannelThreadsByConversation(ctx, ch.ID, cur)
			}
		}
	}
	if err := b.bindThreadKeys(ctx, ch.ID, keys, target.ConvUUID); err != nil {
		_ = b.sendChannelReply(ctx, ad, cc, in, fmt.Sprintf("切换失败: %s", err), nil)
		return
	}

	log.WithFields(log.Fields{
		"channel_id":  ch.ID,
		"conv_uuid":   target.ConvUUID,
		"thread_keys": keys,
	}).Info("[Channel] /continue — switched to archived conversation")

	msg := fmt.Sprintf("已切换到会话「%s」（更新于 %s）。直接发消息继续。",
		target.Title, target.UpdatedAt.Format(time.DateTime))
	_ = b.sendChannelReply(ctx, ad, cc, in, msg, nil)
}

func (b *Bridge) handleHelpCommand(ctx context.Context, cc ChannelConfig, in *Inbound, ad WebhookDriver) {
	help := `可用命令：
/new            开启新会话（旧会话保留，可用 /continue 找回）
/continue       列出最近的归档会话
/continue N     切换到第 N 个归档会话
/help           显示本帮助`
	_ = b.sendChannelReply(ctx, ad, cc, in, help, nil)
}
