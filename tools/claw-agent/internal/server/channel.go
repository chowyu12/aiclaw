package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chowyu12/aiclaw/internal/model"
	pluginpkg "github.com/chowyu12/aiclaw/internal/plugin"
	legacy "github.com/chowyu12/aiclaw/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/agent"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

// channelGateway 把通道（微信、企业微信）收到的消息变成一轮对话。
//
// 这是入站消息变成一轮的**唯一**入口，它守两条规则：没见过的外部会话只记下来
// 等用户放行，不服务；放行了的跑在一个受限会话里——只读工具加用户逐个放开的，
// 审批档位是「无人值守」（要确认的操作直接失败），因为发消息的人不是本机用户。
//
// 通道插件是按旧版的事件模型（internal/protocol.Event）写的，这里把内核的
// 通知翻成那几种事件，插件代码一行不用改。
type channelGateway struct {
	server *Server
}

// 通道会话的 id 前缀，与桌面会话（s_）区分开，侧边栏里一眼能认出来。
const channelSessionPrefix = "c_"

// Submit 实现 plugin.Gateway。
func (g *channelGateway) Submit(ctx context.Context, pluginUUID string, message pluginpkg.Inbound, observe func(legacy.Event) error) error {
	s := g.server
	channelID := strings.TrimSpace(message.ChannelID)
	externalKey := strings.TrimSpace(message.ExternalKey)
	if pluginUUID == "" || channelID == "" || externalKey == "" {
		return errors.New("入站消息缺少插件、通道或会话标识")
	}
	binding, err := s.appDB.GetChannelBinding(ctx, pluginUUID, channelID, externalKey)
	if err != nil {
		return err
	}
	if binding == nil {
		// 第一次见到这个外部会话：记下来让用户去放行，这次什么都不做。
		pending := &model.ChannelBinding{
			PluginUUID: pluginUUID, ChannelID: channelID, ExternalKey: externalKey,
			DisplayName: strings.TrimSpace(message.DisplayName), Allowed: false,
			LastMessage: time.Now(),
		}
		if err := s.appDB.SaveChannelBinding(ctx, pending); err != nil {
			return err
		}
		return pluginpkg.ErrBindingNotAllowed
	}
	binding.LastMessage = time.Now()
	if !binding.Allowed {
		if err := s.appDB.SaveChannelBinding(ctx, binding); err != nil {
			return err
		}
		return pluginpkg.ErrBindingNotAllowed
	}
	if binding.ProviderID == 0 || strings.TrimSpace(binding.ModelName) == "" {
		return fmt.Errorf("会话「%s」还没选模型", bindingLabel(binding))
	}

	session, err := g.sessionFor(ctx, binding)
	if err != nil {
		return err
	}
	if err := s.appDB.SaveChannelBinding(ctx, binding); err != nil {
		return err
	}
	if session.Busy() {
		// 通道自己按会话串行派发，正常到不了这里；到了也别把输入排进队列——
		// 排队的输入没有出口，发消息的人会等一个永远不来的回复。
		return errors.New("上一条消息还在处理")
	}

	turnID := fmt.Sprintf("t_%d", time.Now().UnixNano())
	emitter := &channelEmitter{observe: observe, kinds: map[string]protocol.ItemKind{}}
	session.RunTurn(ctx, turnID, message.Text, nil, nil, emitter)
	if err := session.Save(ctx, s.db); err != nil {
		s.options.Logf("保存通道会话失败：%v", err)
	}
	return emitter.err
}

