package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/model"
)

// Skill 自动结晶 (W3-W4)：
//
// 当一次 Agent 执行中调用了 ≥crystallizeMinDistinctTools 个不同工具且最终成功，
// 视为存在「可复用执行路径」的潜力，将其归档到 skills-pending/<ts>.md。
// 这些归档不会自动注入 system prompt；需要通过 `skill` 工具 promote 到 ~/.aiclaw/skills/
// 才会成为正式 skill。
const (
	crystallizeMinDistinctTools = 3
	crystallizeMaxToolStepsKept = 24
)

// crystallizeHook 在 Agent 完成后判断是否值得归档为待审 skill 候选。
func (e *Executor) crystallizeHook(_ context.Context, _ HookEvent, p *HookPayload) HookAction {
	if p == nil || p.Tracker == nil || p.WS == nil || p.Agent == nil {
		return HookContinue
	}

	steps := p.Tracker.Steps()
	toolSteps, distinct := extractToolSteps(steps)
	if distinct < crystallizeMinDistinctTools {
		log.WithFields(log.Fields{
			"distinct":  distinct,
			"min":       crystallizeMinDistinctTools,
			"tool_steps": len(toolSteps),
			"total":     len(steps),
			"conv":      p.ConvUUID,
		}).Debug("[Skill] crystallize skipped: not enough distinct tools")
		return HookContinue
	}
	if errName := firstErrorStep(steps); errName != "" {
		log.WithFields(log.Fields{
			"distinct":      distinct,
			"tool_steps":    len(toolSteps),
			"total":         len(steps),
			"first_error":   errName,
			"conv":          p.ConvUUID,
		}).Debug("[Skill] crystallize skipped: execution contained error steps")
		return HookContinue
	}

	dir := pendingSkillsDir(p.WS.Root())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.WithError(err).Debug("[Skill] mkdir pending failed")
		return HookContinue
	}

	slug := slugifyTask(p.UserMsg)
	ts := time.Now().Format("20060102-150405")
	fileName := fmt.Sprintf("%s-%s.md", ts, slug)
	path := filepath.Join(dir, fileName)

	content := buildPendingSkill(p, toolSteps, distinct)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		log.WithError(err).WithField("path", path).Debug("[Skill] write pending failed")
		return HookContinue
	}
	log.WithFields(log.Fields{
		"path":     path,
		"tools":    distinct,
		"agent":    p.Agent.Name,
		"conv":     p.ConvUUID,
	}).Info("[Skill] crystallized pending candidate")
	return HookContinue
}

func extractToolSteps(steps []model.ExecutionStep) ([]model.ExecutionStep, int) {
	var toolSteps []model.ExecutionStep
	distinct := map[string]bool{}
	for _, s := range steps {
		if s.StepType != model.StepToolCall {
			continue
		}
		toolSteps = append(toolSteps, s)
		distinct[s.Name] = true
	}
	if len(toolSteps) > crystallizeMaxToolStepsKept {
		toolSteps = toolSteps[len(toolSteps)-crystallizeMaxToolStepsKept:]
	}
	return toolSteps, len(distinct)
}

func hasErrorStep(steps []model.ExecutionStep) bool {
	return firstErrorStep(steps) != ""
}

// firstErrorStep 返回第一条 StepError 的标识（"<stepType>:<name>"），用于诊断日志。
func firstErrorStep(steps []model.ExecutionStep) string {
	for _, s := range steps {
		if s.Status == model.StepError {
			return fmt.Sprintf("%s:%s", s.StepType, s.Name)
		}
	}
	return ""
}

