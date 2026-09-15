package wechat

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	pluginpkg "github.com/chowyu12/aiclaw/internal/plugin"
	"github.com/chowyu12/aiclaw/internal/protocol"
	"github.com/chowyu12/aiclaw/pkg/wechatlink"
)

// fakeSession stands in for the iLink relay. Tests never reach the network:
// this connector drives someone's personal WeChat account.
type fakeSession struct {
	mu      sync.Mutex
	sent    []string
	typings int
	handler func(wechatlink.Message)
	ready   chan struct{}
}

func newFakeSession() *fakeSession {
	return &fakeSession{ready: make(chan struct{})}
}

func (s *fakeSession) Listen(ctx context.Context, handler func(wechatlink.Message)) {
	s.mu.Lock()
	s.handler = handler
	s.mu.Unlock()
	close(s.ready)
	<-ctx.Done()
}

func (s *fakeSession) SendText(_ context.Context, _, _, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, text)
	return nil
}

func (s *fakeSession) SendTyping(context.Context, string, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.typings++
	return nil
}

func (s *fakeSession) deliver(message wechatlink.Message) {
	<-s.ready
	s.mu.Lock()
	handler := s.handler
	s.mu.Unlock()
	handler(message)
}

func (s *fakeSession) replies() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.sent...)
}

func (s *fakeSession) typingCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.typings
}

type fakeGateway struct {
	mu        sync.Mutex
	submitted []pluginpkg.Inbound
	err       error
	events    []protocol.Event
}

func (g *fakeGateway) Submit(_ context.Context, _ string, message pluginpkg.Inbound, observe func(protocol.Event) error) error {
	g.mu.Lock()
	g.submitted = append(g.submitted, message)
	err, events := g.err, g.events
	g.mu.Unlock()
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

func signedIn() pluginpkg.Values {
	return pluginpkg.Values{ConfigBotToken: "token-1", ConfigBotID: "bot-1"}
}

func runChannel(t *testing.T, remote *fakeSession, gateway *fakeGateway, config pluginpkg.Values) (context.CancelFunc, chan error) {
	t.Helper()
	channel := &Channel{newSession: func(*wechatlink.Credentials) session { return remote }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- channel.Run(ctx, pluginpkg.ChannelDeps{
			PluginUUID: "p1", Config: config, Gateway: gateway,
			Log: func(string, ...any) {},
		})
	}()
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

// Signing in is what produces the credentials, so a channel without them must
// not start polling.
func TestRunRequiresSignIn(t *testing.T) {
	channel := &Channel{newSession: func(*wechatlink.Credentials) session {
		t.Fatal("the relay was contacted without credentials")
		return nil
	}}
	err := channel.Run(context.Background(), pluginpkg.ChannelDeps{
		Config: pluginpkg.Values{}, Log: func(string, ...any) {},
	})
	if err == nil || !strings.Contains(err.Error(), "signed in") {
		t.Fatalf("an unauthenticated connector started: %v", err)
	}
}

func TestInboundMessageBecomesATurnAndIsAnswered(t *testing.T) {
	remote := newFakeSession()
	gateway := &fakeGateway{events: []protocol.Event{
		{Kind: protocol.EventTurnCompleted, Output: "final answer"},
	}}
	cancel, done := runChannel(t, remote, gateway, signedIn())
	defer func() { cancel(); <-done }()

	remote.deliver(wechatlink.Message{FromUserID: "wxid_7", Text: " hello ", ContextToken: "ctx-1"})
	waitFor(t, func() bool { return len(remote.replies()) > 0 }, "no reply was sent")

	gateway.mu.Lock()
	submitted := gateway.submitted[0]
	gateway.mu.Unlock()
	if submitted.ChannelID != ChannelID || submitted.ExternalKey != "wxid_7" || submitted.Text != "hello" {
		t.Fatalf("inbound = %+v", submitted)
	}
	if remote.replies()[0] != "final answer" {
		t.Fatalf("reply = %q", remote.replies()[0])
	}
	// The sender should see the agent working rather than silence.
	if remote.typingCount() == 0 {
		t.Fatal("no typing indicator was sent")
	}
}

func TestUnauthorizedConversationIsToldToAskForApproval(t *testing.T) {
	remote := newFakeSession()
	gateway := &fakeGateway{err: pluginpkg.ErrBindingNotAllowed}
	cancel, done := runChannel(t, remote, gateway, signedIn())
	defer func() { cancel(); <-done }()

	remote.deliver(wechatlink.Message{FromUserID: "wxid_7", Text: "hello"})
	waitFor(t, func() bool { return len(remote.replies()) > 0 }, "no reply was sent")
	if !strings.Contains(remote.replies()[0], "授权") {
		t.Fatalf("reply = %q", remote.replies()[0])
	}
}

func TestInternalErrorsAreNotLeakedToTheSender(t *testing.T) {
	remote := newFakeSession()
	gateway := &fakeGateway{err: fmt.Errorf("provider 3 rejected key sk-live-abcdef at /Users/someone/.aiclaw")}
	cancel, done := runChannel(t, remote, gateway, signedIn())
	defer func() { cancel(); <-done }()

	remote.deliver(wechatlink.Message{FromUserID: "wxid_7", Text: "hello"})
	waitFor(t, func() bool { return len(remote.replies()) > 0 }, "no reply was sent")
	for _, leaked := range []string{"sk-live", "/Users/", "provider 3"} {
		if strings.Contains(remote.replies()[0], leaked) {
			t.Fatalf("internal detail %q leaked: %q", leaked, remote.replies()[0])
		}
	}
}

func TestFailedTurnStillAnswers(t *testing.T) {
	remote := newFakeSession()
	gateway := &fakeGateway{events: []protocol.Event{
		{Kind: protocol.EventTurnFailed, Error: "model unavailable"},
	}}
	cancel, done := runChannel(t, remote, gateway, signedIn())
	defer func() { cancel(); <-done }()

	remote.deliver(wechatlink.Message{FromUserID: "wxid_7", Text: "hello"})
	waitFor(t, func() bool { return len(remote.replies()) > 0 }, "no reply was sent")
	reply := remote.replies()[0]
	if reply == "" || strings.Contains(reply, "model unavailable") {
		t.Fatalf("reply = %q", reply)
	}
}

// Image-only messages are not served yet, and must be ignored rather than
// submitted as an empty turn.
func TestMessagesWithoutTextAreIgnored(t *testing.T) {
	remote := newFakeSession()
	gateway := &fakeGateway{}
	cancel, done := runChannel(t, remote, gateway, signedIn())
	defer func() { cancel(); <-done }()

	remote.deliver(wechatlink.Message{
		FromUserID: "wxid_7",
		Images:     []wechatlink.ImageSource{{URL: "https://example.invalid/a.png"}},
	})
	time.Sleep(50 * time.Millisecond)
	if gateway.count() != 0 {
		t.Fatal("an image-only message started a turn")
	}
	if len(remote.replies()) != 0 {
		t.Fatalf("an image-only message was answered: %+v", remote.replies())
	}
}

// Cancelling must end Run so the host can stop the channel.
func TestCancellationEndsTheRun(t *testing.T) {
	remote := newFakeSession()
	cancel, done := runChannel(t, remote, &fakeGateway{}, signedIn())
	<-remote.ready
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancellation reported success")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelling did not end the run")
	}
}
