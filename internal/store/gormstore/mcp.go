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

func (s *GormStore) UpsertMCPServer(ctx context.Context, server *model.MCPServer) error {
	if server.UUID == "" {
		server.UUID = uuid.NewString()
	}
	var existing model.MCPServer
	result := s.db.WithContext(ctx).Where("uuid = ?", server.UUID).First(&existing)
	if result.Error == nil {
		server.ID = existing.ID
		return s.db.WithContext(ctx).Save(server).Error
	}
	if result.Error != gorm.ErrRecordNotFound {
		return result.Error
	}
	return s.db.WithContext(ctx).Create(server).Error
}

func (s *GormStore) SetMCPServerEnabled(ctx context.Context, serverUUID string, enabled bool) error {
	return s.db.WithContext(ctx).Model(&model.MCPServer{}).Where("uuid = ?", serverUUID).Update("enabled", enabled).Error
}

func (s *GormStore) SetPluginMCPEnabled(ctx context.Context, pluginUUID string, enabled bool) error {
	return s.db.WithContext(ctx).Model(&model.MCPServer{}).Where("plugin_uuid = ?", pluginUUID).Update("enabled", enabled).Error
}

func (s *GormStore) DeletePluginMCP(ctx context.Context, pluginUUID string) error {
	return s.db.WithContext(ctx).Where("plugin_uuid = ?", pluginUUID).Delete(&model.MCPServer{}).Error
}
