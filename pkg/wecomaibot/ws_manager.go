package wecomaibot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	defaultWSURL          = "wss://openws.work.weixin.qq.com"
	defaultReconnectDelay = 1000
	reconnectMaxDelay     = 30000
	maxMissedPong         = 2
	replyAckTimeout       = 5000
	maxReplyQueueSize     = 100
)

type replyQueueItem struct {
	frame   WsFrame
	resolve func(*WsFrame)
	reject  func(error)
}

type pendingAck struct {
	resolve func(*WsFrame)
	reject  func(error)
	timer   *time.Timer
}

// WsConnectionManager 维护企微智能机器人 WebSocket。
type WsConnectionManager struct {
	logger Logger
	wsURL  string

	heartbeatInterval    int
	reconnectBaseDelay   int
	maxReconnectAttempts int

	// connMu 保护 ws / isManualClose / reconnectAttempts / missedPongCount 等连接级状态。
	connMu        sync.Mutex
	ws            *websocket.Conn
	isManualClose bool

	botID     string
	botSecret string

	reconnectAttempts int
	missedPongCount   int

	// authOK 在收到 SUBSCRIBE 成功回包后置 1；断线、重连拨号开始时置 0。企微侧通常在认证完成前不会稳定下发用户消息。
	authOK atomic.Uint32

	heartbeatTimer *time.Timer

	replyQueues   map[string][]replyQueueItem
	replyQueuesMu sync.Mutex
	pendingAcks   map[string]*pendingAck
	pendingAcksMu sync.Mutex

	OnConnected     func()
	OnAuthenticated func()
	OnDisconnected  func(reason string)
	OnMessage       func(frame *WsFrame)
	OnReconnecting  func(attempt int)
	OnError         func(err error)

	ctx    context.Context
	cancel context.CancelFunc
}

// NewWsConnectionManager 创建管理器。
func NewWsConnectionManager(logger Logger, heartbeatInterval, reconnectBaseDelay, maxReconnectAttempts int, wsURL string) *WsConnectionManager {
	if logger == nil {
		logger = nopLogger{}
	}
	if heartbeatInterval <= 0 {
		heartbeatInterval = 30000
	}
	if reconnectBaseDelay <= 0 {
		reconnectBaseDelay = defaultReconnectDelay
	}
	if maxReconnectAttempts == 0 {
		maxReconnectAttempts = 10
	}
	if wsURL == "" {
		wsURL = defaultWSURL
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &WsConnectionManager{
		logger:               logger,
		wsURL:                wsURL,
		heartbeatInterval:    heartbeatInterval,
		reconnectBaseDelay:   reconnectBaseDelay,
		maxReconnectAttempts: maxReconnectAttempts,
		replyQueues:          make(map[string][]replyQueueItem),
		pendingAcks:          make(map[string]*pendingAck),
		ctx:                  ctx,
		cancel:               cancel,
	}
}

// SetCredentials 设置 bot_id / secret。
func (m *WsConnectionManager) SetCredentials(botID, botSecret string) {
	m.botID = botID
	m.botSecret = botSecret
}

// Connect 建立连接并发送认证。
func (m *WsConnectionManager) Connect() {
	m.connMu.Lock()
	m.isManualClose = false
	if m.ws != nil {
		_ = m.ws.Close()
		m.ws = nil
	}
	// Disconnect 会取消旧 context，每次 Connect 需要刷新。
	if m.ctx == nil || m.ctx.Err() != nil {
		m.ctx, m.cancel = context.WithCancel(context.Background())
	}
	m.connMu.Unlock()
	m.logger.Info("Connecting to WebSocket: " + m.wsURL + "...")
	go m.dialAndRun()
}

func (m *WsConnectionManager) dialAndRun() {
	m.authOK.Store(0)
	dialer := websocket.Dialer{}
	ws, _, err := dialer.Dial(m.wsURL, nil)
	if err != nil {
		m.logger.Error("Failed to create WebSocket connection: " + err.Error())
		if m.OnError != nil {
			m.OnError(err)
		}
		m.scheduleReconnect()
		return
	}
	m.connMu.Lock()
	m.ws = ws
	m.connMu.Unlock()
	m.setupReader()
	m.sendAuth()
}

func (m *WsConnectionManager) setupReader() {
	if m.ws == nil {
		return
	}
	go func() {
		for {
			select {
			case <-m.ctx.Done():
				return
			default:
			}
			_, data, err := m.ws.ReadMessage()
			if err != nil {
				if m.isManualClose {
					return
				}
				m.logger.Error("WebSocket read error: " + err.Error())
				m.handleClose(err.Error())
				return
			}
			m.handleIncoming(data)
		}
	}()
}

func (m *WsConnectionManager) handleIncoming(data []byte) {
	var frame WsFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		m.logger.Error("Failed to parse WebSocket message: " + err.Error())
		return
	}
	if frame.Cmd == WsCmd.CALLBACK || frame.Cmd == WsCmd.EVENT_CALLBACK {
		m.logger.Debug("Received push message: " + string(data))
		if m.OnMessage != nil {
			m.OnMessage(&frame)
		}
		return
	}
	reqID := frame.Headers.ReqID
	if m.hasPendingAck(reqID) {
		m.handleReplyAck(reqID, &frame)
		return
	}
	if strings.HasPrefix(reqID, WsCmd.SUBSCRIBE) {
		m.handleAuthResponse(&frame)
		return
	}
	if strings.HasPrefix(reqID, WsCmd.HEARTBEAT) {
		m.handleHeartbeatResponse(&frame)
		return
	}
	m.logger.Warn("Received unknown frame: " + string(data))
	if m.OnMessage != nil {
		m.OnMessage(&frame)
	}
}

