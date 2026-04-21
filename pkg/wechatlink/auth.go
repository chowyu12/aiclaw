package wechatlink

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	qrCodeURL         = "https://ilinkai.weixin.qq.com/ilink/bot/get_bot_qrcode?bot_type=3"
	qrStatusURLPrefix = "https://ilinkai.weixin.qq.com/ilink/bot/get_qrcode_status?qrcode="
)

// FetchQRCode 获取微信 iLink 登录二维码。
func FetchQRCode(ctx context.Context) (*QRCodeResult, error) {
	resp, err := doGet(ctx, qrCodeURL)
	if err != nil {
		return nil, fmt.Errorf("fetch QR code: %w", err)
	}
	var qr qrCodeResp
	if err := json.Unmarshal(resp, &qr); err != nil {
		return nil, fmt.Errorf("parse QR response: %w", err)
	}
	return &QRCodeResult{
		QRCode:    qr.QRCode,
		QRCodeURL: qr.QRCodeImgContent,
	}, nil
}

// PollQRStatus 单次轮询扫码状态（长轮询，约 40 秒超时）。
func PollQRStatus(ctx context.Context, qrcode string) (*QRStatusResult, error) {
	pollCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	body, err := doGet(pollCtx, qrStatusURLPrefix+qrcode)
	if err != nil {
		return nil, err
	}
	var resp qrStatusResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse status: %w", err)
	}
	return &QRStatusResult{
		Status: resp.Status,
		Credentials: Credentials{
			BotToken:    resp.BotToken,
			ILinkBotID:  resp.ILinkBotID,
			BaseURL:     resp.BaseURL,
			ILinkUserID: resp.ILinkUserID,
		},
	}, nil
}

func doGet(ctx context.Context, url string) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := sharedHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}