func buildPendingSkill(p *HookPayload, toolSteps []model.ExecutionStep, distinct int) string {
	var sb strings.Builder

	sb.WriteString("---\n")
	sb.WriteString("name: \"\"\n")
	sb.WriteString("description: \"\"\n")
	sb.WriteString("status: pending\n")
	sb.WriteString(fmt.Sprintf("captured_at: %s\n", time.Now().Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("agent: %s\n", p.Agent.Name))
	sb.WriteString(fmt.Sprintf("conversation: %s\n", p.ConvUUID))
	sb.WriteString(fmt.Sprintf("tools_used: %d distinct, %d steps\n", distinct, len(toolSteps)))
	sb.WriteString("---\n\n")

	sb.WriteString("# Candidate Skill\n\n")
	sb.WriteString("> 这是 aiclaw 自动归档的执行路径候选。需要你 (或 LLM 通过 `skill` 工具) 填写 name/description ")
	sb.WriteString("并通过 promote 转正后才会成为正式 skill。\n\n")

	sb.WriteString("## 触发场景\n\n")
	sb.WriteString("用户请求：\n\n```\n")
	sb.WriteString(truncateRunes(strings.TrimSpace(p.UserMsg), 800))
	sb.WriteString("\n```\n\n")

	sb.WriteString("## 执行路径\n\n")
	if len(toolSteps) == 0 {
		sb.WriteString("（未捕获工具步骤）\n\n")
	} else {
		for i, s := range toolSteps {
			sb.WriteString(fmt.Sprintf("%d. **%s** ", i+1, s.Name))
			if s.DurationMs > 0 {
				sb.WriteString(fmt.Sprintf("(%dms) ", s.DurationMs))
			}
			sb.WriteString("\n")
			if input := strings.TrimSpace(s.Input); input != "" {
				sb.WriteString("   - input: `")
				sb.WriteString(truncateRunes(collapseWhitespace(input), 200))
				sb.WriteString("`\n")
			}
			if output := strings.TrimSpace(s.Output); output != "" {
				sb.WriteString("   - output: `")
				sb.WriteString(truncateRunes(collapseWhitespace(output), 200))
				sb.WriteString("`\n")
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## 最终输出\n\n")
	sb.WriteString(truncateRunes(strings.TrimSpace(p.Content), 1200))
	sb.WriteString("\n")

	return sb.String()
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

func slugifyTask(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugRE.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "task"
	}
	if utf8.RuneCountInString(s) > 32 {
		rs := []rune(s)
		s = string(rs[:32])
	}
	return s
}

func pendingSkillsDir(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, "skills-pending")
}

// ── Public helpers used by the `skill` tool ──

// PendingSkill 是 skills-pending 目录下的一份候选。
type PendingSkill struct {
	FileName  string    `json:"file_name"`
	Path      string    `json:"path"`
	UpdatedAt time.Time `json:"updated_at"`
	Preview   string    `json:"preview"`
}

// ListPendingSkills 返回 skills-pending 目录中按更新时间倒序排列的候选清单。
func ListPendingSkills(workspaceRoot string, limit int) ([]PendingSkill, error) {
	dir := pendingSkillsDir(workspaceRoot)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	type tmp struct {
		ps  PendingSkill
		mt  time.Time
	}
	var items []tmp
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".md") {
			continue
		}
		info, err := ent.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(dir, ent.Name())
		preview, _ := previewFile(path, 240)
		items = append(items, tmp{
			ps: PendingSkill{
				FileName:  ent.Name(),
				Path:      path,
				UpdatedAt: info.ModTime(),
				Preview:   preview,
			},
			mt: info.ModTime(),
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].mt.After(items[j].mt) })
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	out := make([]PendingSkill, len(items))
	for i, it := range items {
		out[i] = it.ps
	}
	return out, nil
}

// ReadPendingSkill 返回单个 pending skill 的完整内容。
func ReadPendingSkill(workspaceRoot, fileName string) (string, error) {
	if !safeFileName(fileName) {
		return "", fmt.Errorf("invalid file name: %q", fileName)
	}
	path := filepath.Join(pendingSkillsDir(workspaceRoot), fileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// PromotePendingSkill 把一份 pending 转正到 skillsRoot/<slug>/SKILL.md。
// name 与 description 必填，会被写入 frontmatter 顶部。
// 返回新建的目录路径。
func PromotePendingSkill(workspaceRoot, skillsRoot, fileName, name, description string) (string, error) {
	if !safeFileName(fileName) {
		return "", fmt.Errorf("invalid file name: %q", fileName)
	}
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("name is required")
	}
	if strings.TrimSpace(description) == "" {
		return "", fmt.Errorf("description is required")
	}

	srcPath := filepath.Join(pendingSkillsDir(workspaceRoot), fileName)
	raw, err := os.ReadFile(srcPath)
	if err != nil {
		return "", fmt.Errorf("read pending: %w", err)
	}

	body := stripExistingFrontmatter(string(raw))

	dirSlug := slugifyTask(name)
	if dirSlug == "" {
		return "", fmt.Errorf("name yields empty slug: %q", name)
	}
	skillDir := filepath.Join(skillsRoot, dirSlug)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir skill: %w", err)
	}

	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("name: %s\n", yamlEscape(name)))
	sb.WriteString(fmt.Sprintf("description: %s\n", yamlEscape(description)))
	sb.WriteString(fmt.Sprintf("promoted_from: %s\n", fileName))
	sb.WriteString(fmt.Sprintf("promoted_at: %s\n", time.Now().Format(time.RFC3339)))
	sb.WriteString("---\n\n")
	sb.WriteString(body)

	dstPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(dstPath, []byte(sb.String()), 0o644); err != nil {
		return "", fmt.Errorf("write SKILL.md: %w", err)
	}

	if err := os.Remove(srcPath); err != nil {
		log.WithError(err).Debug("[Skill] remove pending after promote failed")
	}
	return skillDir, nil
}

// DiscardPendingSkill 直接删除一份 pending 候选。
func DiscardPendingSkill(workspaceRoot, fileName string) error {
	if !safeFileName(fileName) {
		return fmt.Errorf("invalid file name: %q", fileName)
	}
	return os.Remove(filepath.Join(pendingSkillsDir(workspaceRoot), fileName))
}

func safeFileName(name string) bool {
	if name == "" || strings.ContainsAny(name, "/\\") || strings.HasPrefix(name, ".") {
		return false
	}
	return strings.HasSuffix(name, ".md")
}

func previewFile(path string, maxRunes int) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	body := stripExistingFrontmatter(string(data))
	body = collapseWhitespace(body)
	return truncateRunes(strings.TrimSpace(body), maxRunes), nil
}

// stripExistingFrontmatter 移除文件起始的 YAML frontmatter（若有）。
func stripExistingFrontmatter(s string) string {
	if !strings.HasPrefix(s, "---") {
		return s
	}
	rest := s[3:]
	if idx := strings.Index(rest, "\n---"); idx >= 0 {
		body := rest[idx+4:]
		return strings.TrimLeft(body, "\n")
	}
	return s
}

func yamlEscape(s string) string {
	if strings.ContainsAny(s, "\n\"'#:") {
		s = strings.ReplaceAll(s, "\"", "\\\"")
		return "\"" + s + "\""
	}
	return s
}
