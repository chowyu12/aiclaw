package config

import (
	"errors"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
	"io/fs"
	"os"
	"path/filepath"
)

func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "etc/config.yaml"
	}
	return filepath.Join(home, ".aiclaw", "config.yaml")
}

func ConfigPath(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	p := DefaultConfigPath()
	if _, err := os.Stat(p); err == nil {
		return p
	}
	if _, err := os.Stat("etc/config.yaml"); err == nil {
		log.WithField("path", "etc/config.yaml").Info("using legacy config path")
		return "etc/config.yaml"
	}
	return p
}

type Config struct {
	Workspace string         `yaml:"workspace,omitempty"`
	Database  DatabaseConfig `yaml:"database,omitempty"`
	Log       LogConfig      `yaml:"log,omitempty"`
}

type DatabaseConfig struct {
	Driver       string `yaml:"driver,omitempty"`
	DSN          string `yaml:"dsn,omitempty"`
	MaxOpenConns int    `yaml:"max_open_conns,omitempty"`
	MaxIdleConns int    `yaml:"max_idle_conns,omitempty"`
	AutoMigrate  *bool  `yaml:"auto_migrate,omitempty"`
}

type LogConfig struct {
	Level   string `yaml:"level,omitempty"`
	File    string `yaml:"file,omitempty"`
	MaxSize int    `yaml:"max_size,omitempty"`
}

func Load(path string) (*Config, error) {
	var cfg Config
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	} else {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, err
		}
	}
	setDefaults(&cfg)
	return &cfg, nil
}

func (c *Config) NeedsDatabaseSetup() bool {
	return c.Database.Driver == "" || c.Database.DSN == ""
}

func (c *Config) Save(path string) error {
	MarkSavingYAML()
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	out := *c
	data, err := yaml.Marshal(&out)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func setDefaults(cfg *Config) {
	if cfg.Database.Driver == "sqlite" && cfg.Database.DSN == "" {
		if home, err := os.UserHomeDir(); err == nil {
			cfg.Database.DSN = filepath.Join(home, ".aiclaw", "aiclaw.db")
		}
	}
	if cfg.Database.MaxOpenConns == 0 {
		cfg.Database.MaxOpenConns = 25
	}
	if cfg.Database.MaxIdleConns == 0 {
		cfg.Database.MaxIdleConns = 10
	}
	if cfg.Log.MaxSize == 0 {
		cfg.Log.MaxSize = 10 // 10MB
	}
}