func (m *WsConnectionManager) handleAuthResponse(frame *WsFrame) {
	if frame.ErrCode != 0 {
		m.authOK.Store(0)
		m.logger.Error(fmt.Sprintf("Authentication failed: errcode=%d, errmsg=%s", frame.ErrCode, frame.ErrMsg))
		if m.OnError != nil {
			m.OnError(fmt.Errorf("authentication failed: %s (code: %d)", frame.ErrMsg, frame.ErrCode))
		}
		return
	}
	m.logger.Info("Authentication successful")
	m.authOK.Store(1)
	m.startHeartbeat()
	if m.OnAuthenticated != nil {
		m.OnAuthenticated()
	}
}

func (m *WsConnectionManager) handleHeartbeatResponse(frame *WsFrame) {
	if frame.ErrCode != 0 {
		m.logger.Warn(fmt.Sprintf("Heartbeat ack error: errcode=%d, errmsg=%s", frame.ErrCode, frame.ErrMsg))
		return
	}
	m.connMu.Lock()
	m.missedPongCount = 0
	m.connMu.Unlock()
	m.logger.Debug("Received heartbeat ack")
}

func (m *WsConnectionManager) handleClose(reason string) {
	m.authOK.Store(0)
	m.stopHeartbeat()
	m.clearPendingMessages("WebSocket connection closed (" + reason + ")")
	if m.OnDisconnected != nil {
		m.OnDisconnected(reason)
	}
	m.connMu.Lock()
	manual := m.isManualClose
	m.connMu.Unlock()
	if !manual {
		m.scheduleReconnect()
	}
}

func (m *WsConnectionManager) sendAuth() {
	m.connMu.Lock()
	if m.ws == nil {
		m.connMu.Unlock()
		return
	}
	m.reconnectAttempts = 0
	m.missedPongCount = 0
	m.connMu.Unlock()
	body, err := json.Marshal(map[string]string{"bot_id": m.botID, "secret": m.botSecret})
	if err != nil {
		m.logger.Error("Failed to marshal auth body: " + err.Error())
		return
	}
	frame := WsFrame{
		Cmd: WsCmd.SUBSCRIBE,
		Headers: WsFrameHeaders{
			ReqID: generateReqID(WsCmd.SUBSCRIBE),
		},
		Body: body,
	}
	m.sendFrame(frame)
	m.logger.Info("Auth frame sent")
}

func (m *WsConnectionManager) sendHeartbeat() {
	m.connMu.Lock()
	if m.missedPongCount >= maxMissedPong {
		m.logger.Warn(fmt.Sprintf("No heartbeat ack for %d consecutive pings, closing", m.missedPongCount))
		m.connMu.Unlock()
		m.stopHeartbeat()
		m.connMu.Lock()
		if m.ws != nil {
			_ = m.ws.Close()
		}
		m.connMu.Unlock()
		return
	}
	m.missedPongCount++
	m.connMu.Unlock()

	frame := WsFrame{
		Cmd: WsCmd.HEARTBEAT,
		Headers: WsFrameHeaders{
			ReqID: generateReqID(WsCmd.HEARTBEAT),
		},
	}
	m.sendFrame(frame)
	m.logger.Debug("Heartbeat sent")
	m.startHeartbeat()
}

func (m *WsConnectionManager) startHeartbeat() {
	m.stopHeartbeat()
	m.heartbeatTimer = time.AfterFunc(
		time.Duration(m.heartbeatInterval)*time.Millisecond,
		m.sendHeartbeat,
	)
	m.logger.Debug(fmt.Sprintf("Heartbeat timer started, interval: %dms", m.heartbeatInterval))
}

func (m *WsConnectionManager) stopHeartbeat() {
	if m.heartbeatTimer != nil {
		m.heartbeatTimer.Stop()
		m.heartbeatTimer = nil
		m.logger.Debug("Heartbeat timer stopped")
	}
}

