package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeHTTPServer 是一个最小的 Streamable HTTP MCP server。
//
// 它按 sse 开关决定用哪种响应形态——两条路都要测：真实世界里两种都有，
// 而 SSE 那条的解析（要一直读到 id 对上）是最容易写错的地方。
type fakeHTTPServer struct {
	sse bool
	// sessionID 非空时，initialize 之后的请求必须带回同样的 id。
	sessionID string

	mu       sync.Mutex
	requests []string
	headers  []http.Header
	// noticeBeforeAnswer 让服务端在真正的响应之前先插一条通知（SSE 模式）。
	noticeBeforeAnswer bool
}

func (f *fakeHTTPServer) handler(w http.ResponseWriter, r *http.Request) {
	var message struct {
		ID     *int64          `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	_ = json.NewDecoder(r.Body).Decode(&message)

	f.mu.Lock()
	f.requests = append(f.requests, message.Method)
	f.headers = append(f.headers, r.Header.Clone())
	f.mu.Unlock()

	// 有会话时，除 initialize 外都必须带回会话 id。
	if f.sessionID != "" && message.Method != "initialize" {
		if r.Header.Get("Mcp-Session-Id") != f.sessionID {
			http.Error(w, "missing session", http.StatusNotFound)
			return
		}
	}
	if message.ID == nil {
		w.WriteHeader(http.StatusAccepted) // 通知
		return
	}
	if f.sessionID != "" && message.Method == "initialize" {
		w.Header().Set("Mcp-Session-Id", f.sessionID)
	}

	result := f.resultFor(message.Method)
	payload := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":%s}`, *message.ID, result)

	if !f.sse {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	if f.noticeBeforeAnswer {
		// 一条没有 id 的通知，以及一条 id 对不上的响应。客户端都该跳过。
		_, _ = fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"id\":9999,\"result\":{\"wrong\":true}}\n\n")
	}
	_, _ = fmt.Fprintf(w, ": keep-alive\ndata: %s\n\n", payload)
}

func (f *fakeHTTPServer) resultFor(method string) string {
	switch method {
	case "initialize":
		return `{"protocolVersion":"2025-03-26","capabilities":{},"serverInfo":{"name":"fake","version":"1"}}`
	case "tools/list":
		return `{"tools":[{"name":"echo","description":"回显","inputSchema":{"type":"object"}}]}`
	case "tools/call":
		return `{"content":[{"type":"text","text":"远程结果"}]}`
	default:
		return `{}`
	}
}

func (f *fakeHTTPServer) methods() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

func startFake(t *testing.T, fake *fakeHTTPServer) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(fake.handler))
	t.Cleanup(server.Close)
	return server.URL
}

func TestHTTPTransportHandshakeWithJSONResponses(t *testing.T) {
	fake := &fakeHTTPServer{}
	client, err := Start(context.Background(), Config{URL: startFake(t, fake)})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(client.Close)

	if len(client.Tools()) != 1 || client.Tools()[0].Name != "echo" {
		t.Fatalf("工具清单不对：%+v", client.Tools())
	}
	got, err := client.CallTool(context.Background(), "echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got != "远程结果" {
		t.Errorf("工具结果 = %q", got)
	}
}

func TestHTTPTransportReadsSSEUntilMatchingID(t *testing.T) {
	// 响应前面夹着通知和一条 id 对不上的响应。读第一条就返回的实现会在这里错。
	fake := &fakeHTTPServer{sse: true, noticeBeforeAnswer: true}
	client, err := Start(context.Background(), Config{URL: startFake(t, fake)})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(client.Close)

	if len(client.Tools()) != 1 {
		t.Fatalf("SSE 模式下工具清单没解出来：%+v", client.Tools())
	}
	got, err := client.CallTool(context.Background(), "echo", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "远程结果" {
		t.Errorf("工具结果 = %q", got)
	}
}

func TestHTTPTransportEchoesSessionID(t *testing.T) {
	// 服务端在 initialize 时分配会话 id。不带回去的话它会 404，
	// 而那个 404 看起来像"地址写错了"，极难查。
	fake := &fakeHTTPServer{sessionID: "sess-abc"}
	client, err := Start(context.Background(), Config{URL: startFake(t, fake)})
	if err != nil {
		t.Fatalf("会话 id 没有带回去：%v", err)
	}
	t.Cleanup(client.Close)

	if _, err := client.CallTool(context.Background(), "echo", nil); err != nil {
		t.Fatalf("握手之后的调用也要带会话 id：%v", err)
	}
}

func TestHTTPTransportSendsCustomHeaders(t *testing.T) {
	fake := &fakeHTTPServer{}
	client, err := Start(context.Background(), Config{
		URL:     startFake(t, fake),
		Headers: map[string]string{"Authorization": "Bearer secret-token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)

	fake.mu.Lock()
	defer fake.mu.Unlock()
	for index, header := range fake.headers {
		if header.Get("Authorization") != "Bearer secret-token" {
			t.Fatalf("第 %d 条请求没带上鉴权头", index)
		}
		if !strings.Contains(header.Get("Accept"), "text/event-stream") {
			t.Errorf("Accept 要同时接受 JSON 与 SSE，实际 %q", header.Get("Accept"))
		}
	}
}

func TestHTTPTransportSurfacesUpstreamStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "token 过期了", http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)

	_, err := Start(context.Background(), Config{URL: server.URL})
	if err == nil {
		t.Fatal("上游 401 应当让握手失败")
	}
	// 原因要带出来：只说「握手失败」的话用户没法判断是地址错还是 Key 错。
	if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "token 过期了") {
		t.Errorf("错误里应当带上状态码与上游原话，实际：%v", err)
	}
}

func TestHTTPTransportRejectsNonHTTPURL(t *testing.T) {
	if _, err := Start(context.Background(), Config{URL: "ftp://example.com/mcp"}); err == nil {
		t.Fatal("非 http(s) 地址应当被拒绝")
	}
}

func TestTransportInferredFromConfig(t *testing.T) {
	// 填了 URL 就走 HTTP，不该去当成命令执行。
	fake := &fakeHTTPServer{}
	client, err := Start(context.Background(), Config{URL: startFake(t, fake), Command: "不该被执行"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	if methods := fake.methods(); len(methods) == 0 || methods[0] != "initialize" {
		t.Errorf("应当走 HTTP 并先 initialize，实际 %v", methods)
	}
}

func TestHTTPTransportRespectsContextCancel(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(func() {
		close(release)
		server.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := Start(ctx, Config{URL: server.URL, StartupTimeout: time.Minute}); err == nil {
		t.Fatal("挂住的服务端应当因为 ctx 取消而失败")
	}
	// 用的是 ctx 而不是 StartupTimeout，所以应当很快返回。
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("取消没有及时生效，耗时 %v", elapsed)
	}
}
