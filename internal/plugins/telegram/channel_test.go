package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chowyu12/aiclaw/internal/plugin"
	"github.com/chowyu12/aiclaw/internal/protocol"
)

type tokenConfig struct{ plugin.ConfigReader }

func (tokenConfig) String(key string) string {
	if key == "bot_token" {
		return "123:fixture"
	}
	return ""
}

type fixtureGateway func(context.Context, string, plugin.Inbound, func(protocol.Event) error) error

func (f fixtureGateway) Submit(ctx context.Context, id string, msg plugin.Inbound, observe func(protocol.Event) error) error {
	return f(ctx, id, msg, observe)
}

func TestLongPollingAndReply(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	replied := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bot123:fixture/getMe":
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":12,"username":"sample_bot","is_bot":true}}`))
		case "/bot123:fixture/getUpdates":
			var input struct{ Offset int64 }
			_ = json.NewDecoder(r.Body).Decode(&input)
			if input.Offset == 0 {
				_, _ = w.Write([]byte(`{"ok":true,"result":[{"update_id":55,"message":{"message_id":4,"message_thread_id":19,"text":"😀 @sample_bot hi","from":{"id":42},"chat":{"id":-100,"type":"supergroup"},"entities":[{"type":"mention","offset":3,"length":11}]}}]}`))
				return
			}
			if input.Offset != 56 {
				t.Errorf("offset = %d", input.Offset)
			}
			select {
			case <-replied:
			case <-ctx.Done():
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
			cancel()
		case "/bot123:fixture/sendMessage":
			var sent struct {
				Chat   int64 `json:"chat_id"`
				Thread int64 `json:"message_thread_id"`
				Text   string
			}
			_ = json.NewDecoder(r.Body).Decode(&sent)
			if sent.Chat != -100 || sent.Thread != 19 || sent.Text != "fixture answer" {
				t.Errorf("reply = %+v", sent)
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
			replied <- struct{}{}
		default:
			t.Errorf("unexpected endpoint %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	c := Channel{endpoint: server.URL, client: server.Client()}
	called := false
	gateway := fixtureGateway(func(_ context.Context, id string, msg plugin.Inbound, observe func(protocol.Event) error) error {
		called = true
		if id != "plugin" || msg.ChannelID != "telegram" || msg.ConnectionID != "conn" || msg.ExternalKey != "-100/19" || msg.SenderID != "42" || msg.Text != "😀  hi" {
			t.Errorf("received %+v", msg)
		}
		return observe(protocol.Event{Kind: protocol.EventTurnCompleted, Output: "fixture answer"})
	})
	_ = c.Run(ctx, plugin.ChannelDeps{PluginUUID: "plugin", ConnectionID: "conn", Config: tokenConfig{}, Gateway: gateway})
	if !called || ctx.Err() != context.Canceled {
		t.Fatal("polling did not complete the reply cycle")
	}
}

func TestIgnoresBotsAndUntargetedGroups(t *testing.T) {
	for _, payload := range []string{
		`{"message_id":1,"text":"hi","from":{"id":2,"is_bot":true},"chat":{"id":5,"type":"private"}}`,
		`{"message_id":1,"text":"@some_other_bot hi","from":{"id":2},"chat":{"id":5,"type":"group"}}`,
	} {
		var msg message
		if err := json.Unmarshal([]byte(payload), &msg); err != nil {
			t.Fatal(err)
		}
		if _, ok := normalize(&msg, user{ID: 10, Username: "sample_bot"}); ok {
			t.Errorf("unexpected inbound: %s", payload)
		}
	}
}
