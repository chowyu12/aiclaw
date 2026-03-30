package wecomaibot

import "strings"

// NormalizedMessage 归一化后的入站消息，统一所有消息类型的公共字段。
type NormalizedMessage struct {
	// ThreadKey 会话键（群聊 ChatID 优先，否则用 SenderID）
	ThreadKey string
	// SenderID 发送人 UserID
	SenderID string
	// Text 文本正文（语音消息已由服务端转写）
	Text string
	// ImageURLs 图片 URL 列表（可能是加密 CDN 参数或可直接访问的 URL）
	ImageURLs []string
	// FileURL 文件附件 URL（如有）
	FileURL string
	// VideoURL 视频 URL（如有）
	VideoURL string
	// Frame 原始 WebSocket 帧，用于发送回复
	Frame *WsFrame
	// Base 原始消息公共字段（MsgID、MsgType、ChatID、From 等）
	Base *BaseMessage
}

// OnNormalizedMessage 注册统一入站消息处理器，内部将文本、图片、语音、文件、视频、混排
// 等全部消息类型统一归一化后交由 handler 处理。
// 注意：此方法会覆盖之前单独注册的各类型回调。
func (c *WSClient) OnNormalizedMessage(handler func(*NormalizedMessage)) {
	c.OnMessageText(func(frame *WsFrame) {
		var msg TextMessage
		if err := ParseMessageBody(frame, &msg); err != nil {
			return
		}
		handler(&NormalizedMessage{
			ThreadKey: nmThreadKey(&msg.BaseMessage),
			SenderID:  msg.From.UserID,
			Text:      strings.TrimSpace(msg.Text.Content),
			Frame:     frame,
			Base:      &msg.BaseMessage,
		})
	})

	c.OnMessageMixed(func(frame *WsFrame) {
		var msg MixedMessage
		if err := ParseMessageBody(frame, &msg); err != nil {
			return
		}
		handler(&NormalizedMessage{
			ThreadKey: nmThreadKey(&msg.BaseMessage),
			SenderID:  msg.From.UserID,
			Text:      MixedToUserVisibleText(&msg),
			ImageURLs: CollectImageURLsFromMixed(&msg),
			Frame:     frame,
			Base:      &msg.BaseMessage,
		})
	})

	c.OnMessageImage(func(frame *WsFrame) {
		var msg ImageMessage
		if err := ParseMessageBody(frame, &msg); err != nil {
			return
		}
		var urls []string
		if u := strings.TrimSpace(msg.Image.URL); u != "" {
			urls = []string{u}
		}
		handler(&NormalizedMessage{
			ThreadKey: nmThreadKey(&msg.BaseMessage),
			SenderID:  msg.From.UserID,
			Text:      ImageToUserVisibleText(&msg),
			ImageURLs: urls,
			Frame:     frame,
			Base:      &msg.BaseMessage,
		})
	})

	c.OnMessageVoice(func(frame *WsFrame) {
		var msg VoiceMessage
		if err := ParseMessageBody(frame, &msg); err != nil {
			return
		}
		handler(&NormalizedMessage{
			ThreadKey: nmThreadKey(&msg.BaseMessage),
			SenderID:  msg.From.UserID,
			Text:      strings.TrimSpace(msg.Voice.Content),
			Frame:     frame,
			Base:      &msg.BaseMessage,
		})
	})

	c.OnMessageFile(func(frame *WsFrame) {
		var msg FileMessage
		if err := ParseMessageBody(frame, &msg); err != nil {
			return
		}
		fileURL := strings.TrimSpace(msg.File.URL)
		handler(&NormalizedMessage{
			ThreadKey: nmThreadKey(&msg.BaseMessage),
			SenderID:  msg.From.UserID,
			Text:      "[文件] " + fileURL,
			FileURL:   fileURL,
			Frame:     frame,
			Base:      &msg.BaseMessage,
		})
	})

	c.OnMessageVideo(func(frame *WsFrame) {
		var msg VideoMessage
		if err := ParseMessageBody(frame, &msg); err != nil {
			return
		}
		videoURL := strings.TrimSpace(msg.Video.URL)
		handler(&NormalizedMessage{
			ThreadKey: nmThreadKey(&msg.BaseMessage),
			SenderID:  msg.From.UserID,
			Text:      "[视频] " + videoURL,
			VideoURL:  videoURL,
			Frame:     frame,
			Base:      &msg.BaseMessage,
		})
	})
}

func nmThreadKey(base *BaseMessage) string {
	if c := strings.TrimSpace(base.ChatID); c != "" {
		return c
	}
	return strings.TrimSpace(base.From.UserID)
}
