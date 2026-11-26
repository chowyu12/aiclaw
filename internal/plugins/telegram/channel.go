package telegram

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/chowyu12/aiclaw/internal/plugin"
	"github.com/chowyu12/aiclaw/internal/plugins/connector"
)

const ProviderName = "builtin:telegram"
const channelID = "telegram"

var tokenPattern = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`)

type Channel struct {
	endpoint string
	client   *http.Client
}

func New() plugin.Channel {
	return &Channel{endpoint: "https://api.telegram.org", client: connector.ChannelHTTPClient(40 * time.Second)}
}
func (*Channel) ID() string { return channelID }

type user struct {
	ID       int64
	IsBot    bool `json:"is_bot"`
	Username string
}

type message struct {
	ID       int64 `json:"message_id"`
	ThreadID int64 `json:"message_thread_id"`
	Text     string
	From     *user
	ReplyTo  *struct{ From *user } `json:"reply_to_message"`
	Chat     struct {
		ID          int64
		Type, Title string
	}
	Entities []struct {
		Type           string
		Offset, Length int
	}
}

type update struct {
	ID      int64 `json:"update_id"`
	Message *message
}

type response[T any] struct {
	OK     bool
	Result T
}

func (c *Channel) Run(ctx context.Context, deps plugin.ChannelDeps) error {
	token := strings.TrimSpace(deps.Config.String("bot_token"))
	if !tokenPattern.MatchString(token) {
		return errors.New("Telegram requires a valid bot token")
	}
	endpoint := c.endpoint + "/bot" + token + "/"
	var me response[user]
	if err := connector.PostJSON(ctx, c.client, endpoint+"getMe", struct{}{}, &me); err != nil {
		return err
	}
	if !me.OK || me.Result.ID == 0 || me.Result.Username == "" {
		return errors.New("Telegram rejected the bot token")
	}
	inbox := connector.NewTextInbox(ctx, deps, channelID)
	defer inbox.Close()
	var offset int64
	for ctx.Err() == nil {
		var batch response[[]update]
		request := map[string]any{"offset": offset, "timeout": 25, "allowed_updates": []string{"message"}}
		if err := connector.PostJSON(ctx, c.client, endpoint+"getUpdates", request, &batch); err != nil {
			return err
		}
		if !batch.OK {
			return errors.New("Telegram rejected polling; check the bot connection")
		}
		for _, entry := range batch.Result {
			if entry.ID < offset {
				continue
			}
			msg := entry.Message
			if inbound, ok := normalize(msg, me.Result); ok {
				if !inbox.Submit(strconv.FormatInt(entry.ID, 10), inbound, func(ctx context.Context, text string) error {
					return c.reply(ctx, endpoint, msg, text)
				}) {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					busy, cancel := context.WithTimeout(ctx, 3*time.Second)
					_ = c.reply(busy, endpoint, msg, "The assistant is busy. Please try again.")
					cancel()
				}
			}
			offset = entry.ID + 1
		}
	}
	return ctx.Err()
}

func normalize(msg *message, bot user) (plugin.Inbound, bool) {
	if msg == nil || msg.From == nil || msg.From.IsBot || msg.Chat.ID == 0 || msg.ID == 0 {
		return plugin.Inbound{}, false
	}
	text := strings.TrimSpace(msg.Text)
	switch msg.Chat.Type {
	case "private":
	case "group", "supergroup":
		mention := "@" + bot.Username
		targeted := msg.ReplyTo != nil && msg.ReplyTo.From != nil && msg.ReplyTo.From.ID == bot.ID
		units := utf16.Encode([]rune(msg.Text))
		for _, entity := range msg.Entities {
			if entity.Type != "mention" || entity.Offset < 0 || entity.Offset > len(units) || entity.Length <= 0 || entity.Length > len(units)-entity.Offset {
				continue
			}
			end := entity.Offset + entity.Length
			if !strings.EqualFold(string(utf16.Decode(units[entity.Offset:end])), mention) {
				continue
			}
			targeted = true
			text = strings.TrimSpace(string(utf16.Decode(units[:entity.Offset])) + string(utf16.Decode(units[end:])))
			break
		}
		if !targeted {
			return plugin.Inbound{}, false
		}
	default:
		return plugin.Inbound{}, false
	}
	key := strconv.FormatInt(msg.Chat.ID, 10)
	if msg.ThreadID != 0 {
		key += "/" + strconv.FormatInt(msg.ThreadID, 10)
	}
	return plugin.Inbound{ExternalKey: key, DisplayName: msg.Chat.Title, SenderID: strconv.FormatInt(msg.From.ID, 10), Text: text}, text != ""
}

func (c *Channel) reply(ctx context.Context, endpoint string, msg *message, text string) error {
	runes := []rune(text)
	for start := 0; start < len(runes); start += 2000 {
		params := map[string]any{"chat_id": msg.Chat.ID, "text": string(runes[start:min(start+2000, len(runes))])}
		if msg.ThreadID != 0 {
			params["message_thread_id"] = msg.ThreadID
		}
		var res response[jsonIgnored]
		if err := connector.PostJSON(ctx, c.client, endpoint+"sendMessage", params, &res); err != nil {
			return err
		}
		if !res.OK {
			return errors.New("Telegram rejected the reply")
		}
	}
	return nil
}

type jsonIgnored struct{}
