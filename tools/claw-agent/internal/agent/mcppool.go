package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/mcpclient"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

/*
MCP 连接池：同一份配置的 server 在会话之间共用一条连接。

**为什么需要它。** 挂载不是「起个进程」那么便宜：claw-mcp 在启动时要问内部平台
「这个库里有哪些 API、每个的运行契约长什么样」，五个能力加起来是二十来次串行
HTTPS。每开一个会话重来一遍，表现就是切会话卡一两秒；而且每个会话都留着自己的
五个子进程——开四个会话就是二十个 claw-mcp 挂在那儿（实测）。

**为什么可以共用。** 客户端只是一条连接，工具的审批与策略是**按会话**在注册表
里决定的，共用连接不会把一个会话的策略带给另一个。mcpclient 的两种传输都是
并发安全的（stdio 有 writeMu + pending 表，HTTP 每次请求独立）。

**为什么还要 TTL。** 工具清单是 Start 那一刻拉的。一直复用下去的话，
「库里新增了 API，下次开会话自动带上」这条就不成立了——那是刻意设计的行为
（见 internal/provider 的 expand）。所以过了 reuseTTL 且没人在用的连接会被
关掉重起，把「切会话要快」和「新增的东西能自动出现」这两件事都保住。
*/

// reuseTTL 是一条连接被复用的时限。
//
// 五分钟是按「一次连续的工作」估的：这段时间里来回切会话不该重连，
// 而隔了半小时回来，内部平台那边可能已经不一样了。
const reuseTTL = 5 * time.Minute

type pooledEntry struct {
	client    *mcpclient.Client
	err       error
	refs      int
	startedAt time.Time
	// ready 在握手结束后关闭。先占位再握手，两个会话同时挂同一个 server 时
	// 后来的那个等在这儿，而不是各起一个。
	ready chan struct{}
}

type mcpPool struct {
	mu      sync.Mutex
	entries map[string]*pooledEntry
	now     func() time.Time
}

func newMCPPool() *mcpPool {
	return &mcpPool{entries: map[string]*pooledEntry{}, now: time.Now}
}

// mcpShared 是进程级的池。一个内核进程一个，与会话的生命周期无关。
var mcpShared = newMCPPool()

// fingerprint 认「同一个 server」：名字 + 完整配置。
//
// 配置里任何一个字段变了（换了地址、加了请求头、改了实例配置路径）都算另一个
// server——共用一条连着旧配置的连接，表现会是「改了配置没生效」，而那正是
// 上一版刚修过的那类 bug。
func fingerprint(name string, config protocol.MCPServerConfig) string {
	encoded, err := json.Marshal(struct {
		Name   string
		Config protocol.MCPServerConfig
	}{name, config})
	if err != nil {
		// 编不出来就退回一个必然唯一的值：宁可不复用，也不要错误地复用。
		return name + ":" + time.Now().String()
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// acquire 取一条可用的连接，必要时现起一条。返回的 key 用于归还。
func (p *mcpPool) acquire(
	ctx context.Context,
	name string,
	config protocol.MCPServerConfig,
) (*mcpclient.Client, string, error) {
	key := fingerprint(name, config)

	p.mu.Lock()
	if entry, ok := p.entries[key]; ok {
		stale := p.now().Sub(entry.startedAt) > reuseTTL
		if !stale || entry.refs > 0 {
			// 过期但还有人在用的照旧共用：为了让清单新鲜而把别人的连接拔掉，
			// 换来的是那个会话的工具突然失效。
			entry.refs++
			p.mu.Unlock()
			<-entry.ready
			if entry.err != nil {
				p.release(key)
				return nil, "", entry.err
			}
			return entry.client, key, nil
		}
		// 过期且没人用：关掉重起，让新增的 API 能被带出来。
		delete(p.entries, key)
		go entry.close()
	}

	entry := &pooledEntry{refs: 1, startedAt: p.now(), ready: make(chan struct{})}
	p.entries[key] = entry
	p.mu.Unlock()

	client, err := mcpclient.Start(ctx, mcpclient.Config{
		Command: config.Command,
		Args:    config.Args,
		Env:     config.Env,
		URL:     config.URL,
		Headers: config.Headers,
	})
	entry.client, entry.err = client, err
	close(entry.ready)

	if err != nil {
		// 失败不留在池里：下一个会话应该重试，而不是拿到同一个错误。
		p.mu.Lock()
		if p.entries[key] == entry {
			delete(p.entries, key)
		}
		p.mu.Unlock()
		return nil, "", err
	}
	return client, key, nil
}

// release 归还一条连接。最后一个用完的负责关掉它。
func (p *mcpPool) release(key string) {
	p.mu.Lock()
	entry, ok := p.entries[key]
	if !ok {
		p.mu.Unlock()
		return
	}
	entry.refs--
	if entry.refs > 0 {
		p.mu.Unlock()
		return
	}
	delete(p.entries, key)
	p.mu.Unlock()
	entry.close()
}

func (e *pooledEntry) close() {
	<-e.ready
	if e.client != nil {
		e.client.Close()
	}
}
