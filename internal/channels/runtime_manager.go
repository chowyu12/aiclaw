package channels

import (
	"context"
	"sync"

	agentpkg "github.com/chowyu12/aiclaw/internal/agent"
	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/store"
)

var (
	runtimeMu     sync.RWMutex
	runtimeStore  store.Store
	runtimeBridge *Bridge
)

// InitChannelRuntimes 初始化渠道运行时（main 启动调用）。
func InitChannelRuntimes(s store.Store, exec *agentpkg.Executor) {
	runtimeMu.Lock()
	runtimeStore = s
	runtimeBridge = NewBridge(s, exec)
	runtimeMu.Unlock()
	RefreshChannelRuntimes(context.Background())
}

// RefreshChannelRuntimes 在渠道配置变化后刷新各驱动运行状态。
func RefreshChannelRuntimes(ctx context.Context) {
	runtimeMu.RLock()
	s := runtimeStore
	b := runtimeBridge
	runtimeMu.RUnlock()
	if s == nil {
		return
	}
	list, _, err := s.ListChannels(ctx, model.ListQuery{Page: 1, PageSize: 500})
	if err != nil {
		return
	}
	for _, d := range allDrivers() {
		d.Refresh(ctx, list, b)
	}
}

// StopChannelRuntimes 停止所有渠道运行时（main 退出前调用）。
func StopChannelRuntimes() {
	for _, d := range allDrivers() {
		d.Stop()
	}
}
