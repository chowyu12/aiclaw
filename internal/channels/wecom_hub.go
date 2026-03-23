package channels

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/pkg/wecomaibot"
)

type wecomRunner struct {
	botID, secret string
	client        *wecomaibot.WSClient
	chLive        *atomic.Pointer[model.Channel]
}

var (
	wecomRuntimeDrv = &wecomRuntimeDriver{}
	wecomRunMu      sync.Mutex
	wecomRun        = make(map[int64]*wecomRunner)
)

type wecomRuntimeDriver struct{}

func (*wecomRuntimeDriver) Stop() {
	wecomRunMu.Lock()
	defer wecomRunMu.Unlock()
	for id, r := range wecomRun {
		if r.client != nil {
			r.client.Disconnect()
		}
		delete(wecomRun, id)
		log.WithField("channel_id", id).Debug("[wecom] runtime stopped client")
	}
}

// Refresh 根据已启用的 wecom 渠道（配置含 bot_id + secret）重建连接。
func (w *wecomRuntimeDriver) Refresh(ctx context.Context, all []*model.Channel, bridge *Bridge) {
	_ = ctx
	if bridge == nil {
		return
	}

	type wantRec struct {
		botID, secret string
		ch            *model.Channel
	}
	want := make(map[int64]wantRec)
	for _, ch := range all {
		if ch == nil || ch.ChannelType != model.ChannelWeCom || !ch.Enabled {
			continue
		}
		botID := cfgString([]byte(ch.Config), "bot_id")
		sec := cfgString([]byte(ch.Config), "secret")
		if botID == "" || sec == "" {
			continue
		}
		want[ch.ID] = wantRec{botID: botID, secret: sec, ch: ch}
	}

	wecomRunMu.Lock()
	defer wecomRunMu.Unlock()

	for id, r := range wecomRun {
		if _, ok := want[id]; ok {
			continue
		}
		if r.client != nil {
			r.client.Disconnect()
		}
		delete(wecomRun, id)
		log.WithField("channel_id", id).Info("[wecom] WebSocket 已停止（渠道禁用、删除或缺少 bot_id/secret）")
	}

	for id, rec := range want {
		if r, ok := wecomRun[id]; ok && r.botID == rec.botID && r.secret == rec.secret {
			// 仅依据 TCP 连通判断复用；认证完成前有窗口收不到推送，见 OnAuthenticated 日志。
			if r.client != nil && r.client.IsConnected() {
				if r.chLive != nil {
					r.chLive.Store(rec.ch)
				}
				continue
			}
		}
		if r, ok := wecomRun[id]; ok && r.client != nil {
			r.client.Disconnect()
			delete(wecomRun, id)
		}
		cli, holder := w.connectClient(bridge, rec.ch, rec.botID, rec.secret)
		if cli == nil {
			continue
		}
		wecomRun[id] = &wecomRunner{botID: rec.botID, secret: rec.secret, client: cli, chLive: holder}
		log.WithField("channel_id", id).Info("[wecom] 已发起智能机器人 WebSocket 连接（异步拨号与订阅），认证成功后将打印「长连接已就绪」")
	}
}

