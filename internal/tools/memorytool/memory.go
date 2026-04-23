package memorytool

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/chowyu12/aiclaw/internal/workspace"
)

const (
	entryDelimiter = "\n§\n"
	memoryLimit    = 2200
	userLimit      = 1375

	// indexThresholdPct 当字符占用超过该比例时，LoadSnapshot 自动切换为索引模式：
	// 仅注入每条 entry 的 [id]/tag/摘要，完整内容通过 recall 动作按 ID 拉取。
	indexThresholdPct = 70

	// summaryRunes 索引模式下每条 entry 摘要的字符长度（rune 计）。
	summaryRunes = 80
)

type memoryArgs struct {
	Action  string   `json:"action"`
	Target  string   `json:"target"`
	Content string   `json:"content,omitempty"`
	OldText string   `json:"old_text,omitempty"`
	Tag     string   `json:"tag,omitempty"`
	IDs     []string `json:"ids,omitempty"`
}

type entryView struct {
	ID      string `json:"id"`
	Tag     string `json:"tag,omitempty"`
	Summary string `json:"summary"`
	Content string `json:"content,omitempty"`
}

type memoryResult struct {
	Success    bool        `json:"success"`
	Message    string      `json:"message,omitempty"`
	Target     string      `json:"target"`
	Entries    []entryView `json:"entries"`
	Usage      string      `json:"usage"`
	EntryCount int         `json:"entry_count"`
	Indexed    bool        `json:"indexed"`
	Error      string      `json:"error,omitempty"`
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
	case "recall":
		return handleRecall(dir, p)
	default:
		return errJSON(p.Target, fmt.Sprintf("unknown action %q, use: add, replace, remove, read, recall", p.Action)), nil
	}
}

// LoadSnapshot 在 session 启动时调用，返回冻结的 system prompt 片段。
// 当字符占用 >= indexThresholdPct 时自动切换为索引模式（L1 Insight Index）：
// 仅注入每条 entry 的 [id] + tag + summary，完整内容通过 recall 动作按 ID 拉取。
func LoadSnapshot(ws *workspace.Workspace) (memoryBlock, userBlock string) {
	dir := memoriesDir(ws)
	memoryBlock = renderSnapshotBlock(dir, "memory", "MEMORY (你的个人笔记)", memoryLimit)
	userBlock = renderSnapshotBlock(dir, "user", "USER PROFILE (用户画像)", userLimit)
	return
}

func renderSnapshotBlock(dir, target, title string, limit int) string {
	entries := readEntries(filepath.Join(dir, fileName(target)))
	if len(entries) == 0 {
		return ""
	}

	full := joinEntriesRaw(entries)
	cur := len(full)
	pct := min(100, cur*100/limit)

	header := fmt.Sprintf("══════════════════════════════════════════════\n%s [%d%% — %d/%d 字符]\n══════════════════════════════════════════════",
		title, pct, cur, limit)

	if pct < indexThresholdPct {
		return header + "\n" + full
	}

	var sb strings.Builder
	sb.WriteString(header)
	sb.WriteString("\n[INDEX MODE] 容量已达 ")
	sb.WriteString(fmt.Sprintf("%d%%", pct))
	sb.WriteString("，仅展示索引；完整内容请用 memory(action=recall, target=")
	sb.WriteString(target)
	sb.WriteString(", ids=[\"...\"]) 拉取。\n")
	for _, e := range entries {
		sb.WriteString("\n• [")
		sb.WriteString(e.ID)
		sb.WriteString("]")
		if e.Tag != "" {
			sb.WriteString(" #")
			sb.WriteString(e.Tag)
		}
		sb.WriteString(" ")
		sb.WriteString(truncateRunes(e.Body, summaryRunes))
	}
	return sb.String()
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
	entries := readEntries(path)
	limit := charLimit(p.Target)

	body := stripUserMetadata(content)
	for _, e := range entries {
		if e.Body == body {
			return successJSON(p.Target, entries, "条目已存在，未重复添加。"), nil
		}
	}

	tag := strings.TrimSpace(p.Tag)
	if tag == "" {
		tag = inferTag(body)
	}
	id := newEntryID(body)
	newEntry := storedEntry{ID: id, Tag: tag, Body: body}

	candidate := append(entries, newEntry)
	if len(joinEntriesRaw(candidate)) > limit {
		cur := len(joinEntriesRaw(entries))
		return errJSON(p.Target, fmt.Sprintf("记忆已占用 %d/%d 字符。添加此条目（约 %d 字符）将超出限制。请先替换或删除现有条目。",
			cur, limit, len(newEntry.serialize()))), nil
	}

	writeEntries(path, candidate)
	return successJSON(p.Target, candidate, fmt.Sprintf("条目已添加（id=%s）。", id)), nil
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
	entries := readEntries(path)
	limit := charLimit(p.Target)

	idx, err := findUniqueEntry(entries, oldText)
	if err != nil {
		return errJSON(p.Target, err.Error()), nil
	}

	body := stripUserMetadata(newContent)
	tag := strings.TrimSpace(p.Tag)
	if tag == "" {
		tag = entries[idx].Tag
	}

	test := slices.Clone(entries)
	test[idx] = storedEntry{ID: entries[idx].ID, Tag: tag, Body: body}
	if len(joinEntriesRaw(test)) > limit {
		return errJSON(p.Target, "替换后将超出字符限制，请缩短新内容或先删除其他条目。"), nil
	}

	writeEntries(path, test)
	return successJSON(p.Target, test, fmt.Sprintf("条目已替换（id=%s）。", entries[idx].ID)), nil
}