func (m *WsConnectionManager) scheduleReconnect() {
	m.connMu.Lock()
	if m.maxReconnectAttempts > 0 && m.reconnectAttempts >= m.maxReconnectAttempts {
		m.connMu.Unlock()
		m.logger.Error(fmt.Sprintf("Max reconnect attempts reached (%d), giving up", m.maxReconnectAttempts))
		if m.OnError != nil {
			m.OnError(errors.New("max reconnect attempts exceeded"))
		}
		return
	}
	m.reconnectAttempts++
	attempt := m.reconnectAttempts
	m.connMu.Unlock()

	delay := m.reconnectBaseDelay
	if attempt > 1 {
		delay = m.reconnectBaseDelay * (1 << (attempt - 1))
	}
	if delay > reconnectMaxDelay {
		delay = reconnectMaxDelay
	}
	m.logger.Info(fmt.Sprintf("Reconnecting in %dms (attempt %d)...", delay, attempt))
	if m.OnReconnecting != nil {
		m.OnReconnecting(attempt)
	}
	time.AfterFunc(time.Duration(delay)*time.Millisecond, func() {
		m.connMu.Lock()
		manual := m.isManualClose
		m.connMu.Unlock()
		if manual {
			return
		}
		m.Connect()
	})
}

func (m *WsConnectionManager) sendFrame(frame WsFrame) {
	data, err := json.Marshal(frame)
	if err != nil {
		m.logger.Error("Failed to marshal frame: " + err.Error())
		return
	}
	m.connMu.Lock()
	ws := m.ws
	m.connMu.Unlock()
	if ws == nil {
		return
	}
	if err := ws.WriteMessage(websocket.TextMessage, data); err != nil {
		m.logger.Error("Failed to send frame: " + err.Error())
	}
}

// Send 发送原始帧。
func (m *WsConnectionManager) Send(frame WsFrame) error {
	data, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	m.connMu.Lock()
	ws := m.ws
	m.connMu.Unlock()
	if ws == nil {
		return errors.New("WebSocket not connected, unable to send data")
	}
	return ws.WriteMessage(websocket.TextMessage, data)
}

