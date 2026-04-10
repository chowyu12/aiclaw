package memorytool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/chowyu12/aiclaw/internal/workspace"
)

const (
	entryDelimiter = "\n§\n"
	memoryLimit    = 2200
	userLimit      = 1375
)

type memoryArgs struct {
	Action  string `json:"action"`
	Target  string `json:"target"`
	Content string `json:"content,omitempty"`
	OldText string `json:"old_text,omitempty"`
}

type memoryResult struct {
	Success    bool     `json:"success"`
	Message    string   `json:"message,omitempty"`
	Target     string   `json:"target"`
	Entries    []string `json:"entries"`
	Usage      string   `json:"usage"`
	EntryCount int      `json:"entry_count"`
	Error      string   `json:"error,omitempty"`
}

var fileMu sync.Mutex

// Handler 是 memory 工具的入口。
func Handler(ctx context.Context, args string) (string, error) {
	var p memoryArgs
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if p.Target == "" {
		p.Target = "memory"
	}
	if p.Target != "memory" && p.Target != "user" {
		return errJSON(p.Target, fmt.Sprintf("invalid target %q, use 'memory' or 'user'", p.Target)), nil
	}

	ws := workspace.FromContext(ctx)
	dir := memoriesDir(ws)

	fileMu.Lock()
	defer fileMu.Unlock()

	switch p.Action {
	case "add":
		return handleAdd(dir, p)
	case "replace":
		return handleReplace(dir, p)
	case "remove":
		return handleRemove(dir, p)
	case "read":
		return handleRead(dir, p)
	default:
		return errJSON(p.Target, fmt.Sprintf("unknown action %q, use: add, replace, remove, read", p.Action)), nil
	}
}

// LoadSnapshot 在 session 启动时调用，返回冻结的 system prompt 片段。
func LoadSnapshot(ws *workspace.Workspace) (memoryBlock, userBlock string) {
	dir := memoriesDir(ws)
	memEntries := readFile(filepath.Join(dir, "MEMORY.md"))
	userEntries := readFile(filepath.Join(dir, "USER.md"))

	if len(memEntries) > 0 {
		content := strings.Join(memEntries, entryDelimiter)
		pct := min(100, len(content)*100/memoryLimit)
		memoryBlock = fmt.Sprintf("══════════════════════════════════════════════\nMEMORY (你的个人笔记) [%d%% — %d/%d 字符]\n══════════════════════════════════════════════\n%s",
			pct, len(content), memoryLimit, content)
	}
	if len(userEntries) > 0 {
		content := strings.Join(userEntries, entryDelimiter)
		pct := min(100, len(content)*100/userLimit)
		userBlock = fmt.Sprintf("══════════════════════════════════════════════\nUSER PROFILE (用户画像) [%d%% — %d/%d 字符]\n══════════════════════════════════════════════\n%s",
			pct, len(content), userLimit, content)
	}
	return
}

func handleAdd(dir string, p memoryArgs) (string, error) {
	content := strings.TrimSpace(p.Content)
	if content == "" {
		return errJSON(p.Target, "content cannot be empty"), nil
	}
	if err := scanContent(content); err != nil {
		return errJSON(p.Target, err.Error()), nil
	}

	path := targetPath(dir, p.Target)
	entries := readFile(path)
	limit := charLimit(p.Target)

	for _, e := range entries {
		if e == content {
			return successJSON(p.Target, entries, "条目已存在，未重复添加。"), nil
		}
	}

	newEntries := append(entries, content)
	newTotal := len(strings.Join(newEntries, entryDelimiter))
	if newTotal > limit {
		cur := charCount(entries)
		return errJSON(p.Target, fmt.Sprintf("记忆已占用 %d/%d 字符。添加此条目（%d 字符）将超出限制。请先替换或删除现有条目。", cur, limit, len(content))), nil
	}

	writeFile(path, newEntries)
	return successJSON(p.Target, newEntries, "条目已添加。"), nil
}

func handleReplace(dir string, p memoryArgs) (string, error) {
	oldText := strings.TrimSpace(p.OldText)
	newContent := strings.TrimSpace(p.Content)
	if oldText == "" {
		return errJSON(p.Target, "old_text cannot be empty"), nil
	}
	if newContent == "" {
		return errJSON(p.Target, "content cannot be empty, use 'remove' to delete"), nil
	}
	if err := scanContent(newContent); err != nil {
		return errJSON(p.Target, err.Error()), nil
	}

	path := targetPath(dir, p.Target)
	entries := readFile(path)
	limit := charLimit(p.Target)

	var matches []int
	for i, e := range entries {
		if strings.Contains(e, oldText) {
			matches = append(matches, i)
		}
	}
	if len(matches) == 0 {
		return errJSON(p.Target, fmt.Sprintf("没有找到匹配 '%s' 的条目。", oldText)), nil
	}
	if len(matches) > 1 {
		unique := make(map[string]bool)
		for _, i := range matches {
			unique[entries[i]] = true
		}
		if len(unique) > 1 {
			return errJSON(p.Target, fmt.Sprintf("多个条目匹配 '%s'，请提供更精确的子串。", oldText)), nil
		}
	}

	idx := matches[0]
	test := make([]string, len(entries))
	copy(test, entries)
	test[idx] = newContent
	if len(strings.Join(test, entryDelimiter)) > limit {
		return errJSON(p.Target, "替换后将超出字符限制，请缩短新内容或先删除其他条目。"), nil
	}

	entries[idx] = newContent
	writeFile(path, entries)
	return successJSON(p.Target, entries, "条目已替换。"), nil
}

