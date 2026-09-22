package wecom

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	pluginpkg "github.com/chowyu12/aiclaw/internal/plugin"
	"github.com/chowyu12/aiclaw/internal/protocol"
	"github.com/chowyu12/aiclaw/pkg/wecomaibot"
)

// fakeTransport stands in for the WeCom WebSocket. Tests never open a socket:
// a connector test must not reach someone's real bot.
type fakeTransport struct {
	mu       sync.Mutex
	replies  []sentReply
	onMsg    func(*wecomaibot.NormalizedMessage)
	onErr    func(error)
	connects int
}

type sentReply struct {
	content string
	finish  bool
}

func (t *fakeTransport) OnNormalizedMessage(handler func(*wecomaibot.NormalizedMessage)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onMsg = handler
}

func (t *fakeTransport) OnError(handler func(error)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onErr = handler
}

func (t *fakeTransport) Connect() {
	t.mu.Lock()
	t.connects++
	t.mu.Unlock()
}

func (t *fakeTransport) Disconnect() {}

func (t *fakeTransport) ReplyStream(_ *wecomaibot.WsFrame, _, content string, finish bool) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.replies = append(t.replies, sentReply{content: content, finish: finish})
	return nil
}

func (t *fakeTransport) deliver(message *wecomaibot.NormalizedMessage) {
	t.mu.Lock()
	handler := t.onMsg
	t.mu.Unlock()
	handler(message)
}

func (t *fakeTransport) fail(err error) {
	t.mu.Lock()
	handler := t.onErr
	t.mu.Unlock()
	handler(err)
}

func (t *fakeTransport) sent() []sentReply {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]sentReply(nil), t.replies...)
}

func (t *fakeTransport) finalReply() string {
	for _, reply := range t.sent() {
		if reply.finish {
			return reply.content
		}
	}
	return ""
}

// fakeGateway records submissions and scripts the turn's events.
type fakeGateway struct {
	mu        sync.Mutex
	submitted []pluginpkg.Inbound
	err       error
	events    []protocol.Event
	delay     time.Duration
}

func (g *fakeGateway) Submit(ctx context.Context, _ string, message pluginpkg.Inbound, observe func(protocol.Event) error) error {
	g.mu.Lock()
	g.submitted = append(g.submitted, message)
	err, events, delay := g.err, g.events, g.delay
	g.mu.Unlock()
	if delay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	for _, event := range events {
		if observeErr := observe(event); observeErr != nil {
			return observeErr
		}
	}
	return err
}

func (g *fakeGateway) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.submitted)
}

func (g *fakeGateway) last() pluginpkg.Inbound {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.submitted[len(g.submitted)-1]
}

func textMessage(chatID, sender, text string) *wecomaibot.NormalizedMessage {
	base := &wecomaibot.BaseMessage{MsgID: "msg-1", ChatID: chatID}
	key := chatID
	if key == "" {
		key = sender
	}
	return &wecomaibot.NormalizedMessage{
		ThreadKey: key, SenderID: sender, Text: text,
		Frame: &wecomaibot.WsFrame{}, Base: base,
	}
}

func completedEvent(text string) protocol.Event {
	return protocol.Event{Kind: protocol.EventTurnCompleted, Output: text}
}

func runChannel(t *testing.T, fake *fakeTransport, gateway *fakeGateway, config pluginpkg.Values) (context.CancelFunc, chan error) {
	t.Helper()
	channel := &Channel{connect: func(string, string) (transport, error) { return fake, nil }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- channel.Run(ctx, pluginpkg.ChannelDeps{
			PluginUUID: "p1", Config: config, Gateway: gateway,
			Log: func(string, ...any) {},
		})
	}()
	waitFor(t, func() bool {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		return fake.connects > 0 && fake.onMsg != nil
	}, "the channel never connected")
	return cancel, done
}

func waitFor(t *testing.T, condition func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(message)
}

func credentials() pluginpkg.Values {
	return pluginpkg.Values{"bot_id": "bot-1", "bot_secret": "s3cr3t"}
}

func TestRunRequiresCredentials(t *testing.T) {
	channel := &Channel{connect: func(string, string) (transport, error) {
		t.Fatal("the transport was dialled without credentials")
		return nil, nil
	}}
	err := channel.Run(context.Background(), pluginpkg.ChannelDeps{
		Config: pluginpkg.Values{}, Log: func(string, ...any) {},
	})
	if err == nil || !strings.Contains(err.Error(), "bot_id") {
		t.Fatalf("missing credentials were accepted: %v", err)
	}
}