// SendReply 发送回复并等待回执。
func (m *WsConnectionManager) SendReply(reqID string, body any, cmd string) (*WsFrame, error) {
	if cmd == "" {
		cmd = WsCmd.RESPONSE
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	frame := WsFrame{
		Cmd: cmd,
		Headers: WsFrameHeaders{
			ReqID: reqID,
		},
		Body: bodyJSON,
	}
	resultChan := make(chan *WsFrame, 1)
	errChan := make(chan error, 1)
	item := replyQueueItem{
		frame: frame,
		resolve: func(ack *WsFrame) {
			resultChan <- ack
		},
		reject: func(e error) {
			errChan <- e
		},
	}
	m.enqueueReply(reqID, item)
	select {
	case ack := <-resultChan:
		return ack, nil
	case e := <-errChan:
		return nil, e
	}
}

func (m *WsConnectionManager) enqueueReply(reqID string, item replyQueueItem) {
	m.replyQueuesMu.Lock()
	defer m.replyQueuesMu.Unlock()
	if _, ok := m.replyQueues[reqID]; !ok {
		m.replyQueues[reqID] = []replyQueueItem{}
	}
	queue := m.replyQueues[reqID]
	if len(queue) >= maxReplyQueueSize {
		m.logger.Warn(fmt.Sprintf("Reply queue for reqId %s exceeds max size (%d)", reqID, maxReplyQueueSize))
		item.reject(fmt.Errorf("reply queue for reqId %s exceeds max size", reqID))
		return
	}
	queue = append(queue, item)
	m.replyQueues[reqID] = queue
	if len(queue) == 1 {
		go m.processReplyQueue(reqID)
	}
}

func (m *WsConnectionManager) processReplyQueue(reqID string) {
	m.replyQueuesMu.Lock()
	queue, ok := m.replyQueues[reqID]
	if !ok || len(queue) == 0 {
		m.replyQueuesMu.Unlock()
		return
	}
	item := queue[0]
	m.replyQueuesMu.Unlock()
	if err := m.Send(item.frame); err != nil {
		m.logger.Error(fmt.Sprintf("Failed to send reply for reqId %s: %s", reqID, err.Error()))
		m.replyQueuesMu.Lock()
		if q, exists := m.replyQueues[reqID]; exists && len(q) > 0 {
			m.replyQueues[reqID] = q[1:]
		}
		m.replyQueuesMu.Unlock()
		item.reject(err)
		m.processReplyQueue(reqID)
		return
	}
	m.logger.Debug(fmt.Sprintf("Reply message sent via WebSocket, reqId: %s", reqID))
	m.addPendingAck(reqID, item.resolve, item.reject)
}

func (m *WsConnectionManager) addPendingAck(reqID string, resolve func(*WsFrame), reject func(error)) {
	m.pendingAcksMu.Lock()
	defer m.pendingAcksMu.Unlock()
	if existing, ok := m.pendingAcks[reqID]; ok {
		existing.timer.Stop()
	}
	timer := time.AfterFunc(time.Duration(replyAckTimeout)*time.Millisecond, func() {
		m.handleReplyAckTimeout(reqID)
	})
	m.pendingAcks[reqID] = &pendingAck{
		resolve: resolve,
		reject:  reject,
		timer:   timer,
	}
}

func (m *WsConnectionManager) hasPendingAck(reqID string) bool {
	m.pendingAcksMu.Lock()
	defer m.pendingAcksMu.Unlock()
	_, ok := m.pendingAcks[reqID]
	return ok
}

func (m *WsConnectionManager) handleReplyAck(reqID string, frame *WsFrame) {
	m.pendingAcksMu.Lock()
	pending, ok := m.pendingAcks[reqID]
	if !ok {
		m.pendingAcksMu.Unlock()
		return
	}
	pending.timer.Stop()
	delete(m.pendingAcks, reqID)
	m.pendingAcksMu.Unlock()

	m.replyQueuesMu.Lock()
	if q, exists := m.replyQueues[reqID]; exists && len(q) > 0 {
		m.replyQueues[reqID] = q[1:]
		if len(m.replyQueues[reqID]) == 0 {
			delete(m.replyQueues, reqID)
		}
	}
	m.replyQueuesMu.Unlock()

	if frame.ErrCode != 0 {
		m.logger.Warn(fmt.Sprintf("Reply ack error: reqId=%s, errcode=%d, errmsg=%s", reqID, frame.ErrCode, frame.ErrMsg))
		pending.reject(fmt.Errorf("reply ack error: %s (code: %d)", frame.ErrMsg, frame.ErrCode))
	} else {
		m.logger.Debug(fmt.Sprintf("Reply ack received for reqId: %s", reqID))
		pending.resolve(frame)
	}
	m.processReplyQueue(reqID)
}

func (m *WsConnectionManager) handleReplyAckTimeout(reqID string) {
	m.pendingAcksMu.Lock()
	pending, ok := m.pendingAcks[reqID]
	if !ok {
		m.pendingAcksMu.Unlock()
		return
	}
	delete(m.pendingAcks, reqID)
	m.pendingAcksMu.Unlock()

	m.logger.Warn(fmt.Sprintf("Reply ack timeout (%dms) for reqId: %s", replyAckTimeout, reqID))

	m.replyQueuesMu.Lock()
	if q, exists := m.replyQueues[reqID]; exists && len(q) > 0 {
		m.replyQueues[reqID] = q[1:]
		if len(m.replyQueues[reqID]) == 0 {
			delete(m.replyQueues, reqID)
		}
	}
	m.replyQueuesMu.Unlock()

	pending.reject(fmt.Errorf("reply ack timeout (%dms) for reqId: %s", replyAckTimeout, reqID))
	m.processReplyQueue(reqID)
}

func (m *WsConnectionManager) clearPendingMessages(reason string) {
	m.pendingAcksMu.Lock()
	for _, p := range m.pendingAcks {
		p.timer.Stop()
	}
	m.pendingAcks = make(map[string]*pendingAck)
	m.pendingAcksMu.Unlock()

	m.replyQueuesMu.Lock()
	for reqID, queue := range m.replyQueues {
		for _, item := range queue {
			item.reject(errors.New(reason + ", reply for reqId: " + reqID + " cancelled"))
		}
	}
	m.replyQueues = make(map[string][]replyQueueItem)
	m.replyQueuesMu.Unlock()
}

// Disconnect 断开连接。
func (m *WsConnectionManager) Disconnect() {
	m.connMu.Lock()
	m.isManualClose = true
	m.connMu.Unlock()

	m.authOK.Store(0)
	if m.cancel != nil {
		m.cancel()
	}
	m.stopHeartbeat()
	m.clearPendingMessages("Connection manually closed")

	m.connMu.Lock()
	if m.ws != nil {
		_ = m.ws.Close()
		m.ws = nil
	}
	m.connMu.Unlock()
	m.logger.Info("WebSocket connection manually closed")
}

// IsConnected 是否已连接（存在底层 conn）。
func (m *WsConnectionManager) IsConnected() bool {
	m.connMu.Lock()
	defer m.connMu.Unlock()
	return m.ws != nil
}

// IsAuthenticated 是否已完成 SUBSCRIBE（订阅/认证）成功；此前企微通常不会稳定推送用户消息。
func (m *WsConnectionManager) IsAuthenticated() bool {
	return m.authOK.Load() == 1
}
