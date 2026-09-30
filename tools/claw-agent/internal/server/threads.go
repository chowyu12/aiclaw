package server

import (
	"context"

	"github.com/chowyu12/aiclaw/internal/i18n"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/agent"
)

// threadSource 实现 agent.ThreadSource：会话在内存里就读内存（更新、也知道它此刻在不在跑），
// 不在就读存档。归档了的也能读——用户引用它，就是想让模型看。
type threadSource struct {
	server *Server
}

func (t threadSource) ReadThread(ctx context.Context, id string) (agent.ThreadSnapshot, error) {
	if session := t.server.session(id); session != nil {
		return session.Snapshot(), nil
	}
	record, err := t.server.db.Load(ctx, id)
	if err != nil {
		return agent.ThreadSnapshot{}, i18n.E("没有这个会话（{id}）：可能已经删了", "id", id)
	}
	archived := false
	if list, err := agent.ListArchived(ctx, t.server.db); err == nil {
		for _, item := range list {
			if item.ID == id {
				archived = true
				break
			}
		}
	}
	return agent.SnapshotFromRecord(record, archived)
}

func (t threadSource) ListThreads(ctx context.Context, limit int) ([]agent.ThreadSummary, error) {
	summaries, err := agent.List(ctx, t.server.db)
	if err != nil {
		return nil, err
	}
	if summaries == nil {
		return nil, i18n.E("读不到会话列表")
	}
	result := make([]agent.ThreadSummary, 0, limit)
	for _, item := range summaries {
		if len(result) == limit {
			break
		}
		result = append(result, agent.ThreadSummary{
			ID: item.ID, Title: item.Title, UpdatedAt: item.UpdatedAt, TurnCount: item.TurnCount, ParentID: item.ParentID,
		})
	}
	return result, nil
}
