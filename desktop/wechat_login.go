package main

import (
	"fmt"
	"strings"

	"github.com/chowyu12/aiclaw/internal/plugins/wechat"
	"github.com/chowyu12/aiclaw/pkg/wechatlink"
)

// WeChatLoginQR is a login QR code for the WeChat connector.
type WeChatLoginQR struct {
	QRCode    string `json:"qrcode"`
	QRCodeURL string `json:"qrcode_url"`
}

// WeChatLoginStatus reports the progress of a scan.
type WeChatLoginStatus struct {
	// Status is one of wait, scaned, confirmed, expired.
	Status string `json:"status"`
	// Saved is true once credentials were written into the plugin's
	// configuration. The credentials themselves are never returned.
	Saved bool `json:"saved"`
}

// StartWeChatLogin fetches a QR code for signing the connector in.
func (a *App) StartWeChatLogin() (WeChatLoginQR, error) {
	if err := a.ready(); err != nil {
		return WeChatLoginQR{}, err
	}
	result, err := wechatlink.FetchQRCode(a.ctx)
	if err != nil {
		return WeChatLoginQR{}, err
	}
	return WeChatLoginQR{QRCode: result.QRCode, QRCodeURL: result.QRCodeURL}, nil
}

// PollWeChatLogin checks a scan and, once confirmed, stores the credentials in
// the plugin's configuration.
//
// The credentials never travel back to the UI: the caller learns only that the
// sign-in completed, the same way a stored secret is reported as set.
func (a *App) PollWeChatLogin(pluginUUID, qrcode string) (WeChatLoginStatus, error) {
	if err := a.ready(); err != nil {
		return WeChatLoginStatus{}, err
	}
	plugin, err := a.findPlugin(pluginUUID)
	if err != nil {
		return WeChatLoginStatus{}, err
	}
	if plugin.PluginID != "aiclaw.wechat" {
		return WeChatLoginStatus{}, fmt.Errorf("plugin %q is not the WeChat connector", plugin.Name)
	}
	result, err := wechatlink.PollQRStatus(a.ctx, qrcode)
	if err != nil {
		return WeChatLoginStatus{}, err
	}
	status := WeChatLoginStatus{Status: result.Status}
	if !strings.EqualFold(result.Status, "confirmed") || strings.TrimSpace(result.BotToken) == "" {
		return status, nil
	}
	config := a.pluginConfig()
	for key, value := range map[string]string{
		wechat.ConfigBotToken:  result.BotToken,
		wechat.ConfigBotID:     result.ILinkBotID,
		wechat.ConfigBaseURL:   result.BaseURL,
		wechat.ConfigILinkUser: result.ILinkUserID,
	} {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if err := config.Set(a.ctx, plugin, key, value); err != nil {
			return status, err
		}
	}
	status.Saved = true
	return status, nil
}