func handleRemove(dir string, p memoryArgs) (string, error) {
	oldText := strings.TrimSpace(p.OldText)
	if oldText == "" {
		return errJSON(p.Target, "old_text cannot be empty"), nil
	}

	path := targetPath(dir, p.Target)
	entries := readEntries(path)

	idx, err := findUniqueEntry(entries, oldText)
	if err != nil {
		return errJSON(p.Target, err.Error()), nil
	}

	id := entries[idx].ID
	entries = slices.Delete(entries, idx, idx+1)
	writeEntries(path, entries)
	return successJSON(p.Target, entries, fmt.Sprintf("条目已删除（id=%s）。", id)), nil
}

func handleRead(dir string, p memoryArgs) (string, error) {
	path := targetPath(dir, p.Target)
	entries := readEntries(path)
	return successJSON(p.Target, entries, ""), nil
}

func handleRecall(dir string, p memoryArgs) (string, error) {
	if len(p.IDs) == 0 {
		return errJSON(p.Target, "ids cannot be empty"), nil
	}

	path := targetPath(dir, p.Target)
	entries := readEntries(path)

	wanted := make(map[string]bool, len(p.IDs))
	for _, id := range p.IDs {
		id = strings.TrimSpace(id)
		if id != "" {
			wanted[id] = true
		}
	}

	var hits []storedEntry
	var missed []string
	for id := range wanted {
		found := false
		for _, e := range entries {
			if e.ID == id {
				hits = append(hits, e)
				found = true
				break
			}
		}
		if !found {
			missed = append(missed, id)
		}
	}

	views := make([]entryView, 0, len(hits))
	for _, e := range hits {
		views = append(views, entryView{
			ID:      e.ID,
			Tag:     e.Tag,
			Summary: truncateRunes(e.Body, summaryRunes),
			Content: e.Body,
		})
	}

	limit := charLimit(p.Target)
	cur := len(joinEntriesRaw(entries))
	pct := min(100, cur*100/limit)

	r := memoryResult{
		Success:    true,
		Target:     p.Target,
		Entries:    views,
		EntryCount: len(views),
		Usage:      fmt.Sprintf("%d%% — %d/%d 字符", pct, cur, limit),
	}
	if len(missed) > 0 {
		r.Message = "部分 ID 未找到: " + strings.Join(missed, ", ")
	}
	out, _ := json.Marshal(r)
	return string(out), nil
}

// ── 存储格式 ──

// storedEntry 是磁盘上的单条记忆条目；使用前导元数据行 `~~AICLAW-MEMORY:v1 id=<id> tag=<tag>~~`
// 与正文分隔。老格式（无元数据头）会被自动迁移并补齐 ID/tag。
type storedEntry struct {
	ID   string
	Tag  string
	Body string
}

const metaPrefix = "~~AICLAW-MEMORY:v1"

var metaLineRE = regexp.MustCompile(`^~~AICLAW-MEMORY:v1(?:\s+id=([A-Za-z0-9_-]+))?(?:\s+tag=([^~\s]+))?~~$`)

func (e storedEntry) serialize() string {
	header := metaPrefix
	if e.ID != "" {
		header += " id=" + e.ID
	}
	if e.Tag != "" {
		header += " tag=" + e.Tag
	}
	header += "~~"
	return header + "\n" + e.Body
}

