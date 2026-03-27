package channels

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	log "github.com/sirupsen/logrus"
)

// WhatsApp Cloud API Webhook：https://developers.facebook.com/docs/whatsapp/cloud-api/webhooks/components

type whatsAppAdapter struct{}

func (whatsAppAdapter) HandleGET(ch ChannelConfig, q url.Values) WebhookHTTP {
	mode := q.Get("hub.mode")
	token := q.Get("hub.verify_token")
	challenge := q.Get("hub.challenge")
	expect := cfgString(ch.ConfigJSON, "verify_token", "hub_verify_token")
	if strings.EqualFold(mode, "subscribe") && expect != "" && token == expect && challenge != "" {
		return WebhookHTTP{Status: 200, ContentType: "text/plain; charset=utf-8", Body: []byte(challenge)}
	}
	if challenge != "" && expect == "" {
		log.WithField("channel_uuid", ch.UUID).Warn("[whatsapp] 收到校验请求但未配置 config.verify_token")
	}
	return WebhookHTTP{Status: 200, ContentType: "application/json", Body: []byte(`{"ok":true}`)}
}

func (whatsAppAdapter) HandlePOST(ch ChannelConfig, body []byte, _ string, _ http.Header) (WebhookHTTP, *Inbound) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return jsonOK(), nil
	}
	entries, _ := root["entry"].([]any)
	if len(entries) == 0 {
		return jsonOK(), nil
	}
	e0, _ := entries[0].(map[string]any)
	changes, _ := e0["changes"].([]any)
	if len(changes) == 0 {
		return jsonOK(), nil
	}
	c0, _ := changes[0].(map[string]any)
	val, _ := c0["value"].(map[string]any)
	msgs, _ := val["messages"].([]any)
	if len(msgs) == 0 {
		return jsonOK(), nil
	}
	m0, _ := msgs[0].(map[string]any)
	from, _ := m0["from"].(string)
	text := ""
	if txt, ok := m0["text"].(map[string]any); ok {
		text, _ = txt["body"].(string)
	}
	// TODO: 解析 type=image 消息，通过 Graph API 下载 media
	text = strings.TrimSpace(text)
	if text == "" || from == "" {
		return jsonOK(), nil
	}
	thread := from
	return jsonOK(), &Inbound{ThreadKey: thread, SenderID: from, Text: text, RawMeta: root}
}

func (whatsAppAdapter) Reply(ctx context.Context, ch ChannelConfig, in *Inbound, text string) error {
	_ = ctx
	_ = in
	_ = text
	log.WithField("channel_uuid", ch.UUID).Info("[whatsapp] Reply 需调用 Graph API messages，请在后端扩展")
	return nil
}
