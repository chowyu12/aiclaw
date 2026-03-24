package channels

import (
	"context"
	"net/http"
	"net/url"

	"github.com/chowyu12/aiclaw/internal/model"
)

// Inbound 归一化后的入站消息（一次 Webhook 可能为 nil，例如仅 URL 校验）。
type Inbound struct {
	ThreadKey string
	// ThreadKeyAliases 与 ThreadKey 指向同一线程的其它查找键（如企微先只有 user、后带上 chatid），用于合并为同一会话。
	ThreadKeyAliases []string
	SenderID         string
	Text             string
	RawMeta          map[string]any
	// ReplyWith 若设置，Bridge 优先用它回包（如企微智能机器人 WebSocket），不再调用 WebhookAdapter.Reply。
	ReplyWith func(ctx context.Context, text string) error
}

// WebhookHTTP 写回给第三方平台的 HTTP 响应。
type WebhookHTTP struct {
	Status      int
	ContentType string
	Headers     map[string]string
	Body        []byte
}

// WriteTo 将结果写入 HTTP 响应。
func (w WebhookHTTP) WriteTo(rw http.ResponseWriter) {
	if w.Status == 0 {
		w.Status = http.StatusOK
	}
	if w.ContentType != "" {
		rw.Header().Set("Content-Type", w.ContentType)
	}
	for k, v := range w.Headers {
		rw.Header().Set(k, v)
	}
	rw.WriteHeader(w.Status)
	if len(w.Body) > 0 {
		_, _ = rw.Write(w.Body)
	}
}

// WebhookDriver 单平台 Webhook 行为（GET 校验 / POST 解析 / 异步回复）。
type WebhookDriver interface {
	HandleGET(ch ChannelConfig, q url.Values) WebhookHTTP
	HandlePOST(ch ChannelConfig, body []byte, contentType string, header http.Header) (WebhookHTTP, *Inbound)
	Reply(ctx context.Context, ch ChannelConfig, in *Inbound, text string) error
}

// ChannelDriver 运行时渠道接口：用于管理后台长连接等（如企微 WebSocket）。
// Refresh 入参为该驱动所属 ChannelType 的全部渠道（含未启用），驱动自行决定启停。
type ChannelDriver interface {
	Refresh(ctx context.Context, channels []*model.Channel, bridge *Bridge)
	Stop()
}

// ChannelConfig 从 model.Channel 抽取的只读视图，避免 channels 包依赖过多 GORM 细节。
type ChannelConfig struct {
	ID          int64
	UUID        string
	ChannelType string
	ConfigJSON  []byte
}
