package wechatlink

import (
	"context"
	"fmt"
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

// maxConsecutiveFailures 是连着失败多少次就放弃。
//
// **为什么要有上限。** 原先这个循环永远重试：bot_token 失效、账号在别处下线时
// getupdates 一直回 ret!=0，循环一直退避重试，Run 永不返回，于是上层的通道主管
// 既不会重启它、也不会把它标成失败——界面上显示「运行中」，而消息一条收不到。
// 退避封顶 30 秒，八次约两分半：网络抖动几次就恢复了的场景不会碰到它，而凭据
// 真的失效时两分半后就会浮上来。
const maxConsecutiveFailures = 8

// Run 阻塞执行长轮询循环，直至 ctx 取消或连续失败次数超过上限。
//
// 返回值区分两种结束：ctx 取消返回 nil（是调用方要停），持续失败返回最后那个
// 错误（要让上层看见并处置）。
func (m *Monitor) Run(ctx context.Context) error {
	var buf string
	backoff := time.Second
	const maxBackoff = 30 * time.Second
	firstHandshake := true
	failures := 0

	m.logger.Info("长轮询监听启动")
	defer m.logger.Info("长轮询监听已退出")

	for {
		if ctx.Err() != nil {
			return nil
		}
		msgs, newBuf, timeoutMs, err := m.client.GetUpdates(ctx, buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			failures++
			if failures >= maxConsecutiveFailures {
				m.logger.Error("getUpdates 连续失败 %d 次，放弃：%v", failures, err)
				return fmt.Errorf("长轮询连续失败 %d 次：%w", failures, err)
			}
			m.logger.Error("getUpdates 失败（第 %d 次）: %v, %v 后重试", failures, err, backoff)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, maxBackoff)
			continue
		}
		if failures > 0 {
			m.logger.Info("长轮询已恢复（失败 %d 次之后）", failures)
		}
		failures = 0
		backoff = time.Second
		if newBuf != "" {
			buf = newBuf
		}
		if firstHandshake {
			firstHandshake = false
			m.logger.Info("长轮询握手成功 (timeout_ms=%d, msgs=%d)", timeoutMs, len(msgs))
		}
		if len(msgs) > 0 {
			m.logger.Info("收到 %d 条消息", len(msgs))
		}
		for _, raw := range msgs {
			if raw.MessageType != MsgTypeUser {
				m.logger.Debug("跳过非用户消息 type=%d", raw.MessageType)
				continue
			}
			text, images := extractContent(raw.ItemList)
			if text == "" && len(images) == 0 {
				m.logger.Debug("跳过没有文字与图片的消息")
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
