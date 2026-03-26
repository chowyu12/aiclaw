package skills

import (
	"embed"
	"io/fs"
	"path/filepath"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/model"
)

//go:embed */SKILL.md
var skillFS embed.FS

// BuiltinSkills 从编译时嵌入的 SKILL.md 文件解析并返回所有内置技能。
func BuiltinSkills() []model.Skill {
	var result []model.Skill

	entries, err := fs.ReadDir(skillFS, ".")
	if err != nil {
		log.WithError(err).Error("read embedded skills dir")
		return nil
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dirName := entry.Name()
		mdPath := filepath.Join(dirName, "SKILL.md")
		data, err := skillFS.ReadFile(mdPath)
		if err != nil {
			log.WithFields(log.Fields{"dir": dirName, "error": err}).Warn("read embedded SKILL.md failed")
			continue
		}

		fm, body := parseFrontmatter(string(data))
		name := fm["name"]
		if name == "" {
			name = dirName
		}

		result = append(result, model.Skill{
			Name:        name,
			Description: fm["description"],
			Instruction: strings.TrimSpace(body),
			Source:      model.SkillSourceLocal,
			DirName:     dirName,
			Version:     "1.0.0",
			Author:      "system",
			Enabled:     true,
		})
	}

	return result
}
