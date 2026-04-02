package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// Workspace 封装工作区目录结构及其生命周期，可通过依赖注入传递。
type Workspace struct {
	root         string
	agentDirOnce sync.Map
}

// New 创建并初始化工作区目录结构，替代原先的 Init 全局函数。
func New(dir string) (*Workspace, error) {
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("get user home: %w", err)
		}
		dir = filepath.Join(home, ".aiclaw")
	}

	for _, sub := range []string{
		"",
		"uploads",
		"skills",
		"cron/scripts",
		"cron/logs",
		"agents",
		"logs",
	} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, fmt.Errorf("create workspace dir %q: %w", sub, err)
		}
	}
	return &Workspace{root: dir}, nil
}

func (w *Workspace) Root() string { return w.root }

func (w *Workspace) Uploads() string { return filepath.Join(w.root, "uploads") }

func (w *Workspace) Logs() string { return filepath.Join(w.root, "logs") }

func (w *Workspace) CronDir() string { return filepath.Join(w.root, "cron") }

func (w *Workspace) CronScripts() string { return filepath.Join(w.root, "cron", "scripts") }

func (w *Workspace) CronLogs() string { return filepath.Join(w.root, "cron", "logs") }

func (w *Workspace) Skills() string { return filepath.Join(w.root, "skills") }

func (w *Workspace) SkillDir(dirName string) string {
	if dirName == "" {
		return ""
	}
	return filepath.Join(w.root, "skills", dirName)
}

// AgentDir 返回指定 agent 的工作目录；首次调用时创建子目录。
func (w *Workspace) AgentDir(uuid string) string {
	if uuid == "" {
		return ""
	}
	dir := filepath.Join(w.root, "agents", uuid)
	if _, loaded := w.agentDirOnce.LoadOrStore(uuid, struct{}{}); !loaded {
		for _, sub := range []string{"", "sandbox", "tmp", "session-memory"} {
			_ = os.MkdirAll(filepath.Join(dir, sub), 0o755)
		}
	}
	return dir
}

func (w *Workspace) AgentSandbox(uuid string) string {
	d := w.AgentDir(uuid)
	if d == "" {
		return ""
	}
	return filepath.Join(d, "sandbox")
}

func (w *Workspace) AgentTmp(uuid string) string {
	d := w.AgentDir(uuid)
	if d == "" {
		return ""
	}
	return filepath.Join(d, "tmp")
}

func (w *Workspace) AgentSessionMemory(uuid string) string {
	d := w.AgentDir(uuid)
	if d == "" {
		return ""
	}
	return filepath.Join(d, "session-memory")
}

// CleanupAgentTmpFiles 清理所有 agent tmp 目录中超过 maxAge 的临时文件。
func (w *Workspace) CleanupAgentTmpFiles(maxAge time.Duration) {
	agentsDir := filepath.Join(w.root, "agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-maxAge)
	removed := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		tmpDir := filepath.Join(agentsDir, entry.Name(), "tmp")
		files, err := os.ReadDir(tmpDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			info, err := f.Info()
			if err != nil {
				continue
			}
			if info.ModTime().Before(cutoff) {
				if os.Remove(filepath.Join(tmpDir, f.Name())) == nil {
					removed++
				}
			}
		}
	}
	if removed > 0 {
		log.WithFields(log.Fields{"removed": removed, "max_age": maxAge}).Info("[Workspace] cleaned up agent tmp files")
	}
}

// --------------- Context keys ---------------

type (
	scopeKey struct{}
	wsKey    struct{}
)

// WithWorkdirScope 将 agent UUID 注入 context，供 sandbox/tmp 等工具解析。
func WithWorkdirScope(ctx context.Context, scopeID string) context.Context {
	return context.WithValue(ctx, scopeKey{}, scopeID)
}

// WorkdirScopeFromContext 从 context 提取 agent UUID。
func WorkdirScopeFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(scopeKey{}).(string); ok {
		return v
	}
	return ""
}

// WithWorkspace 将 *Workspace 注入 context，供工具 handler 通过 context 获取。
func WithWorkspace(ctx context.Context, ws *Workspace) context.Context {
	return context.WithValue(ctx, wsKey{}, ws)
}

// FromContext 从 context 提取 *Workspace。
func FromContext(ctx context.Context) *Workspace {
	if ws, ok := ctx.Value(wsKey{}).(*Workspace); ok {
		return ws
	}
	return nil
}

// --------------- Context 便捷函数（工具 handler 用） ---------------

// AgentSandboxFromCtx 从 context 中的 Workspace + WorkdirScope 返回 sandbox 目录。
func AgentSandboxFromCtx(ctx context.Context) string {
	ws := FromContext(ctx)
	id := WorkdirScopeFromContext(ctx)
	if ws == nil || id == "" {
		return ""
	}
	return ws.AgentSandbox(id)
}

// AgentTmpFromCtx 从 context 中的 Workspace + WorkdirScope 返回 tmp 目录。
func AgentTmpFromCtx(ctx context.Context) string {
	ws := FromContext(ctx)
	id := WorkdirScopeFromContext(ctx)
	if ws == nil || id == "" {
		return ""
	}
	return ws.AgentTmp(id)
}

// AgentSessionMemoryFromCtx 从 context 中的 Workspace + WorkdirScope 返回 session-memory 目录。
func AgentSessionMemoryFromCtx(ctx context.Context) string {
	ws := FromContext(ctx)
	id := WorkdirScopeFromContext(ctx)
	if ws == nil || id == "" {
		return ""
	}
	return ws.AgentSessionMemory(id)
}
