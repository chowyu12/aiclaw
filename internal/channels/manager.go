package channels

import (
	"cmp"
	"context"
	"slices"

	log "github.com/sirupsen/logrus"

	agentpkg "github.com/chowyu12/aiclaw/internal/agent"
	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/store"
)

// Manager 管理所有渠道的运行时生命周期和消息桥接（唯一实例）。
type Manager struct {
	store  store.Store
	bridge *Bridge
}

// NewManager 创建 Manager 并执行首次 Refresh。
func NewManager(s store.Store, exec *agentpkg.Executor) *Manager {
	m := &Manager{
		store:  s,
		bridge: newBridge(s, exec),
	}
	m.Refresh(context.Background())
	return m
}

// Bridge 返回唯一的消息桥接实例（供 Webhook Handler 使用同一 Bridge）。
func (m *Manager) Bridge() *Bridge { return m.bridge }

// Refresh 从数据库拉取全量渠道，按类型分发给各运行时驱动。
func (m *Manager) Refresh(ctx context.Context) {
	list, _, err := m.store.ListChannels(ctx, model.ListQuery{Page: 1, PageSize: 500})
	if err != nil {
		log.WithError(err).Error("[channels] Refresh: ListChannels failed")
		return
	}
	for t, d := range runtimeDrivers {
		sub := make([]*model.Channel, 0, len(list))
		for _, ch := range list {
			if ch != nil && ch.ChannelType == t {
				sub = append(sub, ch)
			}
		}
		slices.SortFunc(sub, func(a, b *model.Channel) int {
			return cmp.Compare(a.ID, b.ID)
		})
		d.Refresh(ctx, sub, m.bridge)
	}
}

// Stop 停止所有运行时驱动（进程退出前调用）。
func (m *Manager) Stop() {
	for _, d := range runtimeDrivers {
		d.Stop()
	}
}
