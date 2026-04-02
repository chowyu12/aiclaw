package config

import (
	"strings"
	"sync"
)

// RuntimeConfig 持有进程内当前生效的配置（与磁盘 config.yaml 对应），
// 通过依赖注入传递而非包级全局变量。热加载时原地替换 *Cfg 内容以保持指针稳定。
type RuntimeConfig struct {
	mu   sync.RWMutex
	path string
	cfg  *Config
}

// NewRuntimeConfig 绑定配置文件路径与内存中的 *Config。
func NewRuntimeConfig(path string, cfg *Config) *RuntimeConfig {
	return &RuntimeConfig{path: path, cfg: cfg}
}

// Path 返回配置文件路径。
func (rt *RuntimeConfig) Path() string { return rt.path }

// Save 将当前配置写回磁盘。
func (rt *RuntimeConfig) Save() error {
	rt.mu.RLock()
	path := rt.path
	c := rt.cfg
	rt.mu.RUnlock()
	if c == nil || path == "" {
		return nil
	}
	return c.Save(path)
}

// PublicURL 返回全局服务公网地址（去除末尾斜线），未配置时返回空字符串。
func (rt *RuntimeConfig) PublicURL() string {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	if rt.cfg == nil {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(rt.cfg.Server.PublicURL), "/")
}

// ReplaceFromDisk 重新读取磁盘并合并进当前 Cfg（保持 Cfg 指针不变）。
func (rt *RuntimeConfig) ReplaceFromDisk() error {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.path == "" || rt.cfg == nil {
		return nil
	}
	next, err := Load(rt.path)
	if err != nil {
		return err
	}
	*rt.cfg = *next
	return nil
}

// WithReadLock 在读锁保护下访问当前配置。
func (rt *RuntimeConfig) WithReadLock(fn func(cfg *Config)) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	fn(rt.cfg)
}
