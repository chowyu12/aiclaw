package wecomaibot

import "strings"

// NormalizedMessage 归一化后的入站消息，统一所有消息类型的公共字段。
type NormalizedMessage struct {
	// ThreadKey 会话键（群聊 ChatID 优先，否则用 SenderID）
	ThreadKey string
	// SenderID 发送人 UserID
	SenderID string
	// Text 用户写的文字（语音消息已由服务端转写）。不含媒体链接——那些链接是加密的，
	// 对模型没有用，媒体单独放在 Images / Files 里，由通道下载解密。
	Text string
	// Images 图片（含引用消息里的）。
	Images []MediaRef
	// Files 文件附件（含引用消息里的）。
	Files []MediaRef
	// Note 是给模型的一句说明，说的是收到了但没法转交的东西（比如视频）。
	Note string
	// Frame 原始 WebSocket 帧，用于发送回复
	Frame *WsFrame
	// Base 原始消息公共字段（MsgID、MsgType、ChatID、From 等）
	Base *BaseMessage
}

// withQuote 把引用消息的内容并进来。
func (m *NormalizedMessage) withQuote(quote *QuoteContent) *NormalizedMessage {
	text, images, files := quoteParts(quote)
	if text != "" {
		if m.Text != "" {
			m.Text += "\n"
		}
		m.Text += "（引用）" + text
	}
	m.Images = append(m.Images, images...)
	m.Files = append(m.Files, files...)
	return m
}

// OnNormalizedMessage 注册统一入站消息处理器，内部将文本、图片、语音、文件、视频、混排
// 等全部消息类型统一归一化后交由 handler 处理。
// 注意：此方法会覆盖之前单独注册的各类型回调。
func (c *WSClient) OnNormalizedMessage(handler func(*NormalizedMessage)) {
	base := func(frame *WsFrame, msg *BaseMessage) *NormalizedMessage {
		return &NormalizedMessage{
			ThreadKey: nmThreadKey(msg),
			SenderID:  msg.From.UserID,
			Frame:     frame,
			Base:      msg,
		}
	}

	c.OnMessageText(func(frame *WsFrame) {
		var msg TextMessage
		if err := ParseMessageBody(frame, &msg); err != nil {
			return
		}
		out := base(frame, &msg.BaseMessage)
		out.Text = strings.TrimSpace(msg.Text.Content)
		handler(out.withQuote(msg.Quote))
	})

	c.OnMessageMixed(func(frame *WsFrame) {
		var msg MixedMessage
		if err := ParseMessageBody(frame, &msg); err != nil {
			return
		}
		out := base(frame, &msg.BaseMessage)
		out.Text = MixedToUserVisibleText(&msg)
		out.Images = CollectImagesFromMixed(&msg)
		handler(out.withQuote(msg.Quote))
	})

	c.OnMessageImage(func(frame *WsFrame) {
		var msg ImageMessage
		if err := ParseMessageBody(frame, &msg); err != nil {
			return
		}
		out := base(frame, &msg.BaseMessage)
		if ref, ok := imageRef(&msg.Image); ok {
			out.Images = []MediaRef{ref}
		}
		handler(out)
	})

	c.OnMessageVoice(func(frame *WsFrame) {
		var msg VoiceMessage
		if err := ParseMessageBody(frame, &msg); err != nil {
			return
		}
		out := base(frame, &msg.BaseMessage)
		out.Text = strings.TrimSpace(msg.Voice.Content)
		handler(out)
	})

	c.OnMessageFile(func(frame *WsFrame) {
		var msg FileMessage
		if err := ParseMessageBody(frame, &msg); err != nil {
			return
		}
		out := base(frame, &msg.BaseMessage)
		if u := strings.TrimSpace(msg.File.URL); u != "" {
			out.Files = []MediaRef{{URL: u, AESKey: strings.TrimSpace(msg.File.AESKey)}}
		}
		handler(out)
	})

	c.OnMessageVideo(func(frame *WsFrame) {
		var msg VideoMessage
		if err := ParseMessageBody(frame, &msg); err != nil {
			return
		}
		out := base(frame, &msg.BaseMessage)
		// 视频不下载：模型看不了视频，而一段视频动辄几十 MB。说一声，别让它以为什么都没发。
		out.Note = "[用户发来一段视频，暂不支持查看]"
		handler(out)
	})
}

func nmThreadKey(base *BaseMessage) string {
	if c := strings.TrimSpace(base.ChatID); c != "" {
		return c
	}
	return strings.TrimSpace(base.From.UserID)
}
