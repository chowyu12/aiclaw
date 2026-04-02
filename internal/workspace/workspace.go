package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var root string
var agentDirOnce sync.Map

type ctxKey struct{}

// WithWorkdirScope 将工作区子目录名（当前即单例 Agent 的 UUID）注入 context，供 sandbox/tmp 等工具解析。
func WithWorkdirScope(ctx context.Context, scopeID string) context.Context {
	return context.WithValue(ctx, ctxKey{}, scopeID)
}

func WorkdirScopeFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKey{}).(string); ok {
		return v
	}
	return ""
}

func Init(dir string) error {
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("get user home: %w", err)
		}
		dir = filepath.Join(home, ".aiclaw")
	}
	root = dir

	for _, sub := range []string{
		"",
		"uploads",
		"skills",
		"cron/scripts",
		"cron/logs",
		"agents",
		"logs",
	} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
			return fmt.Errorf("create workspace dir %q: %w", sub, err)
		}
	}
	return nil
}

func Root() string { return root }

// ResetRootForTesting 清空内存中的 workspace 根路径，仅用于测试隔离。
func ResetRootForTesting() {
	root = ""
}

func Uploads() string {
	if root == "" {
		return ""
	}
	return filepath.Join(root, "uploads")
}

func Logs() string {
	if root == "" {
		return ""
	}
	return filepath.Join(root, "logs")
}

func CronDir() string {
	if root == "" {
		return ""
	}
	return filepath.Join(root, "cron")
}

func CronScripts() string {
	if root == "" {
		return ""
	}
	return filepath.Join(root, "cron", "scripts")
}

func CronLogs() string {
	if root == "" {
		return ""
	}
	return filepath.Join(root, "cron", "logs")
}

func Skills() string {
	if root == "" {
		return ""
	}
	return filepath.Join(root, "skills")
}

func SkillDir(dirName string) string {
	if root == "" || dirName == "" {
		return ""
	}
	return filepath.Join(root, "skills", dirName)
}

func AgentSessionMemory(uuid string) string {
	d := AgentDir(uuid)
	if d == "" {
		return ""
	}
	return filepath.Join(d, "session-memory")
}

// AgentSessionMemoryFromCtx 从 context 中的 WorkdirScope 返回对应 session-memory 目录。
func AgentSessionMemoryFromCtx(ctx context.Context) string {
	if id := WorkdirScopeFromContext(ctx); id != "" {
		return AgentSessionMemory(id)
	}
	return ""
}

// AgentDir 返回指定 agent 的工作目录；首次调用时创建子目录，后续直接返回缓存路径。
func AgentDir(uuid string) string {
	if root == "" || uuid == "" {
		return ""
	}
	dir := filepath.Join(root, "agents", uuid)
	if _, loaded := agentDirOnce.LoadOrStore(uuid, struct{}{}); !loaded {
		for _, sub := range []string{"", "sandbox", "tmp", "session-memory"} {
			_ = os.MkdirAll(filepath.Join(dir, sub), 0o755)
		}
	}
	return dir
}

func AgentSandbox(uuid string) string {
	d := AgentDir(uuid)
	if d == "" {
		return ""
	}
	return filepath.Join(d, "sandbox")
}

func AgentTmp(uuid string) string {
	d := AgentDir(uuid)
	if d == "" {
		return ""
	}
	return filepath.Join(d, "tmp")
}

// AgentSandboxFromCtx 从 context 中的 WorkdirScope 返回对应 sandbox 目录。
func AgentSandboxFromCtx(ctx context.Context) string {
	if id := WorkdirScopeFromContext(ctx); id != "" {
		return AgentSandbox(id)
	}
	return ""
}

// AgentTmpFromCtx 从 context 中的 WorkdirScope 返回对应 tmp 目录。
func AgentTmpFromCtx(ctx context.Context) string {
	if id := WorkdirScopeFromContext(ctx); id != "" {
		return AgentTmp(id)
	}
	return ""
}