func (*wecomRuntimeDriver) connectClient(bridge *Bridge, ch *model.Channel, botID, secret string) (*wecomaibot.WSClient, *atomic.Pointer[model.Channel]) {
	jl := wecomaibot.NewLoggerFunc(func(level, format string, v ...any) {
		e := log.WithField("channel_id", ch.ID).WithField("subsystem", "wecom-aibot")
		msg := fmt.Sprintf(format, v...)
		switch level {
		case "DEBUG":
			e.Debug(msg)
		case "INFO":
			e.Info(msg)
		case "WARN":
			e.Warn(msg)
		default:
			e.Error(msg)
		}
	})
	client := wecomaibot.NewWSClient(wecomaibot.WSClientOptions{
		BotID:  botID,
		Secret: secret,
		Logger: jl,
	})

	chLive := &atomic.Pointer[model.Channel]{}
	chLive.Store(ch)

	dispatch := func(frame *wecomaibot.WsFrame, base *wecomaibot.BaseMessage, userText string, extra map[string]any) {
		wecomDispatchInbound(bridge, chLive, client, frame, base, userText, extra)
	}

	client.OnMessageText(func(frame *wecomaibot.WsFrame) {
		var msg wecomaibot.TextMessage
		if err := wecomaibot.ParseMessageBody(frame, &msg); err != nil {
			log.WithError(err).WithField("channel_id", ch.ID).Debug("[wecom] parse text message")
			return
		}
		text := strings.TrimSpace(msg.Text.Content)
		dispatch(frame, &msg.BaseMessage, text, nil)
	})

	client.OnMessageMixed(func(frame *wecomaibot.WsFrame) {
		var msg wecomaibot.MixedMessage
		if err := wecomaibot.ParseMessageBody(frame, &msg); err != nil {
			log.WithError(err).WithField("channel_id", ch.ID).Debug("[wecom] parse mixed message")
			return
		}
		text := wecomaibot.MixedToUserVisibleText(&msg)
		extra := map[string]any{}
		if urls := wecomaibot.CollectImageURLsFromMixed(&msg); len(urls) > 0 {
			extra["image_urls"] = urls
		}
		dispatch(frame, &msg.BaseMessage, text, extra)
	})

	client.OnMessageImage(func(frame *wecomaibot.WsFrame) {
		var msg wecomaibot.ImageMessage
		if err := wecomaibot.ParseMessageBody(frame, &msg); err != nil {
			log.WithError(err).WithField("channel_id", ch.ID).Debug("[wecom] parse image message")
			return
		}
		text := wecomaibot.ImageToUserVisibleText(&msg)
		extra := map[string]any{}
		if u := strings.TrimSpace(msg.Image.URL); u != "" {
			extra["image_urls"] = []string{u}
		}
		dispatch(frame, &msg.BaseMessage, text, extra)
	})

	// Connect() 仅异步拨号；日志若写「已连接」会误导：须等 SUBSCRIBE 成功后才算真正可收消息。
	client.OnAuthenticated(func() {
		log.WithField("channel_id", ch.ID).Info("[wecom] 长连接已就绪（订阅/认证成功），可接收用户消息")
	})
	client.OnReconnecting(func(n int) {
		log.WithFields(log.Fields{"channel_id": ch.ID, "attempt": n}).Warn("[wecom] WebSocket 正在重连，此期间可能收不到消息")
	})
	client.OnError(func(err error) {
		log.WithError(err).WithField("channel_id", ch.ID).Error("[wecom] WebSocket 错误")
	})

	client.Connect()
	return client, chLive
}

func wecomDispatchInbound(bridge *Bridge, chLive *atomic.Pointer[model.Channel], client *wecomaibot.WSClient, frame *wecomaibot.WsFrame, base *wecomaibot.BaseMessage, userText string, extra map[string]any) {
	chCur := chLive.Load()
	if chCur == nil || base == nil {
		return
	}
	text := strings.TrimSpace(userText)
	if text == "" {
		return
	}
	c := strings.TrimSpace(base.ChatID)
	u := strings.TrimSpace(base.From.UserID)
	thread := c
	if thread == "" {
		thread = u
	}
	var aliases []string
	if c != "" && u != "" && c != u {
		if thread == c {
			aliases = append(aliases, u)
		} else {
			aliases = append(aliases, c)
		}
	}
	meta := map[string]any{
		"msgid":   base.MsgID,
		"msgtype": base.MsgType,
	}
	for k, v := range extra {
		meta[k] = v
	}
	in := &Inbound{
		ThreadKey:        thread,
		ThreadKeyAliases: aliases,
		SenderID:         base.From.UserID,
		Text:             text,
		RawMeta:          meta,
		ReplyWith: func(ctx context.Context, reply string) error {
			streamID := fmt.Sprintf("stream_%s", frame.Headers.ReqID)
			_, err := client.ReplyStream(frame, streamID, reply, true, nil, nil)
			return err
		},
	}
	bridge.HandleInboundAsync(context.Background(), chCur, in, wecomDrv)
}
