package skills

import (
	"io/fs"
	"os"
	"path/filepath"

	log "github.com/sirupsen/logrus"
)

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
