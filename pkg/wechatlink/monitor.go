package wechatlink

import (
	"context"
	"time"
)

// MessageHandler 收到消息时回调。
type MessageHandler func(msg Message)

// Monitor 封装 iLink 长轮询消息循环。
type Monitor struct {
	client  *Client
	handler MessageHandler
	logger  Logger
}

// NewMonitor 创建消息监听器。
func NewMonitor(client *Client, handler MessageHandler, opts ...ClientOption) *Monitor {
	m := &Monitor{
		client:  client,
		handler: handler,
		logger:  nopLogger{},
	}
	for _, o := range opts {
		stub := &Client{logger: m.logger}
		o(stub)
		m.logger = stub.logger
	}
	return m
}

// Run 阻塞执行长轮询循环，直至 ctx 取消。
func (m *Monitor) Run(ctx context.Context) {
	var buf string
	backoff := time.Second
	const maxBackoff = 30 * time.Second
	firstHandshake := true

	m.logger.Info("长轮询监听启动")
	defer m.logger.Info("长轮询监听已退出")

	for {
		if ctx.Err() != nil {
			return
		}
		msgs, newBuf, timeoutMs, err := m.client.GetUpdates(ctx, buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			m.logger.Error("getUpdates 失败: %v, %v 后重试", err, backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, maxBackoff)
			continue
		}
		backoff = time.Second
		if newBuf != "" {
			buf = newBuf
		}
		if firstHandshake {
			firstHandshake = false
			m.logger.Info("长轮询握手成功 (timeout_ms=%d, msgs=%d)", timeoutMs, len(msgs))
		}
		for _, raw := range msgs {
			if raw.MessageType != MsgTypeUser {
				continue
			}
			text, images := extractContent(raw.ItemList)
			if text == "" && len(images) == 0 {
				continue
			}
			m.handler(Message{
				FromUserID:   raw.FromUserID,
				Text:         text,
				Images:       images,
				ContextToken: raw.ContextToken,
			})
		}
	}
}

func extractContent(items []MessageItem) (text string, images []ImageSource) {
	for _, it := range items {
		switch it.Type {
		case ItemTypeText:
			if it.TextItem != nil && it.TextItem.Text != "" {
				text = it.TextItem.Text
			}
		case ItemTypeImage:
			if it.ImageItem == nil {
				continue
			}
			img := ImageSource{URL: it.ImageItem.URL}
			if it.ImageItem.Media != nil {
				img.Media = it.ImageItem.Media
			}
			if img.URL != "" || img.Media != nil {
				images = append(images, img)
			}
		}
	}
	return
}
