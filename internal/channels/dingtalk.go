package channels

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	log "github.com/sirupsen/logrus"
)

// 钉钉机器人 / 会话回调常见 JSON 形态较多，这里覆盖 stream / 互动卡片等常见 text 字段。

type dingTalkAdapter struct{}

func (dingTalkAdapter) HandleGET(_ ChannelConfig, _ url.Values) WebhookHTTP {
	return WebhookHTTP{Status: 200, ContentType: "application/json", Body: []byte(`{"ok":true}`)}
}

func (dingTalkAdapter) HandlePOST(ch ChannelConfig, body []byte, _ string, _ http.Header) (WebhookHTTP, *Inbound) {
	if len(body) == 0 {
		return jsonOK(), nil
	}
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return jsonOK(), nil
	}
	text := dingTalkExtractText(root)
	// TODO: 解析 richText / picture 类型消息中的图片
	if text == "" {
		return jsonOK(), nil
	}
	convID := strings.TrimSpace(firstString(root, "conversationId", "conversation_id", "chatId", "chatid"))
	staff := strings.TrimSpace(firstString(root, "senderStaffId", "senderNick", "userId", "userid"))
	thread := convID
	if thread == "" {
		thread = staff
	}
	if thread == "" {
		thread = "dingtalk:" + ch.UUID
	}
	var aliases []string
	if convID != "" && staff != "" && convID != staff {
		if thread == convID {
			aliases = append(aliases, staff)
		} else {
			aliases = append(aliases, convID)
		}
	}
	return jsonOK(), &Inbound{ThreadKey: thread, ThreadKeyAliases: aliases, SenderID: staff, Text: text, RawMeta: root}
}

func dingTalkExtractText(m map[string]any) string {
	if t, ok := m["text"].(map[string]any); ok {
		if c, _ := t["content"].(string); c != "" {
			return strings.TrimSpace(c)
		}
	}
	if c, ok := m["content"].(map[string]any); ok {
		if s, _ := c["text"].(string); s != "" {
			return strings.TrimSpace(s)
		}
	}
	if s, _ := m["text"].(string); s != "" {
		return strings.TrimSpace(s)
	}
	return ""
}

func (dingTalkAdapter) Reply(ctx context.Context, ch ChannelConfig, in *Inbound, text string) error {
	_ = ctx
	_ = in
	_ = text
	log.WithField("channel_uuid", ch.UUID).Info("[dingtalk] Reply 需按场景调用钉钉开放接口（机器人群 / 单聊），请在后端扩展")
	return nil
}
