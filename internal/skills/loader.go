package skills

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chowyu12/aiclaw/internal/model"
)

type SkillInfo struct {
	DirName     string
	Name        string
	Description string
	Instruction string
	Version     string
	Author      string
	Slug        string
	MainFile    string
	Config      model.JSON
	Permissions model.JSON
	ToolDefs    model.JSON
}

type clawHubMeta struct {
	OwnerID     string `json:"ownerId"`
	Slug        string `json:"slug"`
	Version     string `json:"version"`
	PublishedAt int64  `json:"publishedAt"`
}

func ParseSkillDir(dirPath string) (*SkillInfo, error) {
	manifestPath := filepath.Join(dirPath, "manifest.json")
	if _, err := os.Stat(manifestPath); err == nil {
		return parseManifestFormat(dirPath)
	}

	metaPath := filepath.Join(dirPath, "_meta.json")
	if _, err := os.Stat(metaPath); err == nil {
		return parseClawHubFormat(dirPath)
	}

	skillMD := filepath.Join(dirPath, "SKILL.md")
	if _, err := os.Stat(skillMD); err == nil {
		return parseSkillMDOnly(dirPath)
	}

	return nil, fmt.Errorf("no manifest.json, _meta.json, or SKILL.md found in %s", dirPath)
}

func parseManifestFormat(dirPath string) (*SkillInfo, error) {
	data, err := os.ReadFile(filepath.Join(dirPath, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("read manifest.json: %w", err)
	}

	var manifest model.SkillManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parse manifest.json: %w", err)
	}

	info := &SkillInfo{
		DirName:     filepath.Base(dirPath),
		Name:        manifest.Name,
		Description: manifest.Description,
		Version:     manifest.Version,
		Author:      manifest.Author,
		MainFile:    manifest.Main,
	}

	skillMD := filepath.Join(dirPath, "SKILL.md")
	if content, err := os.ReadFile(skillMD); err == nil {
		_, body := parseFrontmatter(string(content))
		info.Instruction = strings.TrimSpace(body)
	}

	if manifest.Main != "" {
		if _, err := os.Stat(filepath.Join(dirPath, manifest.Main)); err != nil {
			info.MainFile = ""
		}
	}

	if len(manifest.Permissions) > 0 {
		data, _ := json.Marshal(manifest.Permissions)
		info.Permissions = model.JSON(data)
	}
	if len(manifest.Config) > 0 {
		data, _ := json.Marshal(manifest.Config)
		info.Config = model.JSON(data)
	}
	if len(manifest.Tools) > 0 {
		data, _ := json.Marshal(manifest.Tools)
		info.ToolDefs = model.JSON(data)
	}

	return info, nil
}

func parseClawHubFormat(dirPath string) (*SkillInfo, error) {
	metaData, err := os.ReadFile(filepath.Join(dirPath, "_meta.json"))
	if err != nil {
		return nil, fmt.Errorf("read _meta.json: %w", err)
	}

	var meta clawHubMeta
	if err := json.Unmarshal(metaData, &meta); err != nil {
		return nil, fmt.Errorf("parse _meta.json: %w", err)
	}

	info := &SkillInfo{
		DirName: filepath.Base(dirPath),
		Name:    meta.Slug,
		Slug:    meta.Slug,
		Version: meta.Version,
	}

	skillMD := filepath.Join(dirPath, "SKILL.md")
	if content, err := os.ReadFile(skillMD); err == nil {
		fm, body := parseFrontmatter(string(content))
		info.Instruction = strings.TrimSpace(body)
		if v, ok := fm["name"]; ok {
			info.Name = v
		}
		if v, ok := fm["description"]; ok {
			info.Description = v
		}
	}

	return info, nil
}

func parseSkillMDOnly(dirPath string) (*SkillInfo, error) {
	content, err := os.ReadFile(filepath.Join(dirPath, "SKILL.md"))
	if err != nil {
		return nil, fmt.Errorf("read SKILL.md: %w", err)
	}

	dirName := filepath.Base(dirPath)
	fm, body := parseFrontmatter(string(content))

	info := &SkillInfo{
		DirName:     dirName,
		Name:        dirName,
		Instruction: strings.TrimSpace(body),
	}
	if v, ok := fm["name"]; ok {
		info.Name = v
	}
	if v, ok := fm["description"]; ok {
		info.Description = v
	}

	return info, nil
}

// parseFrontmatter 解析 YAML frontmatter（简易实现，不依赖 yaml 库）。
func parseFrontmatter(content string) (map[string]string, string) {
	result := make(map[string]string)
	if !strings.HasPrefix(content, "---") {
		return result, content
	}

	rest := content[3:]
	endIdx := strings.Index(rest, "\n---")
	if endIdx < 0 {
		return result, content
	}

	fmBlock := rest[:endIdx]
	body := rest[endIdx+4:]

	for line := range strings.SplitSeq(fmBlock, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, val, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		val = strings.Trim(val, `"'`)
		result[key] = val
	}

	return result, body
}

func ScanAll(skillsRoot string) ([]SkillInfo, error) {
	entries, err := os.ReadDir(skillsRoot)
	if err != nil {
		return nil, fmt.Errorf("read skills dir: %w", err)
	}

	var result []SkillInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dirPath := filepath.Join(skillsRoot, entry.Name())
		info, err := ParseSkillDir(dirPath)
		if err != nil {
			continue
		}
		result = append(result, *info)
	}
	return result, nil
}

func InfoToSkill(info SkillInfo, source model.SkillSource, slug string) *model.Skill {
	if slug == "" {
		slug = info.Slug
	}
	return &model.Skill{
		Name:        info.Name,
		Description: info.Description,
		Instruction: info.Instruction,
		Source:      source,
		Slug:        slug,
		Version:     info.Version,
		Author:      info.Author,
		DirName:     info.DirName,
		MainFile:    info.MainFile,
		Config:      info.Config,
		Permissions: info.Permissions,
		ToolDefs:    info.ToolDefs,
		Enabled:     true,
	}
}
