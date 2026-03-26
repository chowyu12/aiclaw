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

var wecomRuntimeDrv = &wecomRuntimeDriver{}

type wecomRuntimeDriver struct {
	mu   sync.Mutex
	runs map[int64]*wecomRunner
}

func (w *wecomRuntimeDriver) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for id, r := range w.runs {
		if r.client != nil {
			r.client.Disconnect()
		}
		delete(w.runs, id)
		log.WithField("channel_id", id).Debug("[wecom] runtime stopped client")
	}
}

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
			log.WithField("channel_id", ch.ID).Warn("[wecom] 渠道已启用但缺少 bot_id/secret，未启动监听")
			continue
		}
		want[ch.ID] = wantRec{botID: botID, secret: sec, ch: ch}
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.runs == nil {
		w.runs = make(map[int64]*wecomRunner)
	}

	for id, r := range w.runs {
		if _, ok := want[id]; ok {
			continue
		}
		if r.client != nil {
			r.client.Disconnect()
		}
		delete(w.runs, id)
		log.WithField("channel_id", id).Info("[wecom] WebSocket 已停止（渠道禁用、删除或缺少 bot_id/secret）")
	}

	for id, rec := range want {
		if r, ok := w.runs[id]; ok && r.botID == rec.botID && r.secret == rec.secret {
			if r.client != nil && r.client.IsConnected() {
				if r.chLive != nil {
					r.chLive.Store(rec.ch)
				}
				continue
			}
		}
		if r, ok := w.runs[id]; ok && r.client != nil {
			r.client.Disconnect()
			delete(w.runs, id)
		}
		cli, holder := w.connectClient(bridge, rec.ch, rec.botID, rec.secret)
		if cli == nil {
			continue
		}
		w.runs[id] = &wecomRunner{botID: rec.botID, secret: rec.secret, client: cli, chLive: holder}
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

	client.OnMessageVoice(func(frame *wecomaibot.WsFrame) {
		var msg wecomaibot.VoiceMessage
		if err := wecomaibot.ParseMessageBody(frame, &msg); err != nil {
			log.WithError(err).WithField("channel_id", ch.ID).Debug("[wecom] parse voice message")
			return
		}
		text := strings.TrimSpace(msg.Voice.Content)
		dispatch(frame, &msg.BaseMessage, text, nil)
	})

	client.OnMessageFile(func(frame *wecomaibot.WsFrame) {
		var msg wecomaibot.FileMessage
		if err := wecomaibot.ParseMessageBody(frame, &msg); err != nil {
			log.WithError(err).WithField("channel_id", ch.ID).Debug("[wecom] parse file message")
			return
		}
		extra := map[string]any{}
		if u := strings.TrimSpace(msg.File.URL); u != "" {
			extra["file_url"] = u
		}
		dispatch(frame, &msg.BaseMessage, "[文件] "+msg.File.URL, extra)
	})

	client.OnMessageVideo(func(frame *wecomaibot.WsFrame) {
		var msg wecomaibot.VideoMessage
		if err := wecomaibot.ParseMessageBody(frame, &msg); err != nil {
			log.WithError(err).WithField("channel_id", ch.ID).Debug("[wecom] parse video message")
			return
		}
		extra := map[string]any{}
		if u := strings.TrimSpace(msg.Video.URL); u != "" {
			extra["video_url"] = u
		}
		dispatch(frame, &msg.BaseMessage, "[视频] "+msg.Video.URL, extra)
	})

	client.OnMessageStream(func(frame *wecomaibot.WsFrame) {
		var msg wecomaibot.StreamMessage
		if err := wecomaibot.ParseMessageBody(frame, &msg); err != nil {
			log.WithError(err).WithField("channel_id", ch.ID).Debug("[wecom] parse stream refresh")
			return
		}
		log.WithFields(log.Fields{"channel_id": ch.ID, "stream_id": msg.Stream.ID}).Debug("[wecom] 收到流式消息刷新回调")
	})

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

	var files []model.ChatFile
	if urls, ok := extra["image_urls"].([]string); ok {
		for _, u := range urls {
			if u = strings.TrimSpace(u); u != "" {
				files = append(files, model.ChatFile{
					Type:           model.ChatFileImage,
					TransferMethod: model.TransferRemoteURL,
					URL:            u,
				})
			}
		}
	}

	if text == "" && len(files) == 0 {
		return
	}
	if text == "" && len(files) > 0 {
		text = "请描述这张图片"
	}

	c := strings.TrimSpace(base.ChatID)
	u := strings.TrimSpace(base.From.UserID)
	thread := c
	if thread == "" {
		thread = u
	}
	meta := map[string]any{
		"msgid":   base.MsgID,
		"msgtype": base.MsgType,
	}
	for k, v := range extra {
		meta[k] = v
	}
	in := &Inbound{
		ThreadKey: thread,
		SenderID:  base.From.UserID,
		Text:      text,
		Files:     files,
		RawMeta:   meta,
		ReplyWith: func(ctx context.Context, reply string) error {
			streamID := fmt.Sprintf("stream_%s", frame.Headers.ReqID)
			_, err := client.ReplyStream(frame, streamID, reply, true, nil, nil)
			return err
		},
	}
	bridge.HandleInboundAsync(context.Background(), chCur, in, wecomDrv)
}
