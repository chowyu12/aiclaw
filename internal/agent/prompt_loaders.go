package agent

import (
	"strings"

	"github.com/chowyu12/aiclaw/internal/tools/memorytool"
	"github.com/chowyu12/aiclaw/internal/tools/todotool"
	"github.com/chowyu12/aiclaw/internal/workspace"
)

// loadPersistentMemory 加载冻结的持久记忆快照（MEMORY.md + USER.md），注入 system prompt。
// 内部调用 memorytool.LoadSnapshot；超过容量阈值时返回 INDEX 模式片段。
func loadPersistentMemory(ws *workspace.Workspace) string {
	memBlock, userBlock := memorytool.LoadSnapshot(ws)
	parts := make([]string, 0, 2)
	if memBlock != "" {
		parts = append(parts, memBlock)
	}
	if userBlock != "" {
		parts = append(parts, userBlock)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n\n")
}

// loadTodoBlock 加载当前会话的 todo 列表，格式化为 system prompt 片段。
func loadTodoBlock(convUUID string) string {
	if convUUID == "" {
		return ""
	}
	return todotool.FormatTodoBlock(todotool.GetOrCreateStore(convUUID).Get())
}
