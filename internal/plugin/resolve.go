package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/skills"
)

// skillMarkers identify a directory that holds a single skill.
var skillMarkers = []string{"manifest.json", "_meta.json", "SKILL.md"}

// ResolvedSkill is one skill contribution with its location inside the bundle.
type ResolvedSkill struct {
	Info skills.SkillInfo
	// RelDir is the skill directory relative to the bundle root, empty when
	// the bundle root itself is the skill.
	RelDir string
}

// Resolved is a bundle that has been fully validated against its source
// directory. Nothing is copied or persisted until a Resolved exists, so a
// rejected bundle leaves no files and no records behind.
type Resolved struct {
	Root        string
	Manifest    *Manifest
	Raw         model.JSON
	Skills      []ResolvedSkill
	Servers     map[string]MCPContribution
	Permissions []string
}

// Resolve reads and validates the bundle at root.
//
// Contribution discovery prefers explicit manifest declarations and falls back
// to directory probing, so bundles written before contribution points existed
// resolve to the same thing they did before.
func Resolve(root string) (*Resolved, error) {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("invalid plugin directory %q", root)
	}
	manifest, raw, err := LoadManifest(root)
	if err != nil {
		return nil, err
	}
	if err := manifest.checkRuntime(); err != nil {
		return nil, err
	}
	resolved := &Resolved{Root: root, Manifest: manifest, Raw: raw}
	if resolved.Skills, err = resolveSkills(root, manifest); err != nil {
		return nil, err
	}
	if resolved.Servers, err = resolveServers(root, manifest); err != nil {
		return nil, err
	}
	if err := validateNativeContributions(manifest); err != nil {
		return nil, err
	}
	skillInfos := make([]skills.SkillInfo, 0, len(resolved.Skills))
	for _, item := range resolved.Skills {
		skillInfos = append(skillInfos, item.Info)
	}
	if resolved.Permissions, err = derivePermissions(manifest, skillInfos, resolved.Servers); err != nil {
		return nil, err
	}
	return resolved, nil
}

func resolveSkills(root string, manifest *Manifest) ([]ResolvedSkill, error) {
	if len(manifest.Contributes.Skills) > 0 {
		result := make([]ResolvedSkill, 0, len(manifest.Contributes.Skills))
		for _, contribution := range manifest.Contributes.Skills {
			rel := strings.TrimSpace(contribution.Dir)
			if rel == "" {
				return nil, fmt.Errorf("skill contribution is missing %q", "dir")
			}
			dir, err := resolveInside(root, rel)
			if err != nil {
				return nil, err
			}
			info, err := skills.ParseSkillDir(dir)
			if err != nil {
				return nil, fmt.Errorf("parse skill %s: %w", rel, err)
			}
			result = append(result, ResolvedSkill{Info: *info, RelDir: filepath.Clean(rel)})
		}
		return result, nil
	}

	var result []ResolvedSkill
	if hasSkillMarker(root) {
		info, err := skills.ParseSkillDir(root)
		if err != nil {
			return nil, err
		}
		result = append(result, ResolvedSkill{Info: *info})
	}
	skillsRoot := filepath.Join(root, "skills")
	if _, err := os.Stat(skillsRoot); err != nil {
		return result, nil
	}
	found, err := skills.ScanAll(skillsRoot)
	if err != nil {
		return nil, err
	}
	for _, info := range found {
		result = append(result, ResolvedSkill{Info: info, RelDir: filepath.Join("skills", info.DirName)})
	}
	return result, nil
}

func resolveServers(root string, manifest *Manifest) (map[string]MCPContribution, error) {
	declared := manifest.Contributes.MCPServers
	if len(declared) == 0 {
		fromFile, err := loadMCPFile(root)
		if err != nil {
			return nil, err
		}
		declared = fromFile
	}
	if len(declared) == 0 {
		return nil, nil
	}
	result := make(map[string]MCPContribution, len(declared))
	for name, contribution := range declared {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("MCP server contribution is missing a name")
		}
		if strings.TrimSpace(contribution.Command) == "" && strings.TrimSpace(contribution.URL) == "" {
			return nil, fmt.Errorf("MCP server %q declares neither a command nor a url", name)
		}
		result[name] = contribution
	}
	return result, nil
}

// validateNativeContributions enforces that native tools and channels only
// come from bundled providers. A third-party bundle ships native tools over
// MCP; Go has no stable plugin ABI to load them in-process.
func validateNativeContributions(manifest *Manifest) error {
	toolNames := make(map[string]bool)
	for _, contribution := range manifest.Contributes.Tools {
		if !strings.HasPrefix(contribution.Provider, BuiltinPrefix) {
			return fmt.Errorf("native tool provider %q must use the %q prefix; ship third-party tools over MCP", contribution.Provider, BuiltinPrefix)
		}
		if len(contribution.Names) == 0 {
			return fmt.Errorf("tool contribution from %q declares no tool names", contribution.Provider)
		}
		for _, name := range contribution.Names {
			name = strings.TrimSpace(name)
			if name == "" {
				return fmt.Errorf("tool contribution from %q declares an empty tool name", contribution.Provider)
			}
			if toolNames[name] {
				return fmt.Errorf("tool %q is contributed twice by this plugin", name)
			}
			toolNames[name] = true
		}
	}
	channelIDs := make(map[string]bool)
	for _, contribution := range manifest.Contributes.Channels {
		id := strings.TrimSpace(contribution.ID)
		if id == "" {
			return fmt.Errorf("channel contribution is missing %q", "id")
		}
		if channelIDs[id] {
			return fmt.Errorf("channel %q is contributed twice by this plugin", id)
		}
		channelIDs[id] = true
		if !strings.HasPrefix(contribution.Provider, BuiltinPrefix) {
			return fmt.Errorf("channel %q must use a %q provider", id, BuiltinPrefix)
		}
	}
	return nil
}

func hasSkillMarker(dir string) bool {
	for _, marker := range skillMarkers {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	return false
}

// resolveInside joins rel onto root and refuses any path that escapes the
// bundle.
func resolveInside(root, rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("plugin path %q must be relative to the bundle root", rel)
	}
	target := filepath.Join(root, rel)
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("plugin path %q escapes the bundle root", rel)
	}
	return target, nil
}

func decodeManifest(raw model.JSON, manifest *Manifest) error {
	if err := json.Unmarshal(raw, manifest); err != nil {
		return fmt.Errorf("parse stored plugin manifest: %w", err)
	}
	return nil
}
