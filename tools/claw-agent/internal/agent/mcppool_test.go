package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
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

// 握手要并发，不能一个接一个。
//
// 一个公网 MCP server 光 initialize 就要几秒，而每开一次会话、每切一次会话都要
// 重来一遍（连接池只在 TTL 内帮得上忙）。三四个 server 串行起来就是十秒级的卡顿，
// 而这些握手互不依赖。这条用「每个 server 各慢 300ms」来钉住：串行要 900ms 以上，
// 并发在 600ms 以内。
func TestMCPServersAreDialedConcurrently(t *testing.T) {
	const servers = 3
	const delay = 300 * time.Millisecond

	config := map[string]protocol.MCPServerConfig{}
	for index := range servers {
		name := fmt.Sprintf("slow%d", index)
		config[name] = protocol.MCPServerConfig{URL: slowMCP(t, name, delay)}
	}

	model := &fakeModel{}
	upstream := httptest.NewServer(http.HandlerFunc(model.handler))
	t.Cleanup(upstream.Close)

	started := time.Now()
	session, err := New(context.Background(), "test", protocol.SessionStartParams{
		Model:      protocol.ModelConfig{BaseURL: upstream.URL, Model: "fake"},
		Workdir:    t.TempDir(),
		MCPServers: config,
	}, StaticKey("sk-test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(session.Close)
	elapsed := time.Since(started)

	if elapsed >= time.Duration(servers)*delay {
		t.Errorf("挂载耗时 %v，看起来仍然是串行（串行约 %v）", elapsed, time.Duration(servers)*delay)
	}
	// 工具顺序仍要稳定：注册那一半是串行且按名字排的。
	var mounted []string
	for _, name := range session.Tools() {
		if strings.HasPrefix(name, "slow") {
			mounted = append(mounted, name)
		}
	}
	want := []string{"slow0__tool_slow0", "slow1__tool_slow1", "slow2__tool_slow2"}
	if !reflect.DeepEqual(mounted, want) {
		t.Errorf("工具顺序应当稳定按名字排：%v", mounted)
	}
}

// slowMCP 是一个每次回应前先睡一会儿的 MCP server。
func slowMCP(t *testing.T, name string, delay time.Duration) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var message struct {
			ID     *int64 `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&message)
		if message.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		// 只在握手的第一步慢：模拟远端建连接的代价。
		if message.Method == "initialize" {
			time.Sleep(delay)
		}
		result := `{}`
		switch message.Method {
		case "initialize":
			result = `{"protocolVersion":"2025-03-26","capabilities":{},"serverInfo":{"name":"slow","version":"1"}}`
		case "tools/list":
			result = fmt.Sprintf(
				`{"tools":[{"name":"tool_%s","description":"假的","inputSchema":{"type":"object"}}]}`, name)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":%s}`, *message.ID, result)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// 并发之后总耗时等于最慢的那一个，只报总数说不出是谁——实测出现过一次 14.7 秒
// 而日志里无从归因。每个 server 的耗时要单独记下来，并按耗时倒序。
func TestSlowServerIsNamed(t *testing.T) {
	config := map[string]protocol.MCPServerConfig{
		"fast": {URL: slowMCP(t, "fast", 0)},
		"slow": {URL: slowMCP(t, "slow", 400*time.Millisecond)},
	}
	model := &fakeModel{}
	upstream := httptest.NewServer(http.HandlerFunc(model.handler))
	t.Cleanup(upstream.Close)

	session, err := New(context.Background(), "test", protocol.SessionStartParams{
		Model:      protocol.ModelConfig{BaseURL: upstream.URL, Model: "fake"},
		Workdir:    t.TempDir(),
		MCPServers: config,
	}, StaticKey("sk-test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(session.Close)

	dials := session.MCPDials()
	if len(dials) != 2 {
		t.Fatalf("两个 server 应各记一条：%+v", dials)
	}
	if dials[0].Name != "slow" {
		t.Errorf("最慢的要排在最前面，方便一眼看出是谁：%+v", dials)
	}
	if dials[0].Took < 400*time.Millisecond {
		t.Errorf("慢的那个耗时没记对：%v", dials[0].Took)
	}
	if dials[1].Took > dials[0].Took {
		t.Errorf("应按耗时倒序：%+v", dials)
	}
}
