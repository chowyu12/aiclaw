// Package mcpclient 是一个最小 MCP 客户端，支持两种传输：
//
//   - stdio：把 server 作为子进程拉起来（claw-mcp、绝大多数本地 MCP server）；
//   - Streamable HTTP：POST 到一个地址，响应是 JSON 或 SSE（远程 MCP server）。
//
// 方法只做 initialize / tools/list / tools/call 三个，与 claw-mcp 那侧的最小
// 服务端正好对上。公司内的 公司内部那个 mcpclient 只支持 HTTP、明确不做 stdio，
// 所以这里另写一份；不引第三方库的理由与 claw-mcp 相同。
package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chowyu12/aiclaw/internal/i18n"
)

const protocolVersion = "2025-03-26"

// TransportKind 是传输方式。
type TransportKind string

const (
	TransportStdio TransportKind = "stdio"
	TransportHTTP  TransportKind = "http"
)

// ToolDef 是服务端暴露的一个工具。
type ToolDef struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	InputSchema json.RawMessage  `json:"inputSchema"`
	Annotations *ToolAnnotations `json:"annotations,omitempty"`
}

// ToolAnnotations 是 MCP 2025-03-26 的工具提示。
//
// 这里只关心 readOnlyHint：它决定要不要走审批。用协议自带的字段而不是
// 自己发明一个标记，是因为第三方 server 也会给这个提示，而它们**不给**
// 的时候正好是我们想要的默认——副作用未知就按有副作用处理。
type ToolAnnotations struct {
	ReadOnlyHint *bool `json:"readOnlyHint,omitempty"`
}

// ReadOnly 报告这个工具是否声明了自己只读。
//
// 没声明就当成有副作用：没有沙箱之后，不确定的时候要问。
func (t ToolDef) ReadOnly() bool {
	return t.Annotations != nil && t.Annotations.ReadOnlyHint != nil && *t.Annotations.ReadOnlyHint
}

type Config struct {
	onToolsChanged func()
	AccessToken    func(context.Context) (string, error)
	// Transport 为空时按有没有 URL 推断：有 URL 走 HTTP，否则 stdio。
	Transport TransportKind

	// ---- stdio ----
	Command string
	Args    []string
	// Env 追加到子进程环境。凭据只走这里，不进任何文件。
	Env map[string]string

	// ---- http ----
	URL string
	// Headers 附加到每条请求，鉴权（Authorization 等）走这里。
	Headers map[string]string

	// StartupTimeout 是 initialize + tools/list 的总时限。
	StartupTimeout time.Duration
}

// transport 抽掉两种传输的差异。上层只管发请求收响应。
type transport interface {
	Call(ctx context.Context, method string, params any) (json.RawMessage, error)
	Notify(method string, params any)
	Close()
	// Diagnostics 返回排障用的尾巴：stdio 是子进程 stderr，HTTP 是最后一次
	// 请求的状态。握手失败时真正的原因几乎总在这里面。
	Diagnostics() string
}