func handleRemove(dir string, p memoryArgs) (string, error) {
	oldText := strings.TrimSpace(p.OldText)
	if oldText == "" {
		return errJSON(p.Target, "old_text cannot be empty"), nil
	}

	path := targetPath(dir, p.Target)
	entries := readFile(path)

	var matches []int
	for i, e := range entries {
		if strings.Contains(e, oldText) {
			matches = append(matches, i)
		}
	}
	if len(matches) == 0 {
		return errJSON(p.Target, fmt.Sprintf("没有找到匹配 '%s' 的条目。", oldText)), nil
	}

	idx := matches[0]
	entries = append(entries[:idx], entries[idx+1:]...)
	writeFile(path, entries)
	return successJSON(p.Target, entries, "条目已删除。"), nil
}

func handleRead(dir string, p memoryArgs) (string, error) {
	path := targetPath(dir, p.Target)
	entries := readFile(path)
	return successJSON(p.Target, entries, ""), nil
}

// ── 辅助函数 ──

func memoriesDir(ws *workspace.Workspace) string {
	if ws != nil {
		return filepath.Join(ws.Root(), "memories")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".aiclaw", "memories")
}

func targetPath(dir, target string) string {
	os.MkdirAll(dir, 0o755)
	if target == "user" {
		return filepath.Join(dir, "USER.md")
	}
	return filepath.Join(dir, "MEMORY.md")
}

func charLimit(target string) int {
	if target == "user" {
		return userLimit
	}
	return memoryLimit
}

func charCount(entries []string) int {
	if len(entries) == 0 {
		return 0
	}
	return len(strings.Join(entries, entryDelimiter))
}

func readFile(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	raw := string(data)
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, entryDelimiter)
	var entries []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			entries = append(entries, p)
		}
	}
	return entries
}

func writeFile(path string, entries []string) {
	os.MkdirAll(filepath.Dir(path), 0o755)
	content := ""
	if len(entries) > 0 {
		content = strings.Join(entries, entryDelimiter)
	}
	os.WriteFile(path, []byte(content), 0o644)
}

func successJSON(target string, entries []string, message string) string {
	if entries == nil {
		entries = []string{}
	}
	limit := charLimit(target)
	cur := charCount(entries)
	pct := 0
	if limit > 0 {
		pct = min(100, cur*100/limit)
	}
	r := memoryResult{
		Success:    true,
		Target:     target,
		Entries:    entries,
		Usage:      fmt.Sprintf("%d%% — %d/%d 字符", pct, cur, limit),
		EntryCount: len(entries),
		Message:    message,
	}
	out, _ := json.Marshal(r)
	return string(out)
}

func errJSON(target, msg string) string {
	r := memoryResult{
		Success: false,
		Target:  target,
		Error:   msg,
	}
	out, _ := json.Marshal(r)
	return string(out)
}

// ── 安全扫描 ──

var threatPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)ignore\s+(previous|all|above|prior)\s+instructions`),
	regexp.MustCompile(`(?i)you\s+are\s+now\s+`),
	regexp.MustCompile(`(?i)disregard\s+(your|all|any)\s+(instructions|rules)`),
	regexp.MustCompile(`(?i)curl\s+[^\n]*\$\{?\w*(KEY|TOKEN|SECRET|PASSWORD|API)`),
	regexp.MustCompile(`(?i)wget\s+[^\n]*\$\{?\w*(KEY|TOKEN|SECRET|PASSWORD|API)`),
}

var invisibleChars = map[rune]bool{
	'\u200b': true, '\u200c': true, '\u200d': true,
	'\u2060': true, '\ufeff': true,
	'\u202a': true, '\u202b': true, '\u202c': true, '\u202d': true, '\u202e': true,
}

func scanContent(content string) error {
	for _, r := range content {
		if invisibleChars[r] {
			return fmt.Errorf("内容包含不可见 Unicode 字符 U+%04X，可能是注入攻击", r)
		}
	}
	for _, pat := range threatPatterns {
		if pat.MatchString(content) {
			return fmt.Errorf("内容匹配安全威胁模式，记忆条目会注入 system prompt，不能包含注入或数据外泄内容")
		}
	}
	return nil
}
