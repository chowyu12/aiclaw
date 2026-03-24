package channels

import "github.com/chowyu12/aiclaw/internal/model"

var (
	wecomDrv    = &wecomAdapter{}
	feishuDrv   = &feishuAdapter{}
	dingTalkDrv = &dingTalkAdapter{}
	whatsAppDrv = &whatsAppAdapter{}
	telegramDrv = &telegramAdapter{}
	noopDrv     = &noopAdapter{}
)

var webhookDrivers = map[model.ChannelType]WebhookDriver{
	model.ChannelWeCom:    wecomDrv,
	model.ChannelFeishu:   feishuDrv,
	model.ChannelDingTalk: dingTalkDrv,
	model.ChannelWhatsApp: whatsAppDrv,
	model.ChannelTelegram: telegramDrv,
}

// runtimeDrivers 需要后台长连接的渠道驱动（如企微 WebSocket、微信 iLink 长轮询）。
var runtimeDrivers = map[model.ChannelType]ChannelDriver{
	model.ChannelWeCom:  wecomRuntimeDrv,
	model.ChannelWeChat: wechatILinkRuntimeDrv,
}

// WebhookFor 返回对应平台 Webhook 驱动；未知类型返回 noop。
func WebhookFor(t model.ChannelType) WebhookDriver {
	if d, ok := webhookDrivers[t]; ok {
		return d
	}
	return noopDrv
}
