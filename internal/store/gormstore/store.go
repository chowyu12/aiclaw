package gormstore

import (
	"database/sql"
	"errors"
	"fmt"

	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	mysqlDriver "gorm.io/driver/mysql"
	pgDriver "gorm.io/driver/postgres"
	sqliteDriver "github.com/glebarez/sqlite"

	"github.com/chowyu12/aiclaw/internal/config"
	"github.com/chowyu12/aiclaw/internal/model"
)

type GormStore struct {
	db *gorm.DB
}

func New(cfg config.DatabaseConfig) (*GormStore, error) {
	var dialector gorm.Dialector
	switch cfg.Driver {
	case "mysql":
		dialector = mysqlDriver.Open(cfg.DSN)
	case "postgres":
		dialector = pgDriver.Open(cfg.DSN)
	case "sqlite":
		dialector = sqliteDriver.Open(cfg.DSN)
	default:
		return nil, fmt.Errorf("unsupported database driver: %s", cfg.Driver)
	}

	db, err := gorm.Open(dialector, &gorm.Config{
		Logger:                 logger.Default.LogMode(logger.Warn),
		SkipDefaultTransaction: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get underlying db: %w", err)
	}
	if cfg.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	}

	if cfg.AutoMigrate == nil || *cfg.AutoMigrate {
		if err := autoMigrate(db); err != nil {
			return nil, fmt.Errorf("auto migrate: %w", err)
		}
		log.WithField("driver", cfg.Driver).Info("database connected and migrated")
	} else {
		log.WithField("driver", cfg.Driver).Info("database connected (auto_migrate disabled)")
	}

	return &GormStore{db: db}, nil
}

func autoMigrate(db *gorm.DB) error {
	if err := db.AutoMigrate(
		&model.Agent{},
		&model.Provider{},
		&model.Tool{},
		&model.Channel{},
		&model.ChannelThread{},
		&model.MCPServer{},
		&model.Conversation{},
		&model.Message{},
		&model.ExecutionStep{},
		&model.File{},
	); err != nil {
		return err
	}
	return dropDeprecatedColumns(db)
}

// dropDeprecatedColumns 清理历史遗留列（AutoMigrate 默认不会 drop 已删除字段对应的列）。
// 这里显式列出所有已废弃的列名，在各表上容错地执行 drop。
func dropDeprecatedColumns(db *gorm.DB) error {
	deprecated := map[string][]string{
		"agents": {"memos_enabled", "memos_config"},
	}
	m := db.Migrator()
	for table, cols := range deprecated {
		if !m.HasTable(table) {
			continue
		}
		for _, col := range cols {
			if !m.HasColumn(table, col) {
				continue
			}
			if err := m.DropColumn(table, col); err != nil {
				log.WithFields(log.Fields{"table": table, "column": col, "error": err}).
					Warn("drop deprecated column failed")
			} else {
				log.WithFields(log.Fields{"table": table, "column": col}).
					Info("dropped deprecated column")
			}
		}
	}
	return nil
}

func TestConnection(cfg config.DatabaseConfig) error {
	var dialector gorm.Dialector
	switch cfg.Driver {
	case "mysql":
		dialector = mysqlDriver.Open(cfg.DSN)
	case "postgres":
		dialector = pgDriver.Open(cfg.DSN)
	case "sqlite":
		dialector = sqliteDriver.Open(cfg.DSN)
	default:
		return fmt.Errorf("unsupported database driver: %s", cfg.Driver)
	}

	db, err := gorm.Open(dialector, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get underlying db: %w", err)
	}
	defer sqlDB.Close()
	return sqlDB.Ping()
}

func (s *GormStore) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func notFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return sql.ErrNoRows
	}
	return err
}

func paginate(q model.ListQuery) (offset, limit int) {
	page := q.Page
	if page < 1 {
		page = 1
	}
	size := q.PageSize
	if size < 1 {
		size = 20
	}
	return (page - 1) * size, size
}

func isValidSQLIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_') {
			return false
		}
	}
	return true
}

func setRelation(tx *gorm.DB, table, col1, col2 string, id int64, relIDs []int64) error {
	if !isValidSQLIdentifier(table) || !isValidSQLIdentifier(col1) || !isValidSQLIdentifier(col2) {
		return fmt.Errorf("invalid SQL identifier in setRelation: table=%q col1=%q col2=%q", table, col1, col2)
	}
	if err := tx.Exec("DELETE FROM "+table+" WHERE "+col1+" = ?", id).Error; err != nil {
		return err
	}
	for _, rid := range relIDs {
		if err := tx.Exec("INSERT INTO "+table+" ("+col1+", "+col2+") VALUES (?, ?)", id, rid).Error; err != nil {
			return err
		}
	}
	return nil
}
