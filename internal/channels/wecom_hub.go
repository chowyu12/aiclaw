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

	client.OnNormalizedMessage(func(nm *wecomaibot.NormalizedMessage) {
		wecomDispatchInbound(bridge, chLive, client, nm)
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

func wecomDispatchInbound(bridge *Bridge, chLive *atomic.Pointer[model.Channel], client *wecomaibot.WSClient, nm *wecomaibot.NormalizedMessage) {
	ch := chLive.Load()
	if ch == nil || nm.Base == nil {
		return
	}

	cfg := []byte(ch.Config)
	corpID := cfgString(cfg, "corp_id")
	corpSecret := cfgString(cfg, "corp_secret")
	publicURL := strings.TrimRight(cfgString(cfg, "public_url"), "/")

	var files []model.ChatFile
	for _, u := range nm.ImageURLs {
		if u = strings.TrimSpace(u); u != "" {
			files = append(files, model.ChatFile{
				Type:           model.ChatFileImage,
				TransferMethod: model.TransferRemoteURL,
				URL:            u,
			})
		}
	}

	text := nm.Text
	if text == "" && len(files) == 0 {
		return
	}
	if text == "" && len(files) > 0 {
		text = "请描述这张图片"
	}

	meta := map[string]any{
		"msgid":   nm.Base.MsgID,
		"msgtype": nm.Base.MsgType,
	}
	if nm.FileURL != "" {
		meta["file_url"] = nm.FileURL
	}
	if nm.VideoURL != "" {
		meta["video_url"] = nm.VideoURL
	}
	for _, u := range nm.ImageURLs {
		if u != "" {
			meta["image_urls"] = nm.ImageURLs
			break
		}
	}

	in := &Inbound{
		ThreadKey: nm.ThreadKey,
		SenderID:  nm.SenderID,
		Text:      text,
		Files:     files,
		RawMeta:   meta,
		ReplyWith: func(ctx context.Context, reply string, images []*model.File) error {
			if err := client.ReplyText(nm.Frame, reply); err != nil {
				log.WithError(err).Error("[wecom] ReplyText failed")
				return err
			}
			for _, img := range images {
				if corpID != "" && corpSecret != "" {
					if err := client.ReplyImageByMediaID(ctx, nm.Frame, corpID, corpSecret, img.StoragePath, img.Filename); err != nil {
						log.WithError(err).WithField("file", img.Filename).Error("[wecom] send image via media_id failed")
					}
					continue
				}
				if publicURL != "" && img.UUID != "" {
					imgURL := publicURL + "/public/files/" + img.UUID
					if err := client.ReplyNewsCard(nm.Frame, img.Filename, imgURL); err != nil {
						log.WithError(err).WithField("file", img.Filename).Error("[wecom] send news card failed")
					}
				}
			}
			return nil
		},
	}
	bridge.HandleInboundAsync(context.Background(), ch, in, wecomDrv)
}
