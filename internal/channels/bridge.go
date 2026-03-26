package channels

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	log "github.com/sirupsen/logrus"
	"golang.org/x/sync/singleflight"

	"github.com/chowyu12/aiclaw/internal/agent"
	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/store"
)

const channelLogTextRunes = 500

// 同一 (channel, 线程) 并发首包时合并为一次「建会话 + 写映射」，避免多条 channel_threads / 多个 Conversation。
var channelThreadFlight singleflight.Group

// Bridge 将入站消息映射为会话并调用 Executor，再通过适配器 Reply（参考 goclaw 的 bus 串联思路）。
type Bridge struct {
	store    store.Store
	executor *agent.Executor
}

func newBridge(s store.Store, exec *agent.Executor) *Bridge {
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
	if text == "" && len(in.Files) == 0 {
		return
	}
	log.WithFields(log.Fields{
		"channel_id":    ch.ID,
		"channel_uuid":  ch.UUID,
		"channel_type":  string(ch.ChannelType),
		"thread_key":    strings.TrimSpace(in.ThreadKey),
		"thread_lookup": strings.Join(threadLookupKeys(in), " | "),
		"sender_id":     strings.TrimSpace(in.SenderID),
		"message":       TruncateRunes(text, channelLogTextRunes),
		"message_runes": utf8.RuneCountInString(text),
	}).Info("[Channel] inbound")
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
	startAt := time.Now()

	convUUID, err := b.ensureThreadConversation(ctx, ch, in)
	if err != nil {
		log.WithError(err).WithFields(log.Fields{
			"channel_id":   ch.ID,
			"channel_type": string(ch.ChannelType),
			"thread_key":   strings.TrimSpace(in.ThreadKey),
			"sender_id":    strings.TrimSpace(in.SenderID),
			"message":      TruncateRunes(userText, channelLogTextRunes),
		}).Error("[Channel] ensure conversation failed")
		return
	}

	req := model.ChatRequest{
		ConversationID: convUUID,
		UserID:         channelUserID(ch, in),
		Message:        userText,
		Files:          in.Files,
		ExecChannel: &model.ChannelExecTrace{
			ID:        ch.ID,
			UUID:      ch.UUID,
			Type:      string(ch.ChannelType),
			ThreadKey: strings.TrimSpace(in.ThreadKey),
			SenderID:  strings.TrimSpace(in.SenderID),
		},
	}
	log.WithFields(log.Fields{
		"channel_id":        ch.ID,
		"channel_type":      string(ch.ChannelType),
		"conversation_uuid": convUUID,
		"thread_key":        strings.TrimSpace(in.ThreadKey),
		"sender_id":         strings.TrimSpace(in.SenderID),
		"user_message":      TruncateRunes(userText, channelLogTextRunes),
	}).Info("[Channel] executor >> start")
	res, err := b.executor.Execute(ctx, req)
	if err != nil {
		log.WithError(err).WithFields(log.Fields{
			"channel_id":        ch.ID,
			"channel_type":      string(ch.ChannelType),
			"conversation_uuid": convUUID,
			"message":           TruncateRunes(userText, channelLogTextRunes),
			"duration_ms":       time.Since(startAt).Milliseconds(),
		}).Error("[Channel] executor failed")
		fallback := "处理失败，请稍后重试。"
		_ = b.persistAssistantFallback(ctx, convUUID, fallback)
		_ = b.sendChannelReply(ctx, ad, cc, in, fallback)
		return
	}
	log.WithFields(log.Fields{
		"channel_id":        ch.ID,
		"channel_type":      string(ch.ChannelType),
		"conversation_uuid": convUUID,
		"duration_ms":       time.Since(startAt).Milliseconds(),
		"tokens_used":       res.TokensUsed,
		"steps_count":       len(res.Steps),
	}).Info("[Channel] executor << done")
	if err := b.sendChannelReply(ctx, ad, cc, in, res.Content); err != nil {
		log.WithError(err).WithFields(log.Fields{
			"channel_id":        ch.ID,
			"conversation_uuid": convUUID,
			"reply":             TruncateRunes(res.Content, channelLogTextRunes),
		}).Warn("[Channel] reply failed")
		return
	}
	log.WithFields(log.Fields{
		"channel_id":         ch.ID,
		"channel_uuid":       ch.UUID,
		"channel_type":       string(ch.ChannelType),
		"conversation_uuid":  convUUID,
		"reply":              TruncateRunes(res.Content, channelLogTextRunes),
		"reply_runes":        utf8.RuneCountInString(res.Content),
		"user_message":       TruncateRunes(userText, channelLogTextRunes),
		"user_message_runes": utf8.RuneCountInString(userText),
	}).Info("[Channel] reply ok")
}

