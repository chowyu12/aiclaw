package gormstore

import (
	"context"
	"database/sql"
	"strings"

	"github.com/google/uuid"

	"github.com/chowyu12/aiclaw/internal/model"
)

func (s *GormStore) CreateAgent(ctx context.Context, a *model.Agent) error {
	if a.UUID == "" {
		a.UUID = uuid.New().String()
	}
	if a.Token == "" {
		a.Token = "ag-" + strings.ReplaceAll(uuid.New().String(), "-", "")
	}
	return s.db.WithContext(ctx).Create(a).Error
}

func (s *GormStore) GetAgent(ctx context.Context, id int64) (*model.Agent, error) {
	var a model.Agent
	if err := s.db.WithContext(ctx).First(&a, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &a, nil
}

func (s *GormStore) GetAgentByUUID(ctx context.Context, u string) (*model.Agent, error) {
	var a model.Agent
	if err := s.db.WithContext(ctx).Where("uuid = ?", u).First(&a).Error; err != nil {
		return nil, notFound(err)
	}
	return &a, nil
}

func (s *GormStore) GetAgentByToken(ctx context.Context, token string) (*model.Agent, error) {
	var a model.Agent
	if err := s.db.WithContext(ctx).Where("token = ?", token).First(&a).Error; err != nil {
		return nil, notFound(err)
	}
	return &a, nil
}

func (s *GormStore) GetDefaultAgent(ctx context.Context) (*model.Agent, error) {
	var a model.Agent
	if err := s.db.WithContext(ctx).Where("is_default = ?", true).First(&a).Error; err != nil {
		// fallback: return any agent ordered by id
		if err2 := s.db.WithContext(ctx).Order("id ASC").First(&a).Error; err2 != nil {
			return nil, notFound(err2)
		}
		return &a, nil
	}
	return &a, nil
}

func (s *GormStore) ListAgents(ctx context.Context, q model.ListQuery) ([]*model.Agent, int64, error) {
	var items []*model.Agent
	var total int64
	db := s.db.WithContext(ctx).Model(&model.Agent{})
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

func (s *GormStore) UpdateAgent(ctx context.Context, id int64, req *model.UpdateAgentReq) error {
	updates := map[string]any{}
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.SystemPrompt != nil {
		updates["system_prompt"] = *req.SystemPrompt
	}
	if req.ProviderID != nil {
		updates["provider_id"] = *req.ProviderID
	}
	if req.ModelName != nil {
		updates["model_name"] = *req.ModelName
	}
	if req.Temperature != nil {
		updates["temperature"] = *req.Temperature
	}
	if req.MaxTokens != nil {
		updates["max_tokens"] = *req.MaxTokens
	}
	if req.Timeout != nil {
		updates["timeout"] = *req.Timeout
	}
	if req.MaxHistory != nil {
		updates["max_history"] = *req.MaxHistory
	}
	if req.MaxIterations != nil {
		updates["max_iterations"] = *req.MaxIterations
	}
	if req.EnableThinking != nil {
		updates["enable_thinking"] = *req.EnableThinking
	}
	if req.ReasoningEffort != nil {
		updates["reasoning_effort"] = *req.ReasoningEffort
	}
	if req.EnableWebSearch != nil {
		updates["enable_web_search"] = *req.EnableWebSearch
	}
	if req.ToolSearchEnabled != nil {
		updates["tool_search_enabled"] = *req.ToolSearchEnabled
	}
	if req.ToolIDs != nil {
		updates["tool_ids"] = model.Int64Slice(req.ToolIDs)
	}
	if req.IsDefault != nil {
		updates["is_default"] = *req.IsDefault
	}
	if len(updates) == 0 {
		return nil
	}
	// 若将此 Agent 设为默认，先清除其他 Agent 的默认标记
	if v, ok := updates["is_default"]; ok && v == true {
		if err := s.db.WithContext(ctx).Model(&model.Agent{}).Where("id <> ?", id).Update("is_default", false).Error; err != nil {
			return err
		}
	}
	return s.db.WithContext(ctx).Model(&model.Agent{}).Where("id = ?", id).Updates(updates).Error
}

func (s *GormStore) ResetAgentToken(ctx context.Context, id int64) (string, error) {
	newToken := "ag-" + strings.ReplaceAll(uuid.New().String(), "-", "")
	if err := s.db.WithContext(ctx).Model(&model.Agent{}).Where("id = ?", id).Update("token", newToken).Error; err != nil {
		return "", err
	}
	return newToken, nil
}

func (s *GormStore) DeleteAgent(ctx context.Context, id int64) error {
	res := s.db.WithContext(ctx).Delete(&model.Agent{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return sql.ErrNoRows
	}
	return nil
}
