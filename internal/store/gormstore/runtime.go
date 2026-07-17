package gormstore

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/chowyu12/aiclaw/internal/model"
)

func (s *GormStore) CreateRuntime(ctx context.Context, runtime *model.Runtime) error {
	if runtime.UUID == "" {
		runtime.UUID = uuid.NewString()
	}
	if runtime.Token == "" {
		runtime.Token = newRuntimeToken()
	}
	if runtime.Status == "" {
		runtime.Status = model.RuntimeStatusOffline
	}
	if agentType, ok := model.NormalizeRuntimeAgentType(runtime.AgentType); ok {
		runtime.AgentType = agentType
	} else {
		runtime.AgentType = model.RuntimeAgentTypeCustom
	}
	runtime.PromptMode = runtime.EffectivePromptMode()
	return s.db.WithContext(ctx).Create(runtime).Error
}

func (s *GormStore) GetRuntime(ctx context.Context, id int64) (*model.Runtime, error) {
	var runtime model.Runtime
	if err := s.db.WithContext(ctx).First(&runtime, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &runtime, nil
}

func (s *GormStore) GetRuntimeByToken(ctx context.Context, token string) (*model.Runtime, error) {
	var runtime model.Runtime
	if err := s.db.WithContext(ctx).Where("token = ?", token).First(&runtime).Error; err != nil {
		return nil, notFound(err)
	}
	return &runtime, nil
}

func (s *GormStore) ListRuntimes(ctx context.Context, q model.ListQuery) ([]*model.Runtime, int64, error) {
	var items []*model.Runtime
	var total int64
	db := s.db.WithContext(ctx).Model(&model.Runtime{})
	if q.Keyword != "" {
		kw := "%" + q.Keyword + "%"
		db = db.Where("name LIKE ? OR description LIKE ?", kw, kw)
	}
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(q)
	if err := db.Order("id ASC").Offset(offset).Limit(limit).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (s *GormStore) UpdateRuntime(ctx context.Context, id int64, req model.UpdateRuntimeReq) error {
	updates := map[string]any{}
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.AgentType != nil {
		updates["agent_type"] = *req.AgentType
	}
	if req.Command != nil {
		updates["command"] = *req.Command
	}
	if req.Args != nil {
		updates["args"] = model.StringSlice(*req.Args)
	}
	if req.PromptMode != nil {
		updates["prompt_mode"] = *req.PromptMode
	}
	if len(updates) == 0 {
		return nil
	}
	res := s.db.WithContext(ctx).Model(&model.Runtime{}).Where("id = ?", id).Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *GormStore) TouchRuntime(ctx context.Context, id int64, version string, seenAt time.Time) error {
	res := s.db.WithContext(ctx).Model(&model.Runtime{}).Where("id = ?", id).Updates(map[string]any{
		"status":       model.RuntimeStatusOnline,
		"version":      strings.TrimSpace(version),
		"last_seen_at": seenAt,
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *GormStore) ResetRuntimeToken(ctx context.Context, id int64) (string, error) {
	token := newRuntimeToken()
	res := s.db.WithContext(ctx).Model(&model.Runtime{}).Where("id = ?", id).Update("token", token)
	if res.Error != nil {
		return "", res.Error
	}
	if res.RowsAffected == 0 {
		return "", sql.ErrNoRows
	}
	return token, nil
}

func (s *GormStore) DeleteRuntime(ctx context.Context, id int64) error {
	res := s.db.WithContext(ctx).Delete(&model.Runtime{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func newRuntimeToken() string {
	return "rt-" + strings.ReplaceAll(uuid.NewString(), "-", "")
}
