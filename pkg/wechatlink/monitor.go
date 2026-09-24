package wechatlink

import (
	"context"
	"fmt"
	"strings"
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
			message := extractContent(raw.ItemList)
			if message.Text == "" && len(message.Images) == 0 && len(message.Files) == 0 {
				m.logger.Debug("跳过没有内容的消息")
				continue
			}
			message.FromUserID = raw.FromUserID
			message.ContextToken = raw.ContextToken
			m.handler(message)
		}
	}
}

// extractContent 把一条消息的各项归一成文字、图片与文件。
//
// 多段文字按顺序连起来（旧写法只留最后一段）；引用的消息里的图片与文件也收进来：
// 「引用一张图，问这是什么」是最常见的用法，丢了引用模型就只看到一句「这是什么」。
func extractContent(items []MessageItem) Message {
	var message Message
	var texts []string
	var walk func(items []MessageItem, quoted bool)
	walk = func(items []MessageItem, quoted bool) {
		for _, it := range items {
			switch it.Type {
			case ItemTypeText:
				if it.TextItem != nil && strings.TrimSpace(it.TextItem.Text) != "" {
					if quoted {
						texts = append(texts, "（引用）"+it.TextItem.Text)
					} else {
						texts = append(texts, it.TextItem.Text)
					}
				}
			case ItemTypeImage:
				if it.ImageItem == nil {
					continue
				}
				img := ImageSource{URL: it.ImageItem.URL, Media: it.ImageItem.Media, HexKey: it.ImageItem.AESKey}
				if img.URL != "" || img.Media != nil {
					message.Images = append(message.Images, img)
				}
			case ItemTypeVoice:
				if it.VoiceItem == nil {
					continue
				}
				if t := strings.TrimSpace(it.VoiceItem.Text); t != "" {
					texts = append(texts, t)
				} else {
					texts = append(texts, "[一段语音，微信没有转出文字]")
				}
			case ItemTypeFile:
				if it.FileItem != nil && it.FileItem.Media != nil {
					message.Files = append(message.Files, FileSource{Name: it.FileItem.FileName, Media: it.FileItem.Media})
				}
			case ItemTypeVideo:
				// 视频不下载：模型看不了视频，而一段视频动辄几十 MB。说一声，别让它以为什么都没发。
				texts = append(texts, "[一段视频，暂不支持查看]")
			}
			if it.RefMsg != nil && it.RefMsg.MessageItem != nil {
				walk([]MessageItem{*it.RefMsg.MessageItem}, true)
			}
		}
	}
	walk(items, false)
	message.Text = strings.Join(texts, "\n")
	return message
}
