package channels

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

// Telegram Bot Webhook：https://core.telegram.org/bots/api#setwebhook

type telegramAdapter struct{}

func (telegramAdapter) HandleGET(ch ChannelConfig, q url.Values) WebhookHTTP {
	// 部分代理健康检查
	_ = ch
	if q.Get("ping") != "" {
		return WebhookHTTP{Status: 200, Body: []byte("pong")}
	}
	return WebhookHTTP{Status: 200, ContentType: "application/json", Body: []byte(`{"ok":true}`)}
}

func (telegramAdapter) HandlePOST(ch ChannelConfig, body []byte, _ string, _ http.Header) (WebhookHTTP, *Inbound) {
	var upd telegramUpdate
	if err := json.Unmarshal(body, &upd); err != nil {
		return jsonOK(), nil
	}
	if upd.Message == nil {
		return jsonOK(), nil
	}
	msg := upd.Message
	content := strings.TrimSpace(msg.Text)
	if content == "" {
		content = strings.TrimSpace(msg.Caption)
	}
	if content == "" {
		return jsonOK(), nil
	}
	senderID := ""
	if msg.From != nil {
		senderID = strconv.FormatInt(msg.From.ID, 10)
	}
	chatID := strconv.FormatInt(msg.Chat.ID, 10)
	return jsonOK(), &Inbound{
		ThreadKey: chatID,
		SenderID:  senderID,
		Text:      content,
		RawMeta: map[string]any{
			"message_id": msg.MessageID,
			"chat_type":  msg.Chat.Type,
		},
	}
}

func (telegramAdapter) Reply(ctx context.Context, ch ChannelConfig, in *Inbound, text string) error {
	if in == nil {
		return nil
	}
	token := cfgString(ch.ConfigJSON, "bot_token", "token")
	if token == "" {
		return fmt.Errorf("telegram: config.bot_token 未配置")
	}
	api := "https://api.telegram.org/bot" + token + "/sendMessage"
	form := url.Values{}
	form.Set("chat_id", in.ThreadKey)
	form.Set("text", text)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, api, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("telegram sendMessage: %s: %s", resp.Status, bytes.TrimSpace(respBody))
	}
	log.WithFields(log.Fields{"channel_uuid": ch.UUID, "chat_id": in.ThreadKey}).Debug("[telegram] sendMessage ok")
	return nil
}

type telegramUpdate struct {
	Message *telegramMessage `json:"message"`
}

type telegramMessage struct {
	MessageID int           `json:"message_id"`
	From      *telegramUser `json:"from"`
	Chat      telegramChat  `json:"chat"`
	Text      string        `json:"text"`
	Caption   string        `json:"caption"`
}

type telegramUser struct {
	ID int64 `json:"id"`
}

type telegramChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}
