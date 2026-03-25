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
	DefaultBaseURL  = "https://ilinkai.weixin.qq.com"
	longPollTimeout = 60 * time.Second
	sendTimeout     = 15 * time.Second
)

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
		http:     &http.Client{},
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
			ItemList:     []MessageItem{{Type: ItemTypeText, TextItem: &TextItem{Text: text}}},
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

// GetUpdates 长轮询获取消息（服务端 hold 约 30-60 秒）。
func (c *Client) GetUpdates(ctx context.Context, buf string) (msgs []rawWeixinMsg, newBuf string, err error) {
	ctx, cancel := context.WithTimeout(ctx, longPollTimeout+10*time.Second)
	defer cancel()
	var resp getUpdatesResp
	if err := c.doPost(ctx, "/ilink/bot/getupdates", getUpdatesReq{
		GetUpdatesBuf: buf,
		BaseInfo:      baseInfo{ChannelVersion: "1.0.0"},
	}, &resp); err != nil {
		return nil, buf, err
	}
	return resp.Msgs, resp.GetUpdatesBuf, nil
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
