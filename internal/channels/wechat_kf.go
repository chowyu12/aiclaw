package channels

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/url"
	"strings"

	log "github.com/sirupsen/logrus"
)

// 微信客服回调与企业微信 XML 形态相近，共用解析思路。

type wechatKFAdapter struct{}

func (wechatKFAdapter) HandleGET(_ ChannelConfig, q url.Values) WebhookHTTP {
	echo := strings.TrimSpace(q.Get("echostr"))
	if echo != "" {
		return WebhookHTTP{Status: 200, ContentType: "text/plain; charset=utf-8", Body: []byte(echo)}
	}
	return WebhookHTTP{Status: 200, ContentType: "application/json", Body: []byte(`{"ok":true}`)}
}

func (wechatKFAdapter) HandlePOST(_ ChannelConfig, body []byte, _ string, _ http.Header) (WebhookHTTP, *Inbound) {
	if in := parseWeComJSON(body); in != nil {
		return jsonOK(), in
	}
	if in := parseWeComXML(body); in != nil {
		return jsonOK(), in
	}
	return jsonOK(), nil
}

func (wechatKFAdapter) Reply(ctx context.Context, ch ChannelConfig, in *Inbound, text string) error {
	_ = ctx
	_ = in
	_ = text
	log.WithField("channel_uuid", ch.UUID).Info("[wechat_kf] Reply 需调用微信客服发送 API，请在后端扩展")
	return nil
}

func parseWeComJSON(body []byte) *Inbound {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil
	}
	text := ""
	if m, ok := root["text"].(map[string]any); ok {
		if c, _ := m["content"].(string); c != "" {
			text = strings.TrimSpace(c)
		}
	}
	if text == "" {
		if c, _ := root["Content"].(string); c != "" {
			text = strings.TrimSpace(c)
		}
	}
	if text == "" {
		return nil
	}
	from := firstString(root, "from", "FromUserName", "userid", "UserId", "open_userid")
	to := firstString(root, "to", "ToUserName", "chatid", "ChatId")
	thread := firstString(root, "chatid", "ChatId", "conversation_id")
	if thread == "" {
		thread = from + ":" + to
	}
	if thread == "" {
		thread = from
	}
	return &Inbound{
		ThreadKey: thread,
		SenderID:  from,
		Text:      text,
		RawMeta:   root,
	}
}

type wecomXML struct {
	FromUserName string `xml:"FromUserName"`
	ToUserName   string `xml:"ToUserName"`
	MsgType      string `xml:"MsgType"`
	Content      string `xml:"Content"`
}

func parseWeComXML(body []byte) *Inbound {
	s := strings.TrimSpace(string(body))
	if !strings.HasPrefix(s, "<xml") && !strings.HasPrefix(s, "<XML") && !strings.HasPrefix(s, "<?xml") {
		return nil
	}
	var x wecomXML
	if err := xml.Unmarshal(body, &x); err != nil {
		return nil
	}
	text := strings.TrimSpace(x.Content)
	if text == "" {
		return nil
	}
	from := strings.TrimSpace(x.FromUserName)
	to := strings.TrimSpace(x.ToUserName)
	thread := from + ":" + to
	if thread == ":" {
		thread = from
	}
	return &Inbound{ThreadKey: thread, SenderID: from, Text: text, RawMeta: map[string]any{"msg_type": x.MsgType}}
}
