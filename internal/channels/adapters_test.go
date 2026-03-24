package channels

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/chowyu12/aiclaw/internal/model"
)

func TestWeComHTTPNoInbound(t *testing.T) {
	ad := wecomAdapter{}
	_, in := ad.HandlePOST(ChannelConfig{UUID: "x"}, []byte(`{"text":{"content":"hello"}}`), "application/json", nil)
	if in != nil {
		t.Fatalf("wecom HTTP must not emit inbound; use WebSocket hub")
	}
}



func TestFeishuChallenge(t *testing.T) {
	ad := feishuAdapter{}
	body := []byte(`{"challenge":"abc","type":"url_verification"}`)
	wh, in := ad.HandlePOST(ChannelConfig{}, body, "application/json", http.Header{})
	if in != nil {
		t.Fatalf("expected no inbound")
	}
	if string(wh.Body) != `{"challenge":"abc"}` {
		t.Fatalf("body %s", wh.Body)
	}
}

func TestTelegramInbound(t *testing.T) {
	ad := telegramAdapter{}
	body := []byte(`{"message":{"message_id":1,"from":{"id":99},"chat":{"id":42,"type":"private"},"text":"yo"}}`)
	_, in := ad.HandlePOST(ChannelConfig{}, body, "application/json", nil)
	if in == nil || in.Text != "yo" || in.ThreadKey != "42" {
		t.Fatalf("in=%v", in)
	}
}

func TestWhatsAppVerifyGET(t *testing.T) {
	ad := whatsAppAdapter{}
	cfg := ChannelConfig{
		ConfigJSON: []byte(`{"verify_token":"sekret"}`),
	}
	q := url.Values{}
	q.Set("hub.mode", "subscribe")
	q.Set("hub.verify_token", "sekret")
	q.Set("hub.challenge", "12345")
	wh := ad.HandleGET(cfg, q)
	if string(wh.Body) != "12345" {
		t.Fatalf("body %s", wh.Body)
	}
}

func TestWebhookForUnknown(t *testing.T) {
	if _, ok := WebhookFor(model.ChannelType("nope")).(*noopAdapter); !ok {
		t.Fatal("expected noop")
	}
}
