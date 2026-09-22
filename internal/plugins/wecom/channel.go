// Package wecom implements the bundled WeCom (企业微信) AI bot connector.
//
// The transport lives in pkg/wecomaibot; this package is the adapter between
// that protocol and the plugin channel contract. Inbound messages are data:
// they reach a turn only through the gateway, which decides whether the
// conversation is authorized and restricts what the turn may do.
package wecom

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	pluginpkg "github.com/chowyu12/aiclaw/internal/plugin"
	"github.com/chowyu12/aiclaw/internal/plugins/connector"
	"github.com/chowyu12/aiclaw/pkg/wecomaibot"
)

// ProviderName is the identifier the bundled manifest references.
const ProviderName = "builtin:wecom"

// ChannelID matches the manifest's channel contribution id.
const ChannelID = "wecom"

// maxConcurrentTurns bounds how many inbound messages run at once. The pace is
// controlled by whoever is messaging the bot, so it has to be bounded here.
const maxConcurrentTurns = 4

// streamFlushRunes is how much assistant output accumulates before a streaming
// update is pushed, so a long answer appears progressively without sending one
// frame per token.
const streamFlushRunes = 40

// Channel is the WeCom connector.
type Channel struct {
	// connect builds the transport; tests replace it.
	connect func(botID, secret string) (transport, error)
}

// transport is the part of the WeCom client this connector uses.
type transport interface {
	OnNormalizedMessage(handler func(*wecomaibot.NormalizedMessage))
	OnError(handler func(error))
	Connect()
	Disconnect()
	ReplyStream(frame *wecomaibot.WsFrame, streamID, content string, finish bool) error
}

// New builds the channel for the plugin host.
func New() pluginpkg.Channel { return &Channel{connect: dial} }

func (c *Channel) ID() string { return ChannelID }

// Run connects and serves until the context ends or the transport gives up.
//
// The transport reconnects on its own for transient drops; returning an error
// here hands the failure to the plugin host, which restarts the channel with
// freshly loaded configuration. That is what makes a rotated credential take
// effect without restarting the application.
func (c *Channel) Run(ctx context.Context, deps pluginpkg.ChannelDeps) error {
	botID := strings.TrimSpace(deps.Config.String("bot_id"))
	secret := strings.TrimSpace(deps.Config.String("bot_secret"))
	if botID == "" || secret == "" {
		return fmt.Errorf("wecom connector needs bot_id and bot_secret")
	}
	client, err := c.connect(botID, secret)
	if err != nil {
		return err
	}

	fatal := make(chan error, 1)
	client.OnError(func(err error) {
		select {
		case fatal <- err:
		default:
		}
	})

	dispatcher := connector.NewDispatcher(maxConcurrentTurns)
	client.OnNormalizedMessage(func(message *wecomaibot.NormalizedMessage) {
		if message == nil || message.Frame == nil {
			return
		}
		// The handler runs on the connection's read loop, so the turn must
		// not be executed here.
		accepted := dispatcher.Submit(ctx, message.ThreadKey, func(runCtx context.Context) {
			c.serve(runCtx, deps, client, message)
		})
		if !accepted {
			c.reply(client, message, "当前正在处理其他会话，请稍后再发一次。")
		}
	})

	client.Connect()
	defer func() {
		client.Disconnect()
		// Let in-flight turns finish so a reply is not cut in half.
		dispatcher.Wait()
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-fatal:
		return err
	}
}

