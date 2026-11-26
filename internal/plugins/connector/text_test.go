package connector

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chowyu12/aiclaw/internal/plugin"
	"github.com/chowyu12/aiclaw/internal/protocol"
)

type textGateway func(context.Context, string, plugin.Inbound, func(protocol.Event) error) error

func (g textGateway) Submit(ctx context.Context, id string, msg plugin.Inbound, observe func(protocol.Event) error) error {
	return g(ctx, id, msg, observe)
}

func TestTextInboxUsesChannelAuthorizationAndDeduplicates(t *testing.T) {
	sent := make(chan string, 2)
	var calls int
	deps := plugin.ChannelDeps{PluginUUID: "plugin", ConnectionID: "connection", Gateway: textGateway(func(_ context.Context, id string, msg plugin.Inbound, _ func(protocol.Event) error) error {
		calls++
		if id != "plugin" || msg.ChannelID != "feishu" || msg.ConnectionID != "connection" || msg.ExternalKey != "chat" {
			t.Errorf("wrong destination: %s %+v", id, msg)
		}
		return plugin.ErrBindingNotAllowed
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	inbox := NewTextInbox(ctx, deps, "feishu")
	defer inbox.Close()
	message := plugin.Inbound{ExternalKey: "chat", Text: "hello"}
	reply := func(_ context.Context, text string) error { sent <- text; return nil }
	if !inbox.Submit("event", message, reply) || !inbox.Submit("event", message, reply) {
		t.Fatal("initial event or its duplicate was rejected")
	}
	select {
	case text := <-sent:
		if !strings.Contains(text, "连接设置") {
			t.Errorf("expected an authorization notice, got %q", text)
		}
	case <-ctx.Done():
		t.Fatal("did not get a reply")
	}
	inbox.Close()
	if calls != 1 || len(sent) != 0 {
		t.Fatalf("duplicate message was processed: %d calls, %d pending replies", calls, len(sent))
	}
}
