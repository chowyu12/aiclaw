package mcpclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

// httpTransport 是 MCP 的 Streamable HTTP 传输（规范版本 2025-03-26）。
//
// 形状与 stdio 很不一样：没有长连接，每条请求就是一次 POST，响应可能是
//   - application/json —— 一条 JSON-RPC 响应，直接解；
//   - text/event-stream —— 一段 SSE，里面可能夹着若干通知，最后才是我们等的
//     那条响应。所以要一直读到 id 对上为止，不能读第一条就返回。
//
// 会话：服务端可以在 initialize 的响应头里给一个 Mcp-Session-Id，之后每条请求
// 都要带回去。不带的话有状态的服务端会把后续请求当成新会话拒掉。
//
// **不支持旧的 HTTP+SSE 传输**（2024-11-05 那版，GET /sse 拿事件、POST 到另一个
// 端点发消息）。那是另一套握手，半吊子实现出来只会在连不上时给出误导的报错；
// 遇到只支持旧版的服务端，这里会在握手阶段明确失败。
type httpTransport struct {
	url     string
	headers map[string]string
	client  *http.Client
	nextID  atomic.Int64

	sessionMu sync.Mutex
	sessionID string

	closed atomic.Bool
	// lastStatus 留着拼诊断信息：握手失败时光说「失败」没法查。
	lastStatus string
	statusMu   sync.Mutex
}

func startHTTP(config Config) (*httpTransport, error) {
	url := strings.TrimSpace(config.URL)
	if url == "" {
		return nil, errors.New("MCP server 地址为空")
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("MCP server 地址必须是 http(s)：%s", url)
	}
	headers := make(map[string]string, len(config.Headers))
	for key, value := range config.Headers {
		headers[key] = value
	}
	return &httpTransport{
		url:     url,
		headers: headers,
		// 不设 Timeout：单次工具调用可能很长，超时交给 ctx。这里只兜住连接建立。
		client: &http.Client{},
	}, nil
}

func (t *httpTransport) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if t.closed.Load() {
		return nil, errors.New("MCP server 已关闭")
	}
	id := t.nextID.Add(1)
	resp, err := t.post(ctx, rpcRequest{JSONRPC: "2.0", ID: &id, Method: method, Params: params}, &id)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("%s（code %d）", resp.Error.Message, resp.Error.Code)
	}
	return resp.Result, nil
}

func (t *httpTransport) Notify(method string, params any) {
	if t.closed.Load() {
		return
	}
	// 通知没有 id，服务端通常回 202 空响应。失败了也没人能处理，忽略。
	_, _ = t.post(context.Background(), rpcRequest{JSONRPC: "2.0", Method: method, Params: params}, nil)
}

func (t *httpTransport) Close() {
	if !t.closed.CompareAndSwap(false, true) {
		return
	}
	t.client.CloseIdleConnections()
}

func (t *httpTransport) Diagnostics() string {
	t.statusMu.Lock()
	defer t.statusMu.Unlock()
	if t.lastStatus == "" {
		return ""
	}
	return "\n" + t.lastStatus
}

// post 发一条消息并取回 wantID 对应的响应。wantID 为 nil 表示通知，不等回应。
func (t *httpTransport) post(ctx context.Context, message rpcRequest, wantID *int64) (rpcResponse, error) {
	body, err := json.Marshal(message)
	if err != nil {
		return rpcResponse{}, err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return rpcResponse{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	// 两种都收：服务端按自己的情况决定回 JSON 还是开一段 SSE。
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", protocolVersion)
	for key, value := range t.headers {
		request.Header.Set(key, value)
	}
	t.sessionMu.Lock()
	if t.sessionID != "" {
		request.Header.Set("Mcp-Session-Id", t.sessionID)
	}
	t.sessionMu.Unlock()

	response, err := t.client.Do(request)
	if err != nil {
		t.setStatus("请求失败：" + err.Error())
		return rpcResponse{}, fmt.Errorf("连接 MCP server 失败：%w", err)
	}
	defer response.Body.Close()

	// 服务端在 initialize 时分配会话 id，之后每条都要带回去。
	if id := response.Header.Get("Mcp-Session-Id"); id != "" {
		t.sessionMu.Lock()
		t.sessionID = id
		t.sessionMu.Unlock()
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		detail := strings.TrimSpace(string(snippet))
		t.setStatus(fmt.Sprintf("HTTP %d %s", response.StatusCode, detail))
		return rpcResponse{}, fmt.Errorf("MCP server 返回 HTTP %d：%s", response.StatusCode, detail)
	}
	if wantID == nil {
		return rpcResponse{}, nil
	}

	contentType := response.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/event-stream") {
		return readSSEResponse(response.Body, *wantID)
	}

	var resp rpcResponse
	raw, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if err != nil {
		return rpcResponse{}, fmt.Errorf("读取 MCP server 响应失败：%w", err)
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		// 可能是批量响应（数组）。取 id 对得上的那条。
		var batch []rpcResponse
		if json.Unmarshal(raw, &batch) == nil {
			for _, item := range batch {
				if item.ID != nil && *item.ID == *wantID {
					return item, nil
				}
			}
		}
		return rpcResponse{}, fmt.Errorf("MCP server 响应不是合法 JSON-RPC：%w", err)
	}
	return resp, nil
}

// readSSEResponse 读 SSE 直到拿到 id 对得上的那条响应。
//
// 中间夹的通知（服务端的进度上报之类）直接跳过：本客户端不处理它们，
// 但也不能把它们当成答案返回。
func readSSEResponse(body io.Reader, wantID int64) (rpcResponse, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	var data strings.Builder
	flush := func() (rpcResponse, bool) {
		payload := strings.TrimSpace(data.String())
		data.Reset()
		if payload == "" {
			return rpcResponse{}, false
		}
		var resp rpcResponse
		if json.Unmarshal([]byte(payload), &resp) != nil || resp.ID == nil {
			return rpcResponse{}, false
		}
		return resp, *resp.ID == wantID
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			// 空行分隔事件：到这里一个事件才算完整。
			if resp, ok := flush(); ok {
				return resp, nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue // 注释/心跳
		}
		if value, found := strings.CutPrefix(line, "data:"); found {
			data.WriteString(strings.TrimPrefix(value, " "))
		}
	}
	if err := scanner.Err(); err != nil {
		return rpcResponse{}, fmt.Errorf("读取 MCP 事件流失败：%w", err)
	}
	// 流结束前最后一个事件可能没有空行收尾。
	if resp, ok := flush(); ok {
		return resp, nil
	}
	return rpcResponse{}, errors.New("MCP 事件流结束但没有收到对应的响应")
}

func (t *httpTransport) setStatus(status string) {
	t.statusMu.Lock()
	t.lastStatus = status
	t.statusMu.Unlock()
}