// serve runs one inbound message as a turn and reports the outcome back.
func (c *Channel) serve(ctx context.Context, deps pluginpkg.ChannelDeps, client transport, message *wecomaibot.NormalizedMessage) {
	text := strings.TrimSpace(message.Text)
	if text == "" {
		return
	}
	streamID := message.Base.MsgID
	if streamID == "" {
		streamID = message.ThreadKey
	}
	stream := newStreamer(client, message, streamID)
	reply := connector.NewReply(stream.push)

	err := deps.Gateway.Submit(ctx, deps.PluginUUID, pluginpkg.Inbound{
		ChannelID:   ChannelID,
		ExternalKey: message.ThreadKey,
		DisplayName: displayName(message),
		SenderID:    message.SenderID,
		Text:        text,
	}, reply.Observe)

	switch {
	case errors.Is(err, pluginpkg.ErrBindingNotAllowed):
		stream.finish("这个会话还没有被授权访问助手，请在桌面端的插件设置里放行后再试。")
	case err != nil:
		deps.Log("wecom turn failed for %s: %v", message.ThreadKey, err)
		// The error may name internal paths or configuration, so the sender
		// gets an acknowledgement rather than the detail.
		stream.finish("处理这条消息时出错了，请稍后再试。")
	case reply.Failed() || strings.TrimSpace(reply.Text()) == "":
		stream.finish("这次没有得到可用的回复，请换个说法再试一次。")
	default:
		stream.finish(reply.Text())
	}
}

func (c *Channel) reply(client transport, message *wecomaibot.NormalizedMessage, text string) {
	streamID := message.Base.MsgID
	if streamID == "" {
		streamID = message.ThreadKey
	}
	_ = client.ReplyStream(message.Frame, streamID, text, true)
}

// streamer pushes assistant output progressively and closes the stream once.
//
// **Every frame carries the whole answer so far, not the latest chunk.** A
// WeCom stream frame replaces the bubble's content; it does not append to it.
// Sending only the newly accumulated runes made the bubble show one fragment
// at a time ("一段一段的") instead of a growing message.
type streamer struct {
	client   transport
	message  *wecomaibot.NormalizedMessage
	streamID string

	mu sync.Mutex
	// text is everything received so far; sinceFlush counts what arrived after
	// the last frame so a long answer is not sent one frame per token.
	text       []rune
	sinceFlush int
	done       bool
}

func newStreamer(client transport, message *wecomaibot.NormalizedMessage, streamID string) *streamer {
	return &streamer{client: client, message: message, streamID: streamID}
}

// push appends a delta and, once enough has accumulated since the last frame,
// sends the full text so far.
func (s *streamer) push(delta string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return nil
	}
	runes := []rune(delta)
	s.text = append(s.text, runes...)
	s.sinceFlush += len(runes)
	if s.sinceFlush < streamFlushRunes {
		return nil
	}
	s.sinceFlush = 0
	return s.client.ReplyStream(s.message.Frame, s.streamID, string(s.text), false)
}

// finish sends the complete answer and closes the stream. The full text is
// sent rather than the remaining buffer so the conversation shows one
// coherent message even if an intermediate push was dropped.
func (s *streamer) finish(text string) {
	s.mu.Lock()
	if s.done {
		s.mu.Unlock()
		return
	}
	s.done, s.text, s.sinceFlush = true, nil, 0
	s.mu.Unlock()
	_ = s.client.ReplyStream(s.message.Frame, s.streamID, text, true)
}

func displayName(message *wecomaibot.NormalizedMessage) string {
	if message.Base != nil && strings.TrimSpace(message.Base.ChatID) != "" {
		return "企业微信群 " + message.Base.ChatID
	}
	return "企业微信 " + message.SenderID
}

// dial builds the real transport.
func dial(botID, secret string) (transport, error) {
	client := wecomaibot.NewWSClient(wecomaibot.WSClientOptions{BotID: botID, Secret: secret})
	return &liveTransport{client: client}, nil
}

type liveTransport struct{ client *wecomaibot.WSClient }

func (t *liveTransport) OnNormalizedMessage(handler func(*wecomaibot.NormalizedMessage)) {
	t.client.OnNormalizedMessage(handler)
}

func (t *liveTransport) OnError(handler func(error)) { t.client.OnError(handler) }

func (t *liveTransport) Connect() { t.client.Connect() }

func (t *liveTransport) Disconnect() { t.client.Disconnect() }

func (t *liveTransport) ReplyStream(frame *wecomaibot.WsFrame, streamID, content string, finish bool) error {
	_, err := t.client.ReplyStream(frame, streamID, content, finish, nil, nil)
	return err
}
