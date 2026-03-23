package channels

import (
	"context"
	"net/http"
	"net/url"
)

type noopAdapter struct{}

func (noopAdapter) HandleGET(_ ChannelConfig, _ url.Values) WebhookHTTP {
	return WebhookHTTP{Status: 200, ContentType: "application/json", Body: []byte(`{"ok":true}`)}
}

func (noopAdapter) HandlePOST(_ ChannelConfig, body []byte, _ string, _ http.Header) (WebhookHTTP, *Inbound) {
	_ = body
	return WebhookHTTP{Status: 200, ContentType: "application/json", Body: []byte(`{"ok":true}`)}, nil
}

func (noopAdapter) Reply(_ context.Context, _ ChannelConfig, _ *Inbound, _ string) error {
	return nil
}
