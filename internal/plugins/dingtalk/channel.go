package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/chowyu12/aiclaw/internal/plugin"
	"github.com/chowyu12/aiclaw/internal/plugins/connector"
	"github.com/gorilla/websocket"
)

const ProviderName = "builtin:dingtalk"
const channelID = "dingtalk"
const botTopic = "/v1.0/im/bot/messages/get"

type Channel struct{ client *http.Client }

func New() plugin.Channel   { return &Channel{client: connector.ChannelHTTPClient(15 * time.Second)} }
func (*Channel) ID() string { return channelID }

type callback struct {
	ID                string `json:"msgId"`
	ConversationID    string
	ConversationType  string
	ConversationTitle string
	SenderID          string
	SenderNick        string
	IsInAtList        bool
	Kind              string `json:"msgtype"`
	Webhook           string `json:"sessionWebhook"`
	WebhookExpiry     int64  `json:"sessionWebhookExpiredTime"`
	Text              struct{ Content string }
}

type frame struct {
	Type    string
	Headers map[string]string
	Data    string
}

type ack struct {
	Code    int               `json:"code"`
	Headers map[string]string `json:"headers"`
	Message string            `json:"message"`
	Data    string            `json:"data"`
}

func (c *Channel) Run(ctx context.Context, deps plugin.ChannelDeps) error {
	id, secret := strings.TrimSpace(deps.Config.String("client_id")), strings.TrimSpace(deps.Config.String("client_secret"))
	if id == "" || secret == "" {
		return errors.New("DingTalk requires Client ID and Client Secret")
	}
	var details struct{ Endpoint, Ticket string }
	request := map[string]any{
		"clientId": id, "clientSecret": secret, "ua": "AIClaw",
		"subscriptions": []map[string]string{{"type": "CALLBACK", "topic": botTopic}},
	}
	if err := connector.PostJSON(ctx, c.client, "https://api.dingtalk.com/v1.0/gateway/connections/open", request, &details); err != nil {
		return err
	}
	endpoint, err := url.Parse(details.Endpoint)
	if err != nil || !serviceURL(endpoint, "wss") || details.Ticket == "" {
		return errors.New("DingTalk returned an invalid Stream endpoint")
	}
	query := endpoint.Query()
	query.Set("ticket", details.Ticket)
	endpoint.RawQuery = query.Encode()
	conn, res, err := websocket.DefaultDialer.DialContext(ctx, endpoint.String(), nil)
	if err != nil {
		if res != nil {
			res.Body.Close()
		}
		return errors.New("could not open DingTalk Stream connection")
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	conn.SetReadLimit(1 << 20)

	inbox := connector.NewTextInbox(ctx, deps, channelID)
	defer inbox.Close()
	for {
		if err := conn.SetReadDeadline(time.Now().Add(125 * time.Second)); err != nil {
			return errors.New("could not set DingTalk read deadline")
		}
		var event frame
		if err := conn.ReadJSON(&event); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("DingTalk Stream connection closed")
		}
		if event.Type == "SYSTEM" && event.Headers["topic"] == "disconnect" {
			return errors.New("DingTalk requested reconnection")
		}
		response := c.handle(event, inbox)
		if response == nil {
			continue
		}
		if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return errors.New("could not set DingTalk write deadline")
		}
		if err := conn.WriteJSON(response); err != nil {
			return errors.New("could not acknowledge DingTalk Stream event")
		}
	}
}

func (c *Channel) handle(event frame, inbox *connector.TextInbox) *ack {
	messageID := event.Headers["messageId"]
	if messageID == "" {
		return nil
	}
	response := &ack{Code: 200, Headers: map[string]string{"messageId": messageID, "contentType": "application/json"}, Message: "ok"}
	switch {
	case event.Type == "SYSTEM" && event.Headers["topic"] == "ping":
		response.Data = event.Data
	case event.Type == "CALLBACK" && event.Headers["topic"] == botTopic:
		var msg callback
		if json.Unmarshal([]byte(event.Data), &msg) != nil {
			response.Code, response.Message = 400, "invalid callback"
			return response
		}
		inbound, ok := normalize(msg)
		if ok && !inbox.Submit(msg.ID, inbound, func(ctx context.Context, text string) error {
			return c.reply(ctx, msg, text)
		}) {
			response.Code, response.Message = 503, "assistant busy"
		}
	}
	return response
}

func normalize(msg callback) (plugin.Inbound, bool) {
	target, err := url.Parse(msg.Webhook)
	text := strings.TrimSpace(msg.Text.Content)
	if msg.ID == "" || msg.Kind != "text" || msg.ConversationID == "" || msg.SenderID == "" || text == "" || err != nil || !serviceURL(target, "https") {
		return plugin.Inbound{}, false
	}
	switch msg.ConversationType {
	case "1":
	case "2":
		if !msg.IsInAtList {
			return plugin.Inbound{}, false
		}
	default:
		return plugin.Inbound{}, false
	}
	name := msg.ConversationTitle
	if name == "" {
		name = msg.SenderNick
	}
	return plugin.Inbound{ExternalKey: msg.ConversationID, DisplayName: name, SenderID: msg.SenderID, Text: text}, true
}

func serviceURL(endpoint *url.URL, scheme string) bool {
	if endpoint == nil || endpoint.Scheme != scheme || endpoint.User != nil || endpoint.Fragment != "" || (endpoint.Port() != "" && endpoint.Port() != "443") {
		return false
	}
	host := strings.ToLower(endpoint.Hostname())
	if scheme == "https" {
		return host == "oapi.dingtalk.com" && endpoint.Path == "/robot/sendBySession"
	}
	return strings.HasSuffix(host, ".dingtalk.com") && host != ".dingtalk.com"
}

func (c *Channel) reply(ctx context.Context, msg callback, text string) error {
	if msg.WebhookExpiry > 0 && time.Now().UnixMilli() >= msg.WebhookExpiry {
		return errors.New("DingTalk reply target expired")
	}
	runes := []rune(text)
	for start := 0; start < len(runes); start += 4000 {
		request := map[string]any{"msgtype": "text", "text": map[string]string{"content": string(runes[start:min(start+4000, len(runes))])}}
		var result struct {
			Code *int `json:"errcode"`
		}
		if err := connector.PostJSON(ctx, c.client, msg.Webhook, request, &result); err != nil {
			return err
		}
		if result.Code == nil || *result.Code != 0 {
			return errors.New("DingTalk rejected the reply")
		}
	}
	return nil
}
