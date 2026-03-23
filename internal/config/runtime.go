package config

import (
	"sync"
)

// RT 进程内当前生效的配置（与磁盘 config.yaml 对应）；热加载时原地替换 *Cfg 内容以保持指针稳定。
var RT struct {
	Mu   sync.RWMutex
	Path string
	Cfg  *Config
}

// SetRuntime 在 Load 成功后调用，绑定配置文件路径与内存中的 *Config（与 main 中变量应为同一指针）。
func SetRuntime(path string, cfg *Config) {
	RT.Mu.Lock()
	defer RT.Mu.Unlock()
	RT.Path = path
	RT.Cfg = cfg
}

// SaveRuntime 将 RT.Cfg 写回 RT.Path（供单例 Agent 等保存时调用）。
func SaveRuntime() error {
	RT.Mu.RLock()
	path := RT.Path
	c := RT.Cfg
	RT.Mu.RUnlock()
	if c == nil || path == "" {
		return nil
	}
	return c.Save(path)
}

// ReplaceRuntimeFromDisk 重新读取磁盘并合并进当前 Cfg（保持 Cfg 指针不变）。
func ReplaceRuntimeFromDisk() error {
	RT.Mu.Lock()
	defer RT.Mu.Unlock()
	if RT.Path == "" || RT.Cfg == nil {
		return nil
	}
	next, err := Load(RT.Path)
	if err != nil {
		return err
	}
	*RT.Cfg = *next
	return nil
}
