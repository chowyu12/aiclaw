package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/chowyu12/aiclaw/internal/plugin"
	"github.com/chowyu12/aiclaw/internal/plugins/connector"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

const ProviderName = "builtin:feishu"
const channelID = "feishu"

type Channel struct{}

func New() plugin.Channel   { return &Channel{} }
func (*Channel) ID() string { return channelID }

func (*Channel) Run(ctx context.Context, deps plugin.ChannelDeps) error {
	id := strings.TrimSpace(deps.Config.String("app_id"))
	secret := strings.TrimSpace(deps.Config.String("app_secret"))
	if id == "" || secret == "" {
		return errors.New("Feishu requires App ID and App Secret")
	}
	// SDK logs can include inbound payloads or connection URLs with credentials.
	api := lark.NewClient(id, secret, lark.WithLogger(quietLogger{}), lark.WithReqTimeout(15*time.Second))
	info, err := api.Get(ctx, "/open-apis/bot/v3/info", nil, larkcore.AccessTokenTypeTenant)
	var bot struct {
		Code int
		Bot  struct {
			OpenID string `json:"open_id"`
		}
	}
	if err != nil || info == nil || info.StatusCode != 200 || json.Unmarshal(info.RawBody, &bot) != nil || bot.Code != 0 || bot.Bot.OpenID == "" {
		return errors.New("Feishu bot information unavailable; verify credentials and enable the bot")
	}
	inbox := connector.NewTextInbox(ctx, deps, channelID)
	defer inbox.Close()
	handler := dispatcher.NewEventDispatcher("", "").OnP2MessageReceiveV1(func(_ context.Context, event *larkim.P2MessageReceiveV1) error {
		msg, messageID, ok := normalize(event, bot.Bot.OpenID)
		if !ok {
			return nil
		}
		if !inbox.Submit(messageID, msg, func(ctx context.Context, text string) error {
			runes := []rune(text)
			for start := 0; start < len(runes); start += 4000 {
				content, _ := json.Marshal(map[string]string{"text": string(runes[start:min(start+4000, len(runes))])})
				resp, err := api.Im.V1.Message.Reply(ctx, larkim.NewReplyMessageReqBuilder().MessageId(messageID).
					Body(larkim.NewReplyMessageReqBodyBuilder().MsgType("text").Content(string(content)).Build()).Build())
				if err != nil || !resp.Success() {
					return errors.New("Feishu rejected the reply")
				}
			}
			return nil
		}) {
			return errors.New("Feishu message queue is full")
		}
		return nil
	})
	handler.InitConfig(larkevent.WithLogger(quietLogger{}))
	client := larkws.NewClient(id, secret, larkws.WithEventHandler(handler), larkws.WithLogger(quietLogger{}), larkws.WithAutoReconnect(false))
	if err := client.Start(ctx); err != nil && ctx.Err() == nil {
		return errors.New("Feishu long connection stopped; verify long-connection event subscription")
	}
	return ctx.Err()
}

func normalize(event *larkim.P2MessageReceiveV1, botID string) (plugin.Inbound, string, bool) {
	if event == nil || event.Event == nil || event.Event.Sender == nil || event.Event.Message == nil {
		return plugin.Inbound{}, "", false
	}
	sender, msg := event.Event.Sender, event.Event.Message
	if sender.SenderId == nil || value(sender.SenderType) != "user" || value(msg.MessageType) != "text" {
		return plugin.Inbound{}, "", false
	}
	chatID, messageID := value(msg.ChatId), value(msg.MessageId)
	var content struct{ Text string }
	if chatID == "" || messageID == "" || json.Unmarshal([]byte(value(msg.Content)), &content) != nil {
		return plugin.Inbound{}, "", false
	}
	switch value(msg.ChatType) {
	case "p2p":
	case "group":
		targeted := false
		for _, mention := range msg.Mentions {
			if mention != nil && mention.Id != nil && value(mention.Id.OpenId) == botID && value(mention.Key) != "" {
				targeted = true
				content.Text = strings.ReplaceAll(content.Text, *mention.Key, "")
			}
		}
		if !targeted {
			return plugin.Inbound{}, "", false
		}
	default:
		return plugin.Inbound{}, "", false
	}
	if thread := value(msg.ThreadId); thread != "" {
		chatID += "/" + thread
	}
	text := strings.TrimSpace(content.Text)
	return plugin.Inbound{ExternalKey: chatID, SenderID: value(sender.SenderId.OpenId), Text: text}, messageID, text != ""
}

func value(text *string) string {
	if text == nil {
		return ""
	}
	return *text
}

type quietLogger struct{}

func (quietLogger) Debug(context.Context, ...any) {}
func (quietLogger) Info(context.Context, ...any)  {}
func (quietLogger) Warn(context.Context, ...any)  {}
func (quietLogger) Error(context.Context, ...any) {}
