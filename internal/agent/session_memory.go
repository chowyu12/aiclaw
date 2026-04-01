package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/workspace"
)

const sessionMemoryMaxRunes = 2000

func sessionMemoryPath(convUUID string) string {
	dir := workspace.SessionMemory()
	if dir == "" || convUUID == "" {
		return ""
	}
	return filepath.Join(dir, convUUID+".md")
}

// appendSessionMemory 将本轮执行摘要追加到会话笔记文件。
func appendSessionMemory(convUUID, userMsg string, toolNames []string, outcome string) {
	path := sessionMemoryPath(convUUID)
	if path == "" {
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\n## %s\n", time.Now().Format("2006-01-02 15:04")))

	userSnippet := string([]rune(userMsg))
	if len([]rune(userSnippet)) > 200 {
		userSnippet = string([]rune(userSnippet)[:200]) + "..."
	}
	sb.WriteString(fmt.Sprintf("**User**: %s\n", userSnippet))

	if len(toolNames) > 0 {
		sb.WriteString(fmt.Sprintf("**Tools**: %s\n", strings.Join(toolNames, ", ")))
	}

	outcomeSnippet := string([]rune(outcome))
	if len([]rune(outcomeSnippet)) > 300 {
		outcomeSnippet = string([]rune(outcomeSnippet)[:300]) + "..."
	}
	sb.WriteString(fmt.Sprintf("**Outcome**: %s\n", outcomeSnippet))

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

// loadSessionMemory 读取会话笔记文件，返回截断后的内容供注入 system prompt。
func loadSessionMemory(convUUID string) string {
	path := sessionMemoryPath(convUUID)
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
