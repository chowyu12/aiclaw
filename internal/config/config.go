package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
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
	Server    ServerConfig   `yaml:"server,omitempty"`
	Database  DatabaseConfig `yaml:"database,omitempty"`
	Log       LogConfig      `yaml:"log,omitempty"`
	Auth      AuthConfig     `yaml:"auth,omitempty"`
	Upload    UploadConfig   `yaml:"upload,omitempty"`
	Browser   BrowserConfig  `yaml:"browser,omitempty"`
}

// AuthConfig Web 控制台访问令牌：在配置中设置；登录校验通过后前端以 Bearer 方式携带同一令牌访问 API。
type AuthConfig struct {
	WebToken string `yaml:"web_token,omitempty"`
}

type BrowserConfig struct {
	Visible     bool   `yaml:"visible,omitempty"`
	Width       int    `yaml:"width,omitempty"`
	Height      int    `yaml:"height,omitempty"`
	UserAgent   string `yaml:"user_agent,omitempty"`
	Proxy       string `yaml:"proxy,omitempty"`
	CDPEndpoint string `yaml:"cdp_endpoint,omitempty"`
	IdleTimeout int    `yaml:"idle_timeout,omitempty"`
	MaxTabs     int    `yaml:"max_tabs,omitempty"`
}

type UploadConfig struct {
	Dir     string `yaml:"dir,omitempty"`
	MaxSize int64  `yaml:"max_size,omitempty"`
}

type ServerConfig struct {
	Host      string `yaml:"host,omitempty"`
	Port      int    `yaml:"port,omitempty"`
	PublicURL string `yaml:"public_url,omitempty"`
}

type DatabaseConfig struct {
	Driver       string `yaml:"driver,omitempty"`
	DSN          string `yaml:"dsn,omitempty"`
	MaxOpenConns int    `yaml:"max_open_conns,omitempty"`
	MaxIdleConns int    `yaml:"max_idle_conns,omitempty"`
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

// EnsureAuthWebToken 若 auth.web_token 为空则生成随机令牌并写回配置文件（首次启动）。
// 返回 generated=true 表示本次新写入，调用方可打日志提示用户。
func EnsureAuthWebToken(cfg *Config, path string) (generated bool, err error) {
	if strings.TrimSpace(cfg.Auth.WebToken) != "" {
		return false, nil
	}
	cfg.Auth.WebToken = strings.TrimSpace("web-" + strings.ReplaceAll(uuid.New().String(), "-", ""))
	if err := cfg.Save(path); err != nil {
		return false, err
	}
	return true, nil
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
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}
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
	if cfg.Upload.Dir == "" {
		cfg.Upload.Dir = "./uploads"
	}
	if cfg.Upload.MaxSize == 0 {
		cfg.Upload.MaxSize = 20 << 20 // 20MB
	}
	if cfg.Log.MaxSize == 0 {
		cfg.Log.MaxSize = 10 // 10MB
	}
	cfg.Auth.WebToken = strings.TrimSpace(cfg.Auth.WebToken)
}
