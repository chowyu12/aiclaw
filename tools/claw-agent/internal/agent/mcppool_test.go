package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

// MCP 连接池。
//
// 它存在的理由是可测的那一条：**同一份配置只握手一次**。挂载不是「起个进程」
// 那么便宜——claw-mcp 启动时要把「库里有哪些 API、契约长什么样」向内部平台问一遍，
// 五个能力二十来次串行 HTTPS，每开一个会话重来一遍就是切会话卡一两秒。

// countingMCP 是一个最小 MCP server，记下自己被握手了几次。
func countingMCP(t *testing.T, handshakes *atomic.Int64) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var message struct {
			ID     *int64 `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&message)
		if message.Method == "initialize" {
			handshakes.Add(1)
		}
		if message.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		result := `{}`
		switch message.Method {
		case "initialize":
			result = `{"protocolVersion":"2025-03-26","capabilities":{},"serverInfo":{"name":"x","version":"1"}}`
		case "tools/list":
			result = `{"tools":[{"name":"ping","description":"","inputSchema":{"type":"object"}}]}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":%s}`, *message.ID, result)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func TestPoolSharesOneConnectionAcrossSessions(t *testing.T) {
	var handshakes atomic.Int64
	url := countingMCP(t, &handshakes)
	pool := newMCPPool()
	config := protocol.MCPServerConfig{URL: url}

	var keys []string
	for i := 0; i < 3; i++ {
		client, key, err := pool.acquire(context.Background(), "x", config)
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		if len(client.Tools()) != 1 {
			t.Errorf("第 %d 次拿到的连接没有工具", i)
		}
		keys = append(keys, key)
	}
	if got := handshakes.Load(); got != 1 {
		t.Errorf("同一份配置应当只握手一次，实际 %d 次", got)
	}

	// 全部归还之后连接才真正关掉；再拿就是一条新的。
	for _, key := range keys {
		pool.release(key)
	}
	if _, _, err := pool.acquire(context.Background(), "x", config); err != nil {
		t.Fatalf("重新 acquire: %v", err)
	}
	if got := handshakes.Load(); got != 2 {
		t.Errorf("全部归还后应当重新握手，实际握手 %d 次", got)
	}
}

func TestPoolTreatsDifferentConfigAsDifferentServer(t *testing.T) {
	// 配置变了还共用旧连接，表现会是「改了配置没生效」——上一版刚修过那类 bug。
	var handshakes atomic.Int64
	url := countingMCP(t, &handshakes)
	pool := newMCPPool()

	if _, _, err := pool.acquire(context.Background(), "x", protocol.MCPServerConfig{URL: url}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := pool.acquire(context.Background(), "x", protocol.MCPServerConfig{
		URL: url, Headers: map[string]string{"Authorization": "Bearer 新的"},
	}); err != nil {
		t.Fatal(err)
	}
	if got := handshakes.Load(); got != 2 {
		t.Errorf("配置不同应当各起一条，实际握手 %d 次", got)
	}
}

func TestPoolRestartsAfterTTLWhenIdle(t *testing.T) {
	// 工具清单是握手那一刻拉的。一直复用下去，「库里新增了 API，下次开会话
	// 自动带上」这条就不成立了——那是刻意设计的行为。
	var handshakes atomic.Int64
	url := countingMCP(t, &handshakes)
	pool := newMCPPool()
	now := time.Now()
	pool.now = func() time.Time { return now }
	config := protocol.MCPServerConfig{URL: url}

	_, key, err := pool.acquire(context.Background(), "x", config)
	if err != nil {
		t.Fatal(err)
	}
	pool.release(key)

	now = now.Add(reuseTTL + time.Minute)
	if _, _, err := pool.acquire(context.Background(), "x", config); err != nil {
		t.Fatal(err)
	}
	if got := handshakes.Load(); got != 2 {
		t.Errorf("过了复用时限应当重新握手，实际 %d 次", got)
	}
}

func TestPoolKeepsSharingWhileStillInUse(t *testing.T) {
	// 过期了但还有会话在用：不能为了清单新鲜把别人的连接拔掉——
	// 那个会话的工具会突然失效。
	var handshakes atomic.Int64
	url := countingMCP(t, &handshakes)
	pool := newMCPPool()
	now := time.Now()
	pool.now = func() time.Time { return now }
	config := protocol.MCPServerConfig{URL: url}

	if _, _, err := pool.acquire(context.Background(), "x", config); err != nil {
		t.Fatal(err)
	}
	now = now.Add(reuseTTL + time.Minute)
	if _, _, err := pool.acquire(context.Background(), "x", config); err != nil {
		t.Fatal(err)
	}
	if got := handshakes.Load(); got != 1 {
		t.Errorf("还有人在用时应当继续共用，实际握手 %d 次", got)
	}
}

func TestPoolDoesNotCacheFailures(t *testing.T) {
	// 起失败的不留在池里：下一个会话该重试，而不是拿到同一个错误。
	pool := newMCPPool()
	config := protocol.MCPServerConfig{URL: "http://127.0.0.1:1/mcp"}
	if _, _, err := pool.acquire(context.Background(), "x", config); err == nil {
		t.Fatal("连不上应当报错")
	}
	pool.mu.Lock()
	size := len(pool.entries)
	pool.mu.Unlock()
	if size != 0 {
		t.Errorf("失败的连接不该留在池里，实际还剩 %d 条", size)
	}
}
