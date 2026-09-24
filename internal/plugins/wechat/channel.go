// Package wechat implements the bundled personal WeChat connector.
//
// The transport lives in pkg/wechatlink, which speaks to a third-party iLink
// relay rather than an official WeChat API. That is a property the settings UI
// has to state: availability and account safety depend on that relay, not on
// this application. See docs/design/plugin-system.md.
package wechat

import (
	"context"
	"errors"
	"fmt"
	"strings"

	pluginpkg "github.com/chowyu12/aiclaw/internal/plugin"
	"github.com/chowyu12/aiclaw/internal/plugins/connector"
	"github.com/chowyu12/aiclaw/pkg/wechatlink"
)

// ProviderName is the identifier the bundled manifest references.
const ProviderName = "builtin:wechat"

// ChannelID matches the manifest's channel contribution id.
const ChannelID = "wechat"

// Configuration keys filled in by the QR login flow.
const (
	ConfigBotToken   = "bot_token"
	ConfigBotID      = "ilink_bot_id"
	ConfigBaseURL    = "base_url"
	ConfigILinkUser  = "ilink_user_id"
	maxConcurrentRun = 4
)

// Channel is the WeChat connector.
type Channel struct {
	// newSession builds the transport; tests replace it. The logger goes in
	// here rather than being attached later: the transport logs from the
	// moment it starts, and its first lines (handshake, credentials rejected)
	// are exactly the ones worth having.
	newSession func(credentials *wechatlink.Credentials, log func(string, ...any)) session
}

// session is the part of the WeChat client this connector uses.
type session interface {
	// Listen blocks, delivering inbound messages until ctx ends or the
	// transport gives up. It returns why it stopped: nil when ctx ended,
	// otherwise the failure the supervisor should act on.
	Listen(ctx context.Context, handler func(wechatlink.Message)) error
	SendText(ctx context.Context, toUserID, contextToken, text string) error
	SendTyping(ctx context.Context, toUserID, contextToken string) error
}

// New builds the channel for the plugin host.
func New() pluginpkg.Channel { return &Channel{newSession: dial} }

func (c *Channel) ID() string { return ChannelID }

// Run polls for messages until the context ends.
//
// The long-poll loop retries transient failures internally. A credential that
// stopped working surfaces as a persistent failure there, which is why the
// channel returns once the loop exits: the plugin host then restarts it with
// freshly loaded configuration, or gives up and reports the failure.
func (c *Channel) Run(ctx context.Context, deps pluginpkg.ChannelDeps) error {
	credentials := &wechatlink.Credentials{
		BotToken:    strings.TrimSpace(deps.Config.String(ConfigBotToken)),
		ILinkBotID:  strings.TrimSpace(deps.Config.String(ConfigBotID)),
		BaseURL:     strings.TrimSpace(deps.Config.String(ConfigBaseURL)),
		ILinkUserID: strings.TrimSpace(deps.Config.String(ConfigILinkUser)),
	}
	if credentials.BotToken == "" || credentials.ILinkBotID == "" {
		return fmt.Errorf("wechat connector is not signed in; scan the login QR code first")
	}

	dispatcher := connector.NewDispatcher(maxConcurrentRun)
	remote := c.newSession(credentials, deps.Log)
	listenErr := remote.Listen(ctx, func(message wechatlink.Message) {
		if strings.TrimSpace(message.Text) == "" {
			// Image-only messages are not served yet; see the package notes.
			return
		}
		accepted := dispatcher.Submit(ctx, message.FromUserID, func(runCtx context.Context) {
			c.serve(runCtx, deps, remote, message)
		})
		if !accepted {
			_ = remote.SendText(ctx, message.FromUserID, message.ContextToken,
				"当前正在处理其他会话，请稍后再发一次。")
		}
	})
	dispatcher.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	if listenErr != nil {
		// 凭据失效是这里最常见的原因，而它只有重新扫码才能修——把话说全，
		// 用户在插件页看到的就是这一句。
		return fmt.Errorf("微信长轮询中断（凭据可能已失效，重新扫码登录）：%w", listenErr)
	}
	return fmt.Errorf("wechat message loop stopped")
}

func (c *Channel) serve(ctx context.Context, deps pluginpkg.ChannelDeps, remote session, message wechatlink.Message) {
	// The relay shows a typing indicator, so the sender sees the agent is
	// working rather than silence.
	_ = remote.SendTyping(ctx, message.FromUserID, message.ContextToken)

	// This transport has no streaming reply, so only the final text is sent.
	reply := connector.NewReply(nil)
	err := deps.Gateway.Submit(ctx, deps.PluginUUID, pluginpkg.Inbound{
		ChannelID:   ChannelID,
		ExternalKey: message.FromUserID,
		DisplayName: "微信 " + message.FromUserID,
		SenderID:    message.FromUserID,
		Text:        strings.TrimSpace(message.Text),
	}, reply.Observe)

	text := ""
	switch {
	case errors.Is(err, pluginpkg.ErrBindingNotAllowed):
		text = "这个会话还没有被授权访问助手，请在桌面端的插件设置里放行后再试。"
	case err != nil:
		deps.Log("wechat turn failed for %s: %v", message.FromUserID, err)
		// The error may name internal paths or configuration; the sender gets
		// an acknowledgement rather than the detail.
		text = "处理这条消息时出错了，请稍后再试。"
	case reply.Failed() || strings.TrimSpace(reply.Text()) == "":
		text = "这次没有得到可用的回复，请换个说法再试一次。"
	default:
		text = reply.Text()
	}
	if sendErr := remote.SendText(ctx, message.FromUserID, message.ContextToken, text); sendErr != nil {
		deps.Log("wechat reply failed for %s: %v", message.FromUserID, sendErr)
	}
}

// dial builds the real transport.
//
// **日志要接上。** 早先客户端与监听器都用的是空日志器，于是整条长轮询——握手、
// 凭据被拒、退避重试——在应用日志里一个字都没有。企业微信那边失败会记一行，
// 微信这边什么都没有，用户看到的就是「连上了但收不到消息」而无从排查。
func dial(credentials *wechatlink.Credentials, log func(string, ...any)) session {
	logger := wechatlink.NewLoggerFunc(func(level, format string, v ...any) {
		// Debug 不进应用日志：一次握手一条就够了，每轮都记会把日志冲掉。
		if level == "DEBUG" {
			return
		}
		log("wechat ["+level+"] "+format, v...)
	})
	client := wechatlink.NewClient(credentials, wechatlink.WithLogger(logger))
	return &liveSession{client: client, logger: logger}
}

type liveSession struct {
	client *wechatlink.Client
	logger wechatlink.Logger
}

func (s *liveSession) Listen(ctx context.Context, handler func(wechatlink.Message)) error {
	return wechatlink.NewMonitor(s.client, wechatlink.MessageHandler(handler),
		wechatlink.WithLogger(s.logger)).Run(ctx)
}

func (s *liveSession) SendText(ctx context.Context, toUserID, contextToken, text string) error {
	return s.client.SendMessage(ctx, toUserID, contextToken, "", text)
}

func (s *liveSession) SendTyping(ctx context.Context, toUserID, contextToken string) error {
	return s.client.SendTyping(ctx, toUserID, contextToken)
}
