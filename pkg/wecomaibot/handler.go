package wecomaibot

import (
	"encoding/json"
	"fmt"
)

// FrameEmitter 消息/事件分发。
type FrameEmitter interface {
	EmitMessage(frame *WsFrame)
	EmitMessageText(frame *WsFrame)
	EmitMessageImage(frame *WsFrame)
	EmitMessageMixed(frame *WsFrame)
	EmitMessageVoice(frame *WsFrame)
	EmitMessageFile(frame *WsFrame)
	EmitMessageVideo(frame *WsFrame)
	EmitMessageStream(frame *WsFrame)
	EmitEvent(frame *WsFrame)
	EmitEventEnterChat(frame *WsFrame)
	EmitEventTemplateCardEvent(frame *WsFrame)
	EmitEventFeedbackEvent(frame *WsFrame)
}

// MessageHandler 解析回调帧并分发。
type MessageHandler struct {
	logger Logger
}

// NewMessageHandler 创建处理器。
func NewMessageHandler(logger Logger) *MessageHandler {
	if logger == nil {
		logger = nopLogger{}
	}
	return &MessageHandler{logger: logger}
}

// HandleFrame 处理下行帧。
func (h *MessageHandler) HandleFrame(frame *WsFrame, emitter FrameEmitter) {
	defer func() {
		if r := recover(); r != nil {
			h.logger.Error("Panic in HandleFrame: " + toStringRecover(r))
		}
	}()
	if frame == nil || frame.Body == nil {
		h.logger.Warn("Received invalid message format: frame is nil or body is empty")
		return
	}
	var bodyMap map[string]any
	if err := json.Unmarshal(frame.Body, &bodyMap); err != nil {
		h.logger.Warn("Failed to parse message body: " + err.Error())
		return
	}
	msgtype, _ := bodyMap["msgtype"].(string)
	if msgtype == "" {
		h.logger.Warn("Received message without msgtype")
		return
	}
	if frame.Cmd == WsCmd.EVENT_CALLBACK {
		h.handleEventCallback(frame, emitter, bodyMap)
		return
	}
	h.handleMessageCallback(frame, emitter, bodyMap, msgtype)
}

func (h *MessageHandler) handleMessageCallback(frame *WsFrame, emitter FrameEmitter, bodyMap map[string]any, msgtype string) {
	emitter.EmitMessage(frame)
	switch msgtype {
	case string(MessageTypeText):
		emitter.EmitMessageText(frame)
	case string(MessageTypeImage):
		emitter.EmitMessageImage(frame)
	case string(MessageTypeMixed):
		emitter.EmitMessageMixed(frame)
	case string(MessageTypeVoice):
		emitter.EmitMessageVoice(frame)
	case string(MessageTypeFile):
		emitter.EmitMessageFile(frame)
	case string(MessageTypeVideo):
		emitter.EmitMessageVideo(frame)
	case string(MessageTypeStream):
		emitter.EmitMessageStream(frame)
	default:
		h.logger.Debug("Received unhandled message type: " + msgtype)
	}
}

func (h *MessageHandler) handleEventCallback(frame *WsFrame, emitter FrameEmitter, bodyMap map[string]any) {
	eventRaw, ok := bodyMap["event"]
	if !ok {
		h.logger.Debug("Received event callback without event field")
		return
	}
	var eventMap map[string]any
	switch v := eventRaw.(type) {
	case map[string]any:
		eventMap = v
	case string:
		if err := json.Unmarshal([]byte(v), &eventMap); err != nil {
			h.logger.Error("Failed to parse event JSON: " + err.Error())
			return
		}
	default:
		h.logger.Debug("Received event callback with invalid event format")
		return
	}
	eventTypeRaw, ok := eventMap["eventtype"]
	if !ok {
		h.logger.Debug("Received event callback without eventtype")
		return
	}
	eventType, _ := eventTypeRaw.(string)
	if eventType == "" {
		h.logger.Debug("Received event callback with empty eventtype")
		return
	}
	emitter.EmitEvent(frame)
	switch eventType {
	case string(EventTypeEnterChat):
		emitter.EmitEventEnterChat(frame)
	case string(EventTypeTemplateCardEvent):
		emitter.EmitEventTemplateCardEvent(frame)
	case string(EventTypeFeedbackEvent):
		emitter.EmitEventFeedbackEvent(frame)
	default:
		h.logger.Debug("Received unhandled event type: " + eventType)
	}
}

func toStringRecover(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case error:
		return val.Error()
	default:
		return fmt.Sprint(val)
	}
}
