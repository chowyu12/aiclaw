package channels

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/pkg/wechatlink"
)

var wechatILinkRuntimeDrv = &wechatILinkRuntimeDriver{}

type wechatILinkRunner struct {
	botID  string
	cancel context.CancelFunc
	chLive *atomic.Pointer[model.Channel]
}

type wechatILinkRuntimeDriver struct {
	mu   sync.Mutex
	runs map[int64]*wechatILinkRunner
}

func (w *wechatILinkRuntimeDriver) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for id, r := range w.runs {
		r.cancel()
		delete(w.runs, id)
		log.WithField("channel_id", id).Debug("[wechat] runtime stopped")
	}
}

func (w *wechatILinkRuntimeDriver) Refresh(ctx context.Context, all []*model.Channel, bridge *Bridge) {
	_ = ctx
	if bridge == nil {
		return
	}

	type wantRec struct {
		creds *wechatlink.Credentials
		ch    *model.Channel
	}
	want := make(map[int64]wantRec)
	for _, ch := range all {
		if ch == nil || ch.ChannelType != model.ChannelWeChat || !ch.Enabled {
			continue
		}
		creds := wechatILinkCredsFromConfig(ch)
		if creds == nil {
			log.WithField("channel_id", ch.ID).Debug("[wechat] 渠道已启用但缺少 iLink 凭据（请扫码登录）")
			continue
		}
		want[ch.ID] = wantRec{creds: creds, ch: ch}
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.runs == nil {
		w.runs = make(map[int64]*wechatILinkRunner)
	}

	for id, r := range w.runs {
		rec, ok := want[id]
		if ok && r.botID == rec.creds.ILinkBotID {
			if r.chLive != nil {
				r.chLive.Store(rec.ch)
			}
			continue
		}
		r.cancel()
		delete(w.runs, id)
		log.WithField("channel_id", id).Info("[wechat] 长轮询已停止（渠道禁用、删除或凭据变更）")
	}

	for id, rec := range want {
		if _, ok := w.runs[id]; ok {
			continue
		}
		runCtx, cancel := context.WithCancel(context.Background())
		chLive := &atomic.Pointer[model.Channel]{}
		chLive.Store(rec.ch)
		runner := &wechatILinkRunner{
			botID:  rec.creds.ILinkBotID,
			cancel: cancel,
			chLive: chLive,
		}
		w.runs[id] = runner

		client := wechatlink.NewClient(rec.creds, wechatlink.WithLogger(
			wechatlink.NewLoggerFunc(func(level, format string, v ...any) {
				entry := log.WithField("channel_id", id)
				switch level {
				case "ERROR":
					entry.Errorf("[wechat] "+format, v...)
				case "WARN":
					entry.Warnf("[wechat] "+format, v...)
				case "DEBUG":
					entry.Debugf("[wechat] "+format, v...)
				default:
					entry.Infof("[wechat] "+format, v...)
				}
			}),
		))
		monitor := wechatlink.NewMonitor(client, func(msg wechatlink.Message) {
			wechatILinkDispatch(client, chLive, bridge, msg)
		}, wechatlink.WithLogger(wechatlink.NewLoggerFunc(func(level, format string, v ...any) {
			entry := log.WithField("channel_id", id)
			switch level {
			case "ERROR":
				entry.Errorf("[wechat] "+format, v...)
			case "WARN":
				entry.Warnf("[wechat] "+format, v...)
			default:
				entry.Debugf("[wechat] "+format, v...)
			}
		})))
		go monitor.Run(runCtx)
		log.WithField("channel_id", id).Info("[wechat] 已启动 iLink 长轮询消息监听")
	}
}

func wechatILinkCredsFromConfig(ch *model.Channel) *wechatlink.Credentials {
	botToken := cfgString([]byte(ch.Config), "bot_token")
	botID := cfgString([]byte(ch.Config), "ilink_bot_id")
	if botToken == "" || botID == "" {
		return nil
	}
	return &wechatlink.Credentials{
		BotToken:    botToken,
		ILinkBotID:  botID,
		BaseURL:     cfgString([]byte(ch.Config), "base_url"),
		ILinkUserID: cfgString([]byte(ch.Config), "ilink_user_id"),
	}
}

func wechatILinkDispatch(client *wechatlink.Client, chLive *atomic.Pointer[model.Channel], bridge *Bridge, msg wechatlink.Message) {
	ch := chLive.Load()
	if ch == nil {
		return
	}
	fromUser := strings.TrimSpace(msg.FromUserID)
	contextToken := msg.ContextToken
	clientID := uuid.New().String()

	go func() {
		if err := client.SendTyping(context.Background(), fromUser, contextToken); err != nil {
			log.WithError(err).WithField("channel_id", ch.ID).Debug("[wechat] sendTyping failed")
		}
	}()

	var files []model.ChatFile
	for _, u := range msg.ImageURLs {
		files = append(files, model.ChatFile{
			Type:           model.ChatFileImage,
			TransferMethod: model.TransferRemoteURL,
			URL:            u,
		})
	}

	text := msg.Text
	if text == "" && len(files) > 0 {
		text = "请描述这张图片"
	}

	in := &Inbound{
		ThreadKey: fromUser,
		SenderID:  fromUser,
		Text:      text,
		Files:     files,
		RawMeta: map[string]any{
			"context_token": contextToken,
		},
		ReplyWith: func(ctx context.Context, reply string) error {
			return client.SendMessage(ctx, fromUser, contextToken, clientID, reply)
		},
	}
	bridge.HandleInboundAsync(context.Background(), ch, in, noopDrv)
}
