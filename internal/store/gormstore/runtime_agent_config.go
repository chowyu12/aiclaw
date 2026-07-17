package gormstore

import (
	"context"
	"database/sql"
	"strings"

	"gorm.io/gorm/clause"

	"github.com/chowyu12/aiclaw/internal/model"
)

func (s *GormStore) EnsureRuntimeAgentConfigs(ctx context.Context, runtimeID int64, agentTypes []string) error {
	for _, rawType := range agentTypes {
		agentType, ok := model.NormalizeRuntimeAgentType(rawType)
		if !ok || agentType == model.RuntimeAgentTypeCustom {
			continue
		}
		config := model.RuntimeAgentConfig{RuntimeID: runtimeID, AgentType: agentType, Enabled: true}
		if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "runtime_id"}, {Name: "agent_type"}},
			DoNothing: true,
		}).Create(&config).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *GormStore) ListRuntimeAgentConfigs(ctx context.Context, runtimeID int64) ([]model.RuntimeAgentConfig, error) {
	var items []model.RuntimeAgentConfig
	if err := s.db.WithContext(ctx).Where("runtime_id = ?", runtimeID).Order("id ASC").Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

func (s *GormStore) GetRuntimeAgentConfig(ctx context.Context, runtimeID int64, agentType string) (*model.RuntimeAgentConfig, error) {
	var config model.RuntimeAgentConfig
	if err := s.db.WithContext(ctx).Where("runtime_id = ? AND agent_type = ?", runtimeID, agentType).First(&config).Error; err != nil {
		return nil, notFound(err)
	}
	return &config, nil
}

func (s *GormStore) UpdateRuntimeAgentConfig(ctx context.Context, runtimeID int64, agentType string, req model.UpdateRuntimeAgentConfigReq) error {
	updates := map[string]any{}
	if req.Enabled != nil {
		updates["enabled"] = *req.Enabled
	}
	if req.ModelName != nil {
		updates["model_name"] = strings.TrimSpace(*req.ModelName)
	}
	if len(updates) == 0 {
		return nil
	}
	res := s.db.WithContext(ctx).Model(&model.RuntimeAgentConfig{}).
		Where("runtime_id = ? AND agent_type = ?", runtimeID, agentType).
		Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return sql.ErrNoRows
	}
	return nil
}
