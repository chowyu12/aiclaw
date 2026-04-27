package wechatlink

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	DefaultBaseURL = "https://ilinkai.weixin.qq.com"
	// protocolVersion 对应 @tencent-weixin/openclaw-weixin 的 channel_version。
	// 服务端依据该字段做兼容性/能力路由，过旧的版本号可能被限流或拒绝服务。
	protocolVersion = "1.0.3"
	longPollTimeout = 60 * time.Second
	sendTimeout     = 15 * time.Second
)

// sharedHTTPClient 为包内 auth/cdn 等独立函数提供统一的 *http.Client，
// 避免每次请求新建客户端而无法复用底层 TCP/TLS 连接池。
// 注：Client.http 是每个登录会话独立的实例，保留原行为；这里只服务于无状态工具函数。
// 请求级超时由调用方通过 context.WithTimeout 控制。
var sharedHTTPClient = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:        32,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     90 * time.Second,
	},
}

// Client 微信 iLink Bot API 客户端。
type Client struct {
	baseURL  string
	botToken string
	botID    string
	http     *http.Client
	uin      string
	logger   Logger
}

// ClientOption 客户端可选参数。
type ClientOption func(*Client)

// WithLogger 设置日志。
func WithLogger(l Logger) ClientOption {
	return func(c *Client) { c.logger = l }
}

// NewClient 创建已认证客户端。
func NewClient(creds *Credentials, opts ...ClientOption) *Client {
	baseURL := strings.TrimRight(creds.BaseURL, "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	c := &Client{
		baseURL:  baseURL,
		botToken: creds.BotToken,
		botID:    creds.ILinkBotID,
		http:     sharedHTTPClient,
		uin:      generateUIN(),
		logger:   nopLogger{},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// BotID 返回当前 Bot ID。
func (c *Client) BotID() string { return c.botID }

// SendMessage 发送文本消息。
func (c *Client) SendMessage(ctx context.Context, toUserID, contextToken, clientID, text string) error {
	return c.SendMessageItems(ctx, toUserID, contextToken, clientID,
		[]MessageItem{{Type: ItemTypeText, TextItem: &TextItem{Text: text}}},
	)
}

// SendMessageItems 发送包含任意 ItemList 的消息（支持文字、图片混排）。
func (c *Client) SendMessageItems(ctx context.Context, toUserID, contextToken, clientID string, items []MessageItem) error {
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	var resp sendMessageResp
	if err := c.doPost(ctx, "/ilink/bot/sendmessage", sendMessageReq{
		Msg: sendMsg{
			FromUserID:   c.botID,
			ToUserID:     toUserID,
			ClientID:     clientID,
			MessageType:  MsgTypeBot,
			MessageState: MsgStateFinish,
			ItemList:     items,
			ContextToken: contextToken,
		},
	}, &resp); err != nil {
		return err
	}
	if resp.Ret != 0 {
		return fmt.Errorf("sendmessage ret=%d errmsg=%s", resp.Ret, resp.ErrMsg)
	}
	return nil
}

// SendTyping 发送"正在输入"状态。
func (c *Client) SendTyping(ctx context.Context, userID, contextToken string) error {
	cfgCtx, cfgCancel := context.WithTimeout(ctx, 10*time.Second)
	defer cfgCancel()
	var cfgResp getConfigResp
	if err := c.doPost(cfgCtx, "/ilink/bot/getconfig", getConfigReq{
		ILinkUserID:  userID,
		ContextToken: contextToken,
	}, &cfgResp); err != nil {
		return fmt.Errorf("getconfig: %w", err)
	}
	if cfgResp.TypingTicket == "" {
		return nil
	}
	typCtx, typCancel := context.WithTimeout(ctx, 10*time.Second)
	defer typCancel()
	var typResp sendTypingResp
	if err := c.doPost(typCtx, "/ilink/bot/sendtyping", sendTypingReq{
		ILinkUserID:  userID,
		TypingTicket: cfgResp.TypingTicket,
		Status:       TypingStatusTyping,
	}, &typResp); err != nil {
		return fmt.Errorf("sendtyping: %w", err)
	}
	if typResp.Ret != 0 {
		return fmt.Errorf("sendtyping ret=%d errmsg=%s", typResp.Ret, typResp.ErrMsg)
	}
	return nil
}

// GetUpdates 长轮询获取消息（服务端 hold 约 35 秒）。
// 第三个返回值 timeoutMs 来自服务端 longpolling_timeout_ms，便于上层判断连接活性。
func (c *Client) GetUpdates(ctx context.Context, buf string) (msgs []rawWeixinMsg, newBuf string, timeoutMs int, err error) {
	ctx, cancel := context.WithTimeout(ctx, longPollTimeout+10*time.Second)
	defer cancel()
	var resp getUpdatesResp
	if err := c.doPost(ctx, "/ilink/bot/getupdates", getUpdatesReq{
		GetUpdatesBuf: buf,
		BaseInfo:      baseInfo{ChannelVersion: protocolVersion},
	}, &resp); err != nil {
		return nil, buf, 0, err
	}
	// 关键校验：服务端在 HTTP 200 + ret!=0 的情况下也会返回业务错误（典型场景：
	// bot_token 失效 / 账号在别处下线）。漏掉这一步会让长轮询"看起来成功"
	// 但实际收不到任何消息，且没有任何错误日志。
	if resp.Ret != 0 {
		return nil, buf, resp.LongPollingTimeoutMs, fmt.Errorf("getupdates ret=%d errmsg=%s", resp.Ret, resp.ErrMsg)
	}
	return resp.Msgs, resp.GetUpdatesBuf, resp.LongPollingTimeoutMs, nil
}

// ────────────────────── HTTP 通信 ──────────────────────

func (c *Client) doPost(ctx context.Context, path string, body, result any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("AuthorizationType", "ilink_bot_token")
	req.Header.Set("Authorization", "Bearer "+c.botToken)
	req.Header.Set("X-WECHAT-UIN", c.uin)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	return json.Unmarshal(respBody, result)
}

func generateUIN() string {
	var n uint32
	_ = binary.Read(rand.Reader, binary.LittleEndian, &n)
	return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%d", n)))
}
