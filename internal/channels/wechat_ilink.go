package channels

import (
	"context"

	"github.com/chowyu12/aiclaw/pkg/wechatlink"
)

// FetchWeChatQRCode 获取微信 iLink 登录二维码（供 handler 调用）。
func FetchWeChatQRCode(ctx context.Context) (*wechatlink.QRCodeResult, error) {
	return wechatlink.FetchQRCode(ctx)
}

// PollWeChatQRStatus 单次轮询扫码状态（供 handler 调用）。
func PollWeChatQRStatus(ctx context.Context, qrcode string) (*wechatlink.QRStatusResult, error) {
	return wechatlink.PollQRStatus(ctx, qrcode)
}