// sessionFor 找到或建出一个外部会话对应的本地会话。
//
// 会话是持久的（存在会话库里，桌面端的侧边栏也能看到它的历史），所以同一个
// 外部会话多次来消息接的是同一段上下文。模型来自放行时的选择，不跟桌面默认值走。
func (g *channelGateway) sessionFor(ctx context.Context, binding *model.ChannelBinding) (*agent.Session, error) {
	s := g.server
	if id := strings.TrimSpace(binding.ThreadUUID); id != "" {
		if existing := s.session(id); existing != nil {
			return existing, nil
		}
		loaded, err := agent.Load(ctx, s.db, id, s.keyFor, nil)
		if err == nil {
			s.guard(loaded)
			s.sessMu.Lock()
			s.sessions[id] = loaded
			s.sessMu.Unlock()
			return loaded, nil
		}
		// 旧会话丢了（用户删了它）就重建一个，别让这个外部会话从此聊不了。
		s.options.Logf("通道会话 %s 恢复失败，重建：%v", id, err)
	}
	var allowed []string
	if len(binding.AllowedTools) > 0 {
		_ = json.Unmarshal(binding.AllowedTools, &allowed)
	}
	id := channelSessionPrefix + fmt.Sprint(time.Now().UnixNano())
	session, err := agent.New(ctx, id, protocol.SessionStartParams{
		Model:          protocol.ModelConfig{ProviderID: binding.ProviderID, Model: binding.ModelName},
		ApprovalPolicy: protocol.ApprovalNever,
		// 只读工具是底线；写与执行按会话逐个放开。
		EnabledBuiltins: append(agent.ReadOnlyBuiltins(), allowed...),
		ProtectedPaths:  s.options.ProtectedPaths,
	}, s.keyFor)
	if err != nil {
		return nil, err
	}
	s.guard(session)
	session.Title = bindingLabel(binding)
	s.sessMu.Lock()
	s.sessions[id] = session
	s.sessMu.Unlock()
	if err := session.Save(ctx, s.db); err != nil {
		return nil, err
	}
	binding.ThreadUUID = id
	return session, nil
}

func bindingLabel(binding *model.ChannelBinding) string {
	if name := strings.TrimSpace(binding.DisplayName); name != "" {
		return name
	}
	return binding.ChannelID + ":" + binding.ExternalKey
}

// channelEmitter 把内核的通知翻成通道插件认的事件。
//
// 只翻三种：模型正文的增量（流式回复用）、轮次完成（带最终正文）、轮次失败。
// 工具步骤不翻——对聊天窗口另一头的人来说那是噪音。
type channelEmitter struct {
	observe func(legacy.Event) error

	mu    sync.Mutex
	kinds map[string]protocol.ItemKind
	final string
	// err 是 observe 第一次报的错；通道据此知道回复没送出去。
	err error
}

func (e *channelEmitter) Notify(method string, params any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch method {
	case protocol.NotifyItemStarted:
		if n, ok := params.(protocol.ItemNotification); ok {
			e.kinds[n.Item.ID] = n.Item.Kind
		}
	case protocol.NotifyItemDelta:
		n, ok := params.(protocol.ItemDeltaNotification)
		if !ok || e.kinds[n.ItemID] != protocol.ItemAgentMessage {
			return
		}
		e.emit(legacy.Event{Kind: legacy.EventAssistantDelta, Delta: n.Delta})
	case protocol.NotifyItemCompleted:
		if n, ok := params.(protocol.ItemNotification); ok && n.Item.Kind == protocol.ItemAgentMessage {
			e.final = n.Item.Text
		}
	case protocol.NotifyTurnCompleted:
		n, ok := params.(protocol.TurnNotification)
		if !ok {
			return
		}
		if n.Error != "" {
			e.emit(legacy.Event{Kind: legacy.EventTurnFailed, Error: n.Error})
			return
		}
		e.emit(legacy.Event{Kind: legacy.EventTurnCompleted, Output: e.final})
	}
}

func (e *channelEmitter) emit(event legacy.Event) {
	if e.observe == nil {
		return
	}
	if err := e.observe(event); err != nil && e.err == nil {
		e.err = err
	}
}

// RequestApproval 不会被调到：通道会话是「无人值守」档位，要确认的操作直接失败。
// 万一调到了也按拒绝处理——另一头没有人能点「允许」。
func (e *channelEmitter) RequestApproval(context.Context, protocol.ApprovalRequestParams) (protocol.ApprovalResponse, error) {
	return protocol.ApprovalResponse{}, errors.New("通道会话无人值守，需要确认的操作不执行")
}

// RequestComputer 同理：屏幕操作只能由桌面会话发起。
func (e *channelEmitter) RequestComputer(context.Context, protocol.ComputerRequestParams) (protocol.ComputerResult, error) {
	return protocol.ComputerResult{}, errors.New("通道会话不能操作屏幕")
}
