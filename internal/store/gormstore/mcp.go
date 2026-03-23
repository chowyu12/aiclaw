package gormstore

import (
	"context"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/chowyu12/aiclaw/internal/model"
)

func (s *GormStore) ListMCPServers(ctx context.Context) ([]model.MCPServer, error) {
	var list []model.MCPServer
	if err := s.db.WithContext(ctx).Order("id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// ReplaceMCPServers 全量替换 MCP 列表（与前端 PUT 一致）。
func (s *GormStore) ReplaceMCPServers(ctx context.Context, servers []model.MCPServer) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.MCPServer{}).Error; err != nil {
			return err
		}
		for i := range servers {
			if servers[i].UUID == "" {
				servers[i].UUID = uuid.New().String()
			}
			servers[i].ID = 0
			if err := tx.Create(&servers[i]).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