func (b *Bridge) persistAssistantFallback(ctx context.Context, conversationUUID, content string) error {
	conv, err := b.store.GetConversationByUUID(ctx, conversationUUID)
	if err != nil {
		return err
	}
	msg := &model.Message{
		ConversationID: conv.ID,
		Role:           "assistant",
		Content:        content,
		TokensUsed:     0,
	}
	if err := b.store.CreateMessage(ctx, msg); err != nil {
		return err
	}
	return nil
}

func (b *Bridge) sendChannelReply(ctx context.Context, ad WebhookDriver, cc ChannelConfig, in *Inbound, text string) error {
	if in.ReplyWith != nil {
		return in.ReplyWith(ctx, text)
	}
	return ad.Reply(ctx, cc, in, text)
}

func threadLookupKeys(in *Inbound) []string {
	var out []string
	seen := make(map[string]bool)
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	add(in.ThreadKey)
	for _, a := range in.ThreadKeyAliases {
		add(a)
	}
	if len(out) == 0 {
		if s := strings.TrimSpace(in.SenderID); s != "" {
			return []string{s}
		}
		return []string{"default"}
	}
	return out
}

func threadFlightKey(channelID int64, keys []string) string {
	dup := append([]string(nil), keys...)
	slices.Sort(dup)
	return fmt.Sprintf("%d\x1f%s", channelID, strings.Join(dup, "\x1f"))
}

func (b *Bridge) bindThreadKeys(ctx context.Context, channelID int64, keys []string, convUUID string) error {
	for _, k := range keys {
		if err := b.store.UpsertChannelThread(ctx, channelID, k, convUUID); err != nil {
			return err
		}
	}
	return nil
}

func (b *Bridge) ensureThreadConversation(ctx context.Context, ch *model.Channel, in *Inbound) (string, error) {
	keys := threadLookupKeys(in)
	fk := threadFlightKey(ch.ID, keys)
	v, err, _ := channelThreadFlight.Do(fk, func() (any, error) {
		return b.ensureThreadConversationBody(ctx, ch, in, keys)
	})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}

func (b *Bridge) ensureThreadConversationBody(ctx context.Context, ch *model.Channel, in *Inbound, keys []string) (string, error) {
	for _, k := range keys {
		row, err := b.store.GetChannelThread(ctx, ch.ID, k)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return "", err
			}
			continue
		}
		if strings.TrimSpace(row.ConversationUUID) != "" {
			if _, err := b.store.GetConversationByUUID(ctx, row.ConversationUUID); err != nil {
				if !errors.Is(err, sql.ErrNoRows) {
					return "", err
				}
				continue
			}
			if err := b.bindThreadKeys(ctx, ch.ID, keys, row.ConversationUUID); err != nil {
				return "", err
			}
			return row.ConversationUUID, nil
		}
	}
	title := TruncateRunes(strings.TrimSpace(in.Text), 80)
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
	if err := b.bindThreadKeys(ctx, ch.ID, keys, conv.UUID); err != nil {
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

// TruncateRunes 截断字符串到 n 个 rune。
func TruncateRunes(s string, n int) string {
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