func parseEntry(raw string) storedEntry {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return storedEntry{}
	}
	lines := strings.SplitN(raw, "\n", 2)
	first := strings.TrimSpace(lines[0])
	if m := metaLineRE.FindStringSubmatch(first); m != nil {
		body := ""
		if len(lines) == 2 {
			body = strings.TrimSpace(lines[1])
		}
		id := m[1]
		if id == "" {
			id = newEntryID(body)
		}
		return storedEntry{ID: id, Tag: m[2], Body: body}
	}
	// 老格式：把整段当作 body，按内容哈希补一个 ID
	return storedEntry{ID: newEntryID(raw), Tag: inferTag(raw), Body: raw}
}

// stripUserMetadata 防止用户/模型在 content 里写入 `~~AICLAW-MEMORY` 干扰存储格式。
func stripUserMetadata(s string) string {
	if !strings.HasPrefix(strings.TrimSpace(s), metaPrefix) {
		return s
	}
	lines := strings.SplitN(s, "\n", 2)
	if len(lines) == 2 {
		return strings.TrimSpace(lines[1])
	}
	return ""
}

func joinEntriesRaw(entries []storedEntry) string {
	if len(entries) == 0 {
		return ""
	}
	parts := make([]string, len(entries))
	for i, e := range entries {
		parts[i] = e.serialize()
	}
	return strings.Join(parts, entryDelimiter)
}

func findUniqueEntry(entries []storedEntry, query string) (int, error) {
	// 1) 优先按 ID 精确匹配
	for i, e := range entries {
		if e.ID == query {
			return i, nil
		}
	}
	// 2) 退化为 body 子串匹配
	var matches []int
	for i, e := range entries {
		if strings.Contains(e.Body, query) {
			matches = append(matches, i)
		}
	}
	if len(matches) == 0 {
		return -1, fmt.Errorf("没有找到匹配 '%s' 的条目（可传入 id 精确定位）", query)
	}
	if len(matches) > 1 {
		unique := make(map[string]bool)
		for _, i := range matches {
			unique[entries[i].Body] = true
		}
		if len(unique) > 1 {
			return -1, fmt.Errorf("多个条目匹配 '%s'，请提供更精确的子串或 id", query)
		}
	}
	return matches[0], nil
}

func newEntryID(body string) string {
	h := sha1.Sum([]byte(body + ":" + time.Now().Format(time.RFC3339Nano)))
	return "m_" + hex.EncodeToString(h[:4])
}

// inferTag 从 body 中粗略提取一个标签（首个 # 标记，否则取首词）。
func inferTag(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			tag := strings.TrimLeft(line, "#")
			tag = strings.TrimSpace(tag)
			if i := strings.IndexAny(tag, " \t#:：，,。"); i > 0 {
				tag = tag[:i]
			}
			if tag != "" && len(tag) <= 24 {
				return tag
			}
		}
	}
	return ""
}

// ── 辅助函数 ──

func memoriesDir(ws *workspace.Workspace) string {
	if ws != nil {
		return filepath.Join(ws.Root(), "memories")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".aiclaw", "memories")
}

func fileName(target string) string {
	if target == "user" {
		return "USER.md"
	}
	return "MEMORY.md"
}

func targetPath(dir, target string) string {
	os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, fileName(target))
}

func charLimit(target string) int {
	if target == "user" {
		return userLimit
	}
	return memoryLimit
}

func readEntries(path string) []storedEntry {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	raw := string(data)
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, entryDelimiter)
	out := make([]storedEntry, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			continue
		}
		out = append(out, parseEntry(p))
	}
	return out
}

func writeEntries(path string, entries []storedEntry) {
	os.MkdirAll(filepath.Dir(path), 0o755)
	content := ""
	if len(entries) > 0 {
		content = joinEntriesRaw(entries)
	}
	os.WriteFile(path, []byte(content), 0o644)
}

func successJSON(target string, entries []storedEntry, message string) string {
	limit := charLimit(target)
	cur := len(joinEntriesRaw(entries))
	pct := 0
	if limit > 0 {
		pct = min(100, cur*100/limit)
	}
	indexed := pct >= indexThresholdPct

	views := make([]entryView, 0, len(entries))
	for _, e := range entries {
		v := entryView{ID: e.ID, Tag: e.Tag, Summary: truncateRunes(e.Body, summaryRunes)}
		if !indexed {
			v.Content = e.Body
		}
		views = append(views, v)
	}

	r := memoryResult{
		Success:    true,
		Target:     target,
		Entries:    views,
		Usage:      fmt.Sprintf("%d%% — %d/%d 字符", pct, cur, limit),
		EntryCount: len(views),
		Indexed:    indexed,
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

func truncateRunes(s string, maxRunes int) string {
	rs := []rune(s)
	if len(rs) <= maxRunes {
		return s
	}
	return string(rs[:maxRunes]) + "…"
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