func TestInboundMessageBecomesATurnAndIsAnswered(t *testing.T) {
	transport := &fakeTransport{}
	gateway := &fakeGateway{events: []protocol.Event{
		{Kind: protocol.EventAssistantDelta, Delta: strings.Repeat("a", 50)},
		completedEvent("final answer"),
	}}
	cancel, done := runChannel(t, transport, gateway, credentials())
	defer func() { cancel(); <-done }()

	transport.deliver(textMessage("group-1", "user-7", "  what happened?  "))
	waitFor(t, func() bool { return transport.finalReply() != "" }, "no reply was sent")

	if gateway.count() != 1 {
		t.Fatalf("submissions = %d", gateway.count())
	}
	submitted := gateway.last()
	if submitted.ChannelID != ChannelID || submitted.ExternalKey != "group-1" || submitted.SenderID != "user-7" {
		t.Fatalf("inbound identity = %+v", submitted)
	}
	if submitted.Text != "what happened?" {
		t.Fatalf("text was not trimmed: %q", submitted.Text)
	}
	if transport.finalReply() != "final answer" {
		t.Fatalf("final reply = %q", transport.finalReply())
	}

	// The long delta must have been pushed before the final frame, and only
	// the last frame closes the stream.
	replies := transport.sent()
	if len(replies) < 2 || replies[0].finish {
		t.Fatalf("streaming did not happen before the final reply: %+v", replies)
	}
	for _, reply := range replies[:len(replies)-1] {
		if reply.finish {
			t.Fatalf("the stream was closed early: %+v", replies)
		}
	}
	// Every frame must carry the whole answer so far: a WeCom stream frame
	// replaces the bubble, so sending only the new chunk shows fragments.
	for i := 1; i < len(replies)-1; i++ {
		if !strings.HasPrefix(replies[i].content, replies[i-1].content) {
			t.Fatalf("frame %d is not cumulative: %q does not extend %q", i, replies[i].content, replies[i-1].content)
		}
	}
}

// An unauthorized conversation must be told to ask for approval, not left
// waiting and not answered.
func TestUnauthorizedConversationIsToldToAskForApproval(t *testing.T) {
	transport := &fakeTransport{}
	gateway := &fakeGateway{err: pluginpkg.ErrBindingNotAllowed}
	cancel, done := runChannel(t, transport, gateway, credentials())
	defer func() { cancel(); <-done }()

	transport.deliver(textMessage("group-1", "user-7", "hello"))
	waitFor(t, func() bool { return transport.finalReply() != "" }, "no reply was sent")
	if !strings.Contains(transport.finalReply(), "授权") {
		t.Fatalf("reply = %q", transport.finalReply())
	}
}

// An internal failure must not be echoed to a third party: the error can name
// paths, models or configuration.
func TestInternalErrorsAreNotLeakedToTheSender(t *testing.T) {
	transport := &fakeTransport{}
	gateway := &fakeGateway{err: fmt.Errorf("provider 3 rejected key sk-live-abcdef at /Users/someone/.aiclaw")}
	cancel, done := runChannel(t, transport, gateway, credentials())
	defer func() { cancel(); <-done }()

	transport.deliver(textMessage("group-1", "user-7", "hello"))
	waitFor(t, func() bool { return transport.finalReply() != "" }, "no reply was sent")
	reply := transport.finalReply()
	for _, leaked := range []string{"sk-live", "/Users/", "provider 3"} {
		if strings.Contains(reply, leaked) {
			t.Fatalf("internal detail %q leaked to the sender: %q", leaked, reply)
		}
	}
}

// A failed turn must not be reported as an empty success.
func TestFailedTurnIsReportedAsFailure(t *testing.T) {
	transport := &fakeTransport{}
	gateway := &fakeGateway{events: []protocol.Event{
		{Kind: protocol.EventTurnFailed, Error: "model unavailable"},
	}}
	cancel, done := runChannel(t, transport, gateway, credentials())
	defer func() { cancel(); <-done }()

	transport.deliver(textMessage("group-1", "user-7", "hello"))
	waitFor(t, func() bool { return transport.finalReply() != "" }, "no reply was sent")
	if strings.Contains(transport.finalReply(), "model unavailable") {
		t.Fatalf("the internal failure was echoed: %q", transport.finalReply())
	}
	if transport.finalReply() == "" {
		t.Fatal("a failed turn produced no reply at all")
	}
}

// Messages arrive on the connection's read loop; running a turn there would
// stall the socket.
func TestTurnsDoNotBlockTheReadLoop(t *testing.T) {
	transport := &fakeTransport{}
	gateway := &fakeGateway{delay: 200 * time.Millisecond, events: []protocol.Event{completedEvent("ok")}}
	cancel, done := runChannel(t, transport, gateway, credentials())
	defer func() { cancel(); <-done }()

	started := time.Now()
	transport.deliver(textMessage("group-1", "user-7", "hello"))
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("delivering a message blocked for %v", elapsed)
	}
	waitFor(t, func() bool { return transport.finalReply() != "" }, "no reply was sent")
}

// A transport that gives up must end Run so the host can restart it with
// freshly loaded configuration.
func TestTransportFailureEndsTheRun(t *testing.T) {
	transport := &fakeTransport{}
	gateway := &fakeGateway{}
	cancel, done := runChannel(t, transport, gateway, credentials())
	defer cancel()

	transport.fail(fmt.Errorf("max reconnect attempts exceeded"))
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "max reconnect") {
			t.Fatalf("run ended with %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("a transport failure did not end the run")
	}
}

func TestEmptyMessagesAreIgnored(t *testing.T) {
	transport := &fakeTransport{}
	gateway := &fakeGateway{events: []protocol.Event{completedEvent("ok")}}
	cancel, done := runChannel(t, transport, gateway, credentials())
	defer func() { cancel(); <-done }()

	transport.deliver(textMessage("group-1", "user-7", "   "))
	time.Sleep(50 * time.Millisecond)
	if gateway.count() != 0 {
		t.Fatalf("an empty message started a turn: %+v", gateway.submitted)
	}
}
