package agent

import (
	"context"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/memos"
	"github.com/chowyu12/aiclaw/internal/model"
)

// recallMemories 从 MemOS 长期记忆中检索与 userMsg 相关的记忆片段。
// Agent 未启用 MemOS 或检索失败时返回空字符串，不影响主流程。
func recallMemories(ctx context.Context, userMsg string, ag *model.Agent) string {
	if !memosActive(ag) {
		return ""
	}
	cfg := ag.MemOSCfg
	client := memos.NewClient(cfg.BaseURL, cfg.APIKey)
	result, err := client.Search(ctx, userMsg, cfg.EffectiveUserID(), cfg.EffectiveTopK())
	if err != nil {
		log.WithError(err).Warn("[MemOS] recall failed, continuing without memories")
		return ""
	}
	formatted := memos.FormatMemories(result)
	if formatted != "" {
		log.WithField("items", len(result.Memories)+len(result.Preferences)).Info("[MemOS] recalled memories")
	}
	return formatted
}

// storeMemories 异步将本轮对话存入 MemOS 长期记忆。
func storeMemories(userMsg, assistantMsg string, ag *model.Agent) {
	if !memosActive(ag) {
		return
	}
	cfg := ag.MemOSCfg
	client := memos.NewClient(cfg.BaseURL, cfg.APIKey)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := client.Add(ctx, cfg.EffectiveUserID(), userMsg, assistantMsg, cfg.Async); err != nil {
			log.WithError(err).Warn("[MemOS] add failed")
		} else {
			log.Debug("[MemOS] conversation added")
		}
	}()
}

func memosActive(ag *model.Agent) bool {
	return ag.MemOSEnabled && ag.MemOSCfg.APIKey != ""
}
