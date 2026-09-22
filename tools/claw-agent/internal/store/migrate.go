package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// legacyFile 是旧版「一个会话一个 JSON 文件」的形状。
//
// 只保留搬家需要的字段：config 与 messages 原样转存，store 不解析它们。
type legacyFile struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	CreatedAt time.Time       `json:"createdAt"`
	UpdatedAt time.Time       `json:"updatedAt"`
	Config    json.RawMessage `json:"config"`
	Messages  json.RawMessage `json:"messages"`
	TurnCount int             `json:"turnCount"`
}

// ImportLegacy 把旧的 sessions/*.json 搬进库里，返回搬了几个。
//
// 只搬库里还没有的：重复跑不会覆盖用户后来的改动。
// 搬完**不删原文件**——万一搬错了还能翻回去，而且它们占不了多少地方。
// 目录不存在（全新安装）时直接返回 0。
//
// 错在单个文件上不中断整体：一个坏文件不该让其余会话都进不来。
func ImportLegacy(ctx context.Context, s *Store, dataHome string) (int, error) {
	dir := filepath.Join(dataHome, "sessions")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("读取旧会话目录失败：%w", err)
	}

	imported := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var legacy legacyFile
		if json.Unmarshal(raw, &legacy) != nil || legacy.ID == "" {
			continue
		}
		if _, err := s.Load(ctx, legacy.ID); err == nil {
			continue // 已经在库里了，别覆盖
		}
		if err := s.Save(ctx, Session{
			ID:        legacy.ID,
			Title:     legacy.Title,
			CreatedAt: legacy.CreatedAt,
			UpdatedAt: legacy.UpdatedAt,
			Workdir:   legacyWorkdir(legacy.Config),
			Model:     legacyModel(legacy.Config),
			TurnCount: legacy.TurnCount,
			Config:    legacy.Config,
			Messages:  legacy.Messages,
		}); err != nil {
			continue
		}
		imported++
	}
	return imported, nil
}

// 旧文件里 workdir 和 model 埋在 config 中；列表要按列查，所以搬家时提出来。
func legacyWorkdir(config json.RawMessage) string {
	return legacyField(config).Workdir
}

func legacyModel(config json.RawMessage) string {
	return legacyField(config).Model.Model
}

func legacyField(config json.RawMessage) struct {
	Workdir string `json:"workdir"`
	Model   struct {
		Model string `json:"model"`
	} `json:"model"`
} {
	var parsed struct {
		Workdir string `json:"workdir"`
		Model   struct {
			Model string `json:"model"`
		} `json:"model"`
	}
	_ = json.Unmarshal(config, &parsed)
	return parsed
}
