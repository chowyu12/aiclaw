package channels

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/agent"
	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/store"
)

// Bridge 将入站消息映射为会话并调用 Executor，再通过适配器 Reply（参考 goclaw 的 bus 串联思路）。
type Bridge struct {
	store    store.Store
	executor *agent.Executor
}

func NewBridge(s store.Store, exec *agent.Executor) *Bridge {
	if s == nil || exec == nil {
		return nil
	}
	return &Bridge{store: s, executor: exec}
}

// HandleInboundAsync 在 goroutine 中执行 Agent 并回复；Webhook 应先返回适配器同步响应。
func (b *Bridge) HandleInboundAsync(parent context.Context, ch *model.Channel, in *Inbound, ad WebhookDriver) {
	if b == nil || ch == nil || in == nil {
		return
	}
	if ad == nil {
		ad = noopAdapter{}
	}
	text := strings.TrimSpace(in.Text)
	if text == "" {
		return
	}
	cc := ConfigFromModel(ch)
	go b.runReply(parent, ch, cc, in, ad, text)
}

func (b *Bridge) runReply(_ context.Context, ch *model.Channel, cc ChannelConfig, in *Inbound, ad WebhookDriver, userText string) {
	defer func() {
		if r := recover(); r != nil {
			log.WithFields(log.Fields{"channel_id": ch.ID, "recover": r}).Error("[Channel] inbound panic")
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	convUUID, err := b.ensureThreadConversation(ctx, ch, in)
	if err != nil {
		log.WithError(err).WithField("channel_id", ch.ID).Error("[Channel] ensure conversation failed")
		return
	}

	req := model.ChatRequest{
		AgentID:        "",
		ConversationID: convUUID,
		UserID:         channelUserID(ch, in),
		Message:        userText,
	}
	res, err := b.executor.Execute(ctx, req)
	if err != nil {
		log.WithError(err).WithField("channel_id", ch.ID).Error("[Channel] executor failed")
		_ = b.sendChannelReply(ctx, ad, cc, in, "处理失败，请稍后重试。")
		return
	}
	if err := b.sendChannelReply(ctx, ad, cc, in, res.Content); err != nil {
		log.WithError(err).WithField("channel_id", ch.ID).Warn("[Channel] reply failed")
	}
}

func (b *Bridge) sendChannelReply(ctx context.Context, ad WebhookDriver, cc ChannelConfig, in *Inbound, text string) error {
	if in.ReplyWith != nil {
		return in.ReplyWith(ctx, text)
	}
	return ad.Reply(ctx, cc, in, text)
}

func (b *Bridge) ensureThreadConversation(ctx context.Context, ch *model.Channel, in *Inbound) (string, error) {
	key := strings.TrimSpace(in.ThreadKey)
	if key == "" {
		key = strings.TrimSpace(in.SenderID)
	}
	if key == "" {
		key = "default"
	}
	row, err := b.store.GetChannelThread(ctx, ch.ID, key)
	if err == nil && row != nil {
		return row.ConversationUUID, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	title := truncateRunes(strings.TrimSpace(in.Text), 80)
	if title == "" {
		title = "Channel"
	}
	conv := &model.Conversation{
		UserID: channelUserID(ch, in),
		Title:  title,
	}
	if err := b.store.CreateConversation(ctx, conv); err != nil {
		return "", err
	}
	if err := b.store.UpsertChannelThread(ctx, ch.ID, key, conv.UUID); err != nil {
		return "", err
	}
	return conv.UUID, nil
}

func channelUserID(ch *model.Channel, in *Inbound) string {
	sender := strings.TrimSpace(in.SenderID)
	if sender == "" {
		sender = "unknown"
	}
	return "channel:" + ch.UUID + ":" + sender
}

func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	var b strings.Builder
	i := 0
	for _, r := range s {
		if i >= n {
			break
		}
		b.WriteRune(r)
		i++
	}
	return b.String()
}
