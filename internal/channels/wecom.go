package channels

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	log "github.com/sirupsen/logrus"
)

// 企业微信智能机器人：消息经 WebSocket 长连接（bot_id + secret），参考
// 协议参考 https://developer.work.weixin.qq.com/document/path/101463；客户端见 pkg/wecomaibot。
// HTTP Webhook 仅作健康检查，不解析业务入站（避免与 WS 重复）。

type wecomAdapter struct{}

func (wecomAdapter) HandleGET(_ ChannelConfig, q url.Values) WebhookHTTP {
	echo := strings.TrimSpace(q.Get("echostr"))
	if echo != "" {
		if q.Get("msg_signature") != "" {
			log.Warn("[wecom] URL 含 msg_signature：智能机器人长连接模式请忽略 HTTP 回调，以 WebSocket 为准")
		}
		return WebhookHTTP{Status: 200, ContentType: "text/plain; charset=utf-8", Body: []byte(echo)}
	}
	return jsonOK()
}

func (wecomAdapter) HandlePOST(_ ChannelConfig, _ []byte, _ string, _ http.Header) (WebhookHTTP, *Inbound) {
	// 入站由 aibot WebSocket 处理
	return jsonOK(), nil
}

func (wecomAdapter) Reply(context.Context, ChannelConfig, *Inbound, string) error {
	return nil
}
