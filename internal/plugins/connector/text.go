package connector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/chowyu12/aiclaw/internal/i18n"
	"github.com/chowyu12/aiclaw/internal/plugin"
)

// TextInbox runs complete text replies through the same authorization
// gateway as the streaming connectors. The transport only supplies a reply target.
type TextInbox struct {
	ctx        context.Context
	cancel     context.CancelFunc
	deps       plugin.ChannelDeps
	channel    string
	dispatcher *Dispatcher
	mu         sync.Mutex
	seen       map[string]struct{}
	recent     [1024]string
	next       int
}

func NewTextInbox(ctx context.Context, deps plugin.ChannelDeps, channel string) *TextInbox {
	ctx, cancel := context.WithCancel(ctx)
	return &TextInbox{ctx: ctx, cancel: cancel, deps: deps, channel: channel, dispatcher: NewDispatcher(4), seen: make(map[string]struct{})}
}

func (i *TextInbox) Close() {
	i.mu.Lock()
	i.cancel()
	i.mu.Unlock()
	i.dispatcher.Wait()
}

func (i *TextInbox) Submit(messageID string, msg plugin.Inbound, send func(context.Context, string) error) bool {
	if messageID == "" || msg.ExternalKey == "" || strings.TrimSpace(msg.Text) == "" {
		return true
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.ctx.Err() != nil {
		return false
	}
	if _, exists := i.seen[messageID]; exists {
		return true
	}
	msg.ChannelID, msg.ConnectionID = i.channel, i.deps.ConnectionID
	if !i.dispatcher.Submit(i.ctx, msg.ExternalKey, func(ctx context.Context) {
		if ctx.Err() != nil {
			return
		}
		reply := NewReply(nil)
		err := i.deps.Gateway.Submit(ctx, i.deps.PluginUUID, msg, reply.Observe)
		text := reply.Text()
		switch {
		case errors.Is(err, plugin.ErrBindingNotAllowed):
			text = i18n.D("这个会话还没有被授权访问助手，请在桌面端的连接设置里放行后再试。")
		case err != nil:
			text = i18n.D("处理这条消息时出错了，请稍后再试。")
		case reply.Failed() || strings.TrimSpace(text) == "":
			text = i18n.D("这次没有得到可用的回复，请换个说法再试一次。")
		}
		if ctx.Err() != nil {
			return
		}
		replyCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		if err := send(replyCtx, text); err != nil && i.deps.Log != nil {
			i.deps.Log("%s connector could not deliver a reply", i.channel)
		}
	}) {
		return false
	}
	delete(i.seen, i.recent[i.next])
	i.recent[i.next] = messageID
	i.next = (i.next + 1) % len(i.recent)
	i.seen[messageID] = struct{}{}
	return true
}

// PostJSON never follows a redirect or reports a URL/body that may contain a credential.
func PostJSON(ctx context.Context, client *http.Client, endpoint string, body, result any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return errors.New("could not encode request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return errors.New("invalid request endpoint")
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("could not reach messaging service")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("messaging service returned HTTP %d", res.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(result); err != nil {
		return errors.New("messaging service returned invalid JSON")
	}
	return nil
}

func ChannelHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}
