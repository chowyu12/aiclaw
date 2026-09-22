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
	"time"
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
	transport transport
	tools     []ToolDef
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
		return nil, fmt.Errorf("不认识的 MCP 传输方式：%s", kind)
	}
	if err != nil {
		return nil, err
	}

	client := &Client{transport: conn}

	startCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if _, err := conn.Call(startCtx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "claw-agent", "version": "0.1.0"},
	}); err != nil {
		client.Close()
		return nil, fmt.Errorf("MCP 握手失败：%w%s", err, conn.Diagnostics())
	}
	conn.Notify("notifications/initialized", nil)

	raw, err := conn.Call(startCtx, "tools/list", map[string]any{})
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("拉取工具清单失败：%w%s", err, conn.Diagnostics())
	}
	var list struct {
		Tools []ToolDef `json:"tools"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		client.Close()
		return nil, fmt.Errorf("工具清单格式不对：%w", err)
	}
	client.tools = list.Tools
	return client, nil
}

func (c *Client) Tools() []ToolDef { return c.tools }

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
		return "", fmt.Errorf("工具结果格式不对：%w", err)
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
			text = "工具执行失败"
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