type Client struct {
	transport  transport
	toolsMu    sync.RWMutex
	tools      []ToolDef
	version    uint64
	changed    atomic.Uint64
	applied    uint64
	refreshMu  sync.Mutex
	retryAfter time.Time
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      *int64 `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	ID     *int64          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

// Start 建立连接并完成握手，返回可用的工具清单。
func Start(ctx context.Context, config Config) (*Client, error) {
	timeout := config.StartupTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	kind := config.Transport
	if kind == "" {
		if strings.TrimSpace(config.URL) != "" {
			kind = TransportHTTP
		} else {
			kind = TransportStdio
		}
	}

	client := &Client{}
	config.onToolsChanged = func() { client.changed.Add(1) }
	var (
		conn transport
		err  error
	)
	switch kind {
	case TransportStdio:
		conn, err = startStdio(config)
	case TransportHTTP:
		conn, err = startHTTP(config)
	default:
		return nil, i18n.E("不认识的 MCP 传输方式：{kind}", "kind", kind)
	}
	if err != nil {
		return nil, err
	}

	client.transport = conn

	startCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	initialized, err := conn.Call(startCtx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "claw-agent", "version": "0.1.0"},
	})
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("%s%w%s", i18n.D("MCP 握手失败："), err, conn.Diagnostics())
	}
	conn.Notify("notifications/initialized", nil)

	generation := client.changed.Load()
	list, err := client.listTools(startCtx)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("%s%w", i18n.D("拉取工具清单失败："), err)
	}
	client.tools, client.version, client.applied = list, 1, generation
	var capabilities struct {
		Capabilities struct {
			Tools struct {
				ListChanged bool `json:"listChanged"`
			} `json:"tools"`
		} `json:"capabilities"`
	}
	_ = json.Unmarshal(initialized, &capabilities)
	if http, ok := conn.(*httpTransport); ok && capabilities.Capabilities.Tools.ListChanged {
		http.startNotifications()
	}
	return client, nil
}

func (c *Client) Catalog() ([]ToolDef, uint64) {
	c.toolsMu.RLock()
	defer c.toolsMu.RUnlock()
	return append([]ToolDef(nil), c.tools...), c.version
}
func (c *Client) Tools() []ToolDef { tools, _ := c.Catalog(); return tools }
func (c *Client) Version() uint64  { c.toolsMu.RLock(); defer c.toolsMu.RUnlock(); return c.version }

// Refresh only after a notification. Failures keep the last usable catalog and
// back off; shared connections coalesce concurrent refresh requests.
func (c *Client) RefreshTools(ctx context.Context) error {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	if c.changed.Load() == c.applied || time.Now().Before(c.retryAfter) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for attempt := 0; attempt < 3; attempt++ {
		generation := c.changed.Load()
		list, err := c.listTools(ctx)
		if err != nil {
			c.retryAfter = time.Now().Add(time.Second)
			return err
		}
		if generation != c.changed.Load() {
			continue
		}
		c.toolsMu.Lock()
		c.tools = list
		c.version++
		c.toolsMu.Unlock()
		c.applied = generation
		return nil
	}
	c.retryAfter = time.Now().Add(time.Second)
	return fmt.Errorf("MCP tool list changed repeatedly during refresh")
}

func (c *Client) listTools(ctx context.Context) ([]ToolDef, error) {
	result := []ToolDef{}
	cursor := ""
	seen := map[string]bool{}
	for page := 0; page < 100; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.transport.Call(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var list struct {
			Tools      []ToolDef `json:"tools"`
			NextCursor string    `json:"nextCursor"`
		}
		if err = json.Unmarshal(raw, &list); err != nil {
			return nil, err
		}
		for _, tool := range list.Tools {
			if tool.Name == "" || seen[tool.Name] {
				return nil, fmt.Errorf("invalid or duplicate MCP tool name")
			}
			seen[tool.Name] = true
		}
		result = append(result, list.Tools...)
		if len(result) > 10000 {
			return nil, fmt.Errorf("MCP catalog too large")
		}
		if list.NextCursor == "" {
			return result, nil
		}
		if list.NextCursor == cursor {
			return nil, fmt.Errorf("MCP cursor did not advance")
		}
		cursor = list.NextCursor
	}
	return nil, fmt.Errorf("MCP catalog page limit exceeded")
}

func toolsChanged(raw []byte, notify func()) bool {
	var message struct {
		Method string           `json:"method"`
		ID     *json.RawMessage `json:"id"`
	}
	if json.Unmarshal(raw, &message) != nil || message.ID != nil || message.Method != "notifications/tools/list_changed" {
		return false
	}
	if notify != nil {
		notify()
	}
	return true
}

// CallTool 执行一次工具调用。
//
// 服务端回 isError 时这里返回 error 并把文本带出来——MCP 里那是「工具失败」，
// 与协议错误分开处理：前者要让模型看见原因，后者才是故障。
func (c *Client) CallTool(ctx context.Context, name string, arguments json.RawMessage) (string, error) {
	if len(arguments) == 0 {
		arguments = json.RawMessage(`{}`)
	}
	raw, err := c.transport.Call(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": arguments,
	})
	if err != nil {
		return "", err
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("%s%w", i18n.D("工具结果格式不对："), err)
	}
	var builder strings.Builder
	for _, item := range result.Content {
		if item.Type == "text" {
			builder.WriteString(item.Text)
		}
	}
	text := builder.String()
	if result.IsError {
		if text == "" {
			text = i18n.D("工具执行失败")
		}
		return "", errors.New(text)
	}
	return text, nil
}

func (c *Client) Close() {
	if c.transport != nil {
		c.transport.Close()
	}
}
