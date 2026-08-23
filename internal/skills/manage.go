package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type PendingSkill struct {
	FileName  string    `json:"file_name"`
	Path      string    `json:"path"`
	UpdatedAt time.Time `json:"updated_at"`
	Preview   string    `json:"preview"`
}

func ListPending(root string, limit int) ([]PendingSkill, error) {
	dir := filepath.Join(root, "skills-pending")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	items := make([]PendingSkill, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !safePendingName(entry.Name()) {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		preview, _ := readPreview(path, 240)
		items = append(items, PendingSkill{FileName: entry.Name(), Path: path, UpdatedAt: info.ModTime(), Preview: preview})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	if limit <= 0 {
		limit = 10
	}
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func ReadPending(root, fileName string) (string, error) {
	path, err := pendingPath(root, fileName)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	return string(data), err
}

func DiscardPending(root, fileName string) error {
	path, err := pendingPath(root, fileName)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

func PromotePending(root, fileName, name, description string) (string, *SkillInfo, error) {
	path, err := pendingPath(root, fileName)
	if err != nil {
		return "", nil, err
	}
	name, description = strings.TrimSpace(name), strings.TrimSpace(description)
	if name == "" || description == "" {
		return "", nil, fmt.Errorf("name and description are required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", nil, fmt.Errorf("read pending skill: %w", err)
	}
	slug := skillSlug(name)
	if slug == "" {
		return "", nil, fmt.Errorf("skill name does not produce a valid slug")
	}
	dir := filepath.Join(root, "skills", slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", nil, err
	}
	body := stripFrontmatter(string(raw))
	content := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s", yamlValue(name), yamlValue(description), body)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		return "", nil, err
	}
	info, err := ParseSkillDir(dir)
	if err != nil {
		return "", nil, err
	}
	if err := os.Remove(path); err != nil {
		return "", nil, err
	}
	return dir, info, nil
}

func pendingPath(root, fileName string) (string, error) {
	if !safePendingName(fileName) {
		return "", fmt.Errorf("invalid pending skill file name %q", fileName)
	}
	return filepath.Join(root, "skills-pending", fileName), nil
}

func safePendingName(name string) bool {
	return name != "" && filepath.Base(name) == name && !strings.HasPrefix(name, ".") && strings.HasSuffix(strings.ToLower(name), ".md")
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func skillSlug(value string) string {
	value = nonSlug.ReplaceAllString(strings.ToLower(strings.TrimSpace(value)), "-")
	return strings.Trim(value, "-")
}

func readPreview(path string, limit int) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	value := strings.Join(strings.Fields(stripFrontmatter(string(data))), " ")
	runes := []rune(value)
	if len(runes) > limit {
		value = string(runes[:limit]) + "..."
	}
	return value, nil
}

func stripFrontmatter(value string) string {
	if !strings.HasPrefix(value, "---") {
		return value
	}
	rest := value[3:]
	if index := strings.Index(rest, "\n---"); index >= 0 {
		return strings.TrimLeft(rest[index+4:], "\n")
	}
	return value
}

func yamlValue(value string) string {
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}
