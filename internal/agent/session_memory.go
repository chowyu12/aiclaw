package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/tools/memorytool"
	"github.com/chowyu12/aiclaw/internal/tools/todotool"
	"github.com/chowyu12/aiclaw/internal/workspace"
)

const sessionMemoryMaxRunes = 2000

func sessionMemoryPath(ws *workspace.Workspace, agentUUID, convUUID string) string {
	if ws == nil || convUUID == "" {
		return ""
	}
	dir := ws.AgentSessionMemory(agentUUID)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, convUUID+".md")
}

func appendSessionMemory(ws *workspace.Workspace, agentUUID, convUUID, userMsg string, toolNames []string, outcome string) {
	path := sessionMemoryPath(ws, agentUUID, convUUID)
	if path == "" {
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\n## %s\n", time.Now().Format("2006-01-02 15:04")))

	sb.WriteString(fmt.Sprintf("**User**: %s\n", truncateRunes(userMsg, 200)))

	if len(toolNames) > 0 {
		sb.WriteString(fmt.Sprintf("**Tools**: %s\n", strings.Join(toolNames, ", ")))
	}

	sb.WriteString(fmt.Sprintf("**Outcome**: %s\n", truncateRunes(outcome, 300)))

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.WithError(err).WithField("path", path).Debug("[SessionMemory] open file failed")
		return
	}
	defer f.Close()
	if _, err := f.WriteString(sb.String()); err != nil {
		log.WithError(err).Debug("[SessionMemory] write failed")
	}
}

// loadPersistentMemory 加载冻结的持久记忆快照，注入 system prompt。
func loadPersistentMemory(ws *workspace.Workspace) string {
	memBlock, userBlock := memorytool.LoadSnapshot(ws)
	var parts []string
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
	store := todotool.GetOrCreateStore(convUUID)
	items := store.Get()
	return todotool.FormatTodoBlock(items)
}

func loadSessionMemory(ws *workspace.Workspace, agentUUID, convUUID string) string {
	path := sessionMemoryPath(ws, agentUUID, convUUID)
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return ""
	}
	rs := []rune(content)
	if len(rs) > sessionMemoryMaxRunes {
		content = string(rs[len(rs)-sessionMemoryMaxRunes:])
	}
	return content
}
