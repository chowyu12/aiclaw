package skills

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/model"
)

type builtinSkillStore interface {
	ListSkills(context.Context) ([]model.Skill, error)
	UpsertSkill(context.Context, *model.Skill) error
}

// EnsureBuiltins registers the embedded skill catalog in SQLite using stable
// UUIDs. Existing enable/disable choices are preserved across application
// restarts and upgrades.
func EnsureBuiltins(ctx context.Context, store builtinSkillStore, root string) error {
	skillsRoot := filepath.Join(root, "skills")
	SyncBuiltinsToDisk(skillsRoot)
	existing, err := store.ListSkills(ctx)
	if err != nil {
		return err
	}
	enabled := make(map[string]bool, len(existing))
	for _, skill := range existing {
		if skill.Source == model.SkillSourceBuiltin {
			enabled[skill.UUID] = skill.Enabled
		}
	}
	for _, builtin := range BuiltinSkills() {
		builtin.UUID = uuid.NewSHA1(uuid.NameSpaceURL, []byte("aiclaw/builtin-skill/"+builtin.DirName)).String()
		builtin.InstallDir = filepath.Join(skillsRoot, builtin.DirName)
		if value, ok := enabled[builtin.UUID]; ok {
			builtin.Enabled = value
		}
		if err := store.UpsertSkill(ctx, &builtin); err != nil {
			return err
		}
	}
	return nil
}

// SyncBuiltinsToDisk 将编译时嵌入的内置技能文件写入指定目录（每次启动覆盖）。
func SyncBuiltinsToDisk(skillsDir string) {
	if skillsDir == "" {
		return
	}

	entries, err := fs.ReadDir(skillFS, ".")
	if err != nil {
		log.WithError(err).Error("[Skills] read embedded skills dir")
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dirName := entry.Name()
		mdPath := filepath.Join(dirName, "SKILL.md")
		data, err := skillFS.ReadFile(mdPath)
		if err != nil {
			continue
		}

		destDir := filepath.Join(skillsDir, dirName)
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			log.WithError(err).WithField("dir", destDir).Warn("[Skills] create skill dir failed")
			continue
		}

		destFile := filepath.Join(destDir, "SKILL.md")
		if err := os.WriteFile(destFile, data, 0o644); err != nil {
			log.WithError(err).WithField("file", destFile).Warn("[Skills] write SKILL.md failed")
			continue
		}
	}

	log.WithField("dir", skillsDir).Debug("[Skills] builtin skills synced to disk")
}
