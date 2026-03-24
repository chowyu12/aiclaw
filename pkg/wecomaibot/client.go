package wecomaibot

import (
	"encoding/json"
	"fmt"
	"sync"
)

type messageHandlerFunc func(frame *WsFrame)
type eventHandlerFunc func(frame *WsFrame)
type connectionHandlerFunc func()
type reconnectHandlerFunc func(attempt int)
type errorHandlerFunc func(err error)
type disconnectHandlerFunc func(reason string)

// WSClient 企微智能机器人 WebSocket 客户端。
type WSClient struct {
	opts   wsClientResolvedOpts
	logger Logger

	wsManager *WsConnectionManager
	handler   *MessageHandler

	onMessage       messageHandlerFunc
	onMessageText   messageHandlerFunc
	onMessageImage  messageHandlerFunc
	onMessageMixed  messageHandlerFunc
	onMessageVoice  messageHandlerFunc
	onMessageFile   messageHandlerFunc
	onMessageVideo  messageHandlerFunc
	onMessageStream messageHandlerFunc

	onEvent                  eventHandlerFunc
	onEventEnterChat         eventHandlerFunc
	onEventTemplateCardEvent eventHandlerFunc
	onEventFeedbackEvent     eventHandlerFunc

	userOnAuth     connectionHandlerFunc
	onDisconnected disconnectHandlerFunc
	onReconnecting reconnectHandlerFunc
	onError        errorHandlerFunc

	started bool
	mu      sync.RWMutex
}

type wsClientResolvedOpts struct {
	reconnectInterval    int
	maxReconnectAttempts int
	heartbeatInterval    int
	wsURL                string
	botID                string
	secret               string
}

// NewWSClient 创建客户端（默认 wss://openws.work.weixin.qq.com）。
func NewWSClient(options WSClientOptions) *WSClient {
	opts := wsClientResolvedOpts{
		reconnectInterval:    1000,
		maxReconnectAttempts: 10,
		heartbeatInterval:    30000,
		wsURL:                defaultWSURL,
		botID:                options.BotID,
		secret:               options.Secret,
	}
	if options.ReconnectInterval > 0 {
		opts.reconnectInterval = options.ReconnectInterval
	}
	if options.MaxReconnectAttempts != 0 {
		opts.maxReconnectAttempts = options.MaxReconnectAttempts
	}
	if options.HeartbeatInterval > 0 {
		opts.heartbeatInterval = options.HeartbeatInterval
	}
	if options.WSURL != "" {
		opts.wsURL = options.WSURL
	}
	logger := options.Logger
	if logger == nil {
		logger = nopLogger{}
	}
	c := &WSClient{
		opts:    opts,
		logger:  logger,
		handler: NewMessageHandler(logger),
	}
	c.wsManager = NewWsConnectionManager(logger, opts.heartbeatInterval, opts.reconnectInterval, opts.maxReconnectAttempts, opts.wsURL)
	c.wsManager.SetCredentials(opts.botID, opts.secret)
	c.wsManager.OnMessage = func(frame *WsFrame) {
		c.handler.HandleFrame(frame, c)
	}
	c.wsManager.OnAuthenticated = func() {
		c.logger.Info("Authenticated")
		if c.userOnAuth != nil {
			c.userOnAuth()
		}
	}
	c.wsManager.OnDisconnected = func(reason string) {
		if c.onDisconnected != nil {
			c.onDisconnected(reason)
		}
	}
	c.wsManager.OnReconnecting = func(attempt int) {
		if c.onReconnecting != nil {
			c.onReconnecting(attempt)
		}
	}
	c.wsManager.OnError = func(err error) {
		if c.onError != nil {
			c.onError(err)
		}
	}
	return c
}

// Connect 建立连接。
func (c *WSClient) Connect() *WSClient {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started {
		c.logger.Warn("Client already connected")
		return c
	}
	c.logger.Info("Establishing WebSocket connection...")
	c.started = true
	c.wsManager.Connect()
	return c
}

// Disconnect 断开连接。
func (c *WSClient) Disconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started {
		c.logger.Warn("Client not connected")
		return
	}
	c.logger.Info("Disconnecting...")
	c.started = false
	c.wsManager.Disconnect()
	c.logger.Info("Disconnected")
}

// IsConnected 是否已连接。
func (c *WSClient) IsConnected() bool {
	return c.wsManager.IsConnected()
}

// IsAuthenticated 是否已完成开放平台 SUBSCRIBE 认证（与 IsConnected 不同：先 TCP 再认证）。
func (c *WSClient) IsAuthenticated() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.started {
		return false
	}
	return c.wsManager.IsAuthenticated()
}

// OnMessageText 文本消息。
func (c *WSClient) OnMessageText(handler messageHandlerFunc) { c.onMessageText = handler }

// OnMessageImage 图片消息。
func (c *WSClient) OnMessageImage(handler messageHandlerFunc) { c.onMessageImage = handler }

// OnMessageMixed 图文混排。
func (c *WSClient) OnMessageMixed(handler messageHandlerFunc) { c.onMessageMixed = handler }

// OnMessageVoice 语音。
func (c *WSClient) OnMessageVoice(handler messageHandlerFunc) { c.onMessageVoice = handler }

// OnMessageFile 文件。
func (c *WSClient) OnMessageFile(handler messageHandlerFunc) { c.onMessageFile = handler }

// OnMessageVideo 视频。
func (c *WSClient) OnMessageVideo(handler messageHandlerFunc) { c.onMessageVideo = handler }

// OnMessageStream 流式消息刷新回调。
func (c *WSClient) OnMessageStream(handler messageHandlerFunc) { c.onMessageStream = handler }

// OnMessage 任意消息类型（先于细分回调）。
func (c *WSClient) OnMessage(handler messageHandlerFunc) { c.onMessage = handler }

// OnEvent 任意事件。
func (c *WSClient) OnEvent(handler eventHandlerFunc) { c.onEvent = handler }

// OnEventEnterChat 进入会话。
func (c *WSClient) OnEventEnterChat(handler eventHandlerFunc) { c.onEventEnterChat = handler }

// OnEventTemplateCardEvent 模板卡片事件。
func (c *WSClient) OnEventTemplateCardEvent(handler eventHandlerFunc) {
	c.onEventTemplateCardEvent = handler
}

// OnEventFeedbackEvent 用户反馈事件。
func (c *WSClient) OnEventFeedbackEvent(handler eventHandlerFunc) { c.onEventFeedbackEvent = handler }

// OnAuthenticated 认证成功。
func (c *WSClient) OnAuthenticated(handler connectionHandlerFunc) { c.userOnAuth = handler }

// OnDisconnected 断开。
func (c *WSClient) OnDisconnected(handler disconnectHandlerFunc) { c.onDisconnected = handler }

// OnReconnecting 重连中。
func (c *WSClient) OnReconnecting(handler reconnectHandlerFunc) { c.onReconnecting = handler }

// OnError 错误。
func (c *WSClient) OnError(handler errorHandlerFunc) { c.onError = handler }

func (c *WSClient) EmitMessage(frame *WsFrame) {
	if c.onMessage != nil {
		c.onMessage(frame)
	}
}

func (c *WSClient) EmitMessageText(frame *WsFrame) {
	if c.onMessageText != nil {
		c.onMessageText(frame)
	}
}

func (c *WSClient) EmitMessageImage(frame *WsFrame) {
	if c.onMessageImage != nil {
		c.onMessageImage(frame)
	}
}

func (c *WSClient) EmitMessageMixed(frame *WsFrame) {
	if c.onMessageMixed != nil {
		c.onMessageMixed(frame)
	}
}

func (c *WSClient) EmitMessageVoice(frame *WsFrame) {
	if c.onMessageVoice != nil {
		c.onMessageVoice(frame)
	}
}

func (c *WSClient) EmitMessageFile(frame *WsFrame) {
	if c.onMessageFile != nil {
		c.onMessageFile(frame)
	}
}

func (c *WSClient) EmitMessageVideo(frame *WsFrame) {
	if c.onMessageVideo != nil {
		c.onMessageVideo(frame)
	}
}

func (c *WSClient) EmitMessageStream(frame *WsFrame) {
	if c.onMessageStream != nil {
		c.onMessageStream(frame)
	}
}

func (c *WSClient) EmitEvent(frame *WsFrame) {
	if c.onEvent != nil {
		c.onEvent(frame)
	}
}

func (c *WSClient) EmitEventEnterChat(frame *WsFrame) {
	if c.onEventEnterChat != nil {
		c.onEventEnterChat(frame)
	}
}

func (c *WSClient) EmitEventTemplateCardEvent(frame *WsFrame) {
	if c.onEventTemplateCardEvent != nil {
		c.onEventTemplateCardEvent(frame)
	}
}

func (c *WSClient) EmitEventFeedbackEvent(frame *WsFrame) {
	if c.onEventFeedbackEvent != nil {
		c.onEventFeedbackEvent(frame)
	}
}

// Reply 通用回复（cmd 空则 aibot_respond_msg）。
func (c *WSClient) Reply(frame *WsFrame, body any, cmd string) (*WsFrame, error) {
	reqID := frame.Headers.ReqID
	if reqID == "" {
		return nil, fmt.Errorf("req_id is empty")
	}
	return c.wsManager.SendReply(reqID, body, cmd)
}

// ReplyStream 流式文本回复（单段完结：finish=true）。
func (c *WSClient) ReplyStream(frame *WsFrame, streamID, content string, finish bool, msgItem []ReplyMsgItem, feedback *ReplyFeedback) (*WsFrame, error) {
	stream := struct {
		ID       string         `json:"id"`
		Finish   bool           `json:"finish,omitempty"`
		Content  string         `json:"content,omitempty"`
		MsgItem  []ReplyMsgItem `json:"msg_item,omitempty"`
		Feedback *ReplyFeedback `json:"feedback,omitempty"`
	}{
		ID:       streamID,
		Finish:   finish,
		Content:  content,
		MsgItem:  msgItem,
		Feedback: feedback,
	}
	body := map[string]any{
		"msgtype": "stream",
		"stream":  stream,
	}
	return c.Reply(frame, body, "")
}

// ParseMessageBody 将 frame.Body 解到 v。
func ParseMessageBody(frame *WsFrame, v any) error {
	if frame == nil || frame.Body == nil {
		return fmt.Errorf("frame or body is nil")
	}
	return json.Unmarshal(frame.Body, v)
}
