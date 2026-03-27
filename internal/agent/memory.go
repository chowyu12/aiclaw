package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/memos"
	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/store"
)

type MemoryManager struct {
	store store.ConversationStore
	files store.FileStore
}

func NewMemoryManager(convStore store.ConversationStore, fileStore store.FileStore) *MemoryManager {
	return &MemoryManager{store: convStore, files: fileStore}
}

func memosActive(ag *model.Agent) bool {
	return ag.MemOSEnabled && ag.MemOSCfg.APIKey != ""
}

func memosClientFor(ag *model.Agent) *memos.Client {
	return memos.NewClient(ag.MemOSCfg.BaseURL, ag.MemOSCfg.APIKey)
}

// RecallMemories 从 MemOS 长期记忆中检索与 userMsg 相关的记忆。
// 如果 Agent 未启用 MemOS 或检索失败，返回空字符串。
func (m *MemoryManager) RecallMemories(ctx context.Context, userMsg string, ag *model.Agent) string {
	if !memosActive(ag) {
		return ""
	}
	cfg := ag.MemOSCfg
	client := memosClientFor(ag)
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

// StoreMemories 异步将本轮对话存入 MemOS 长期记忆。
func (m *MemoryManager) StoreMemories(userMsg, assistantMsg string, ag *model.Agent) {
	if !memosActive(ag) {
		return
	}
	cfg := ag.MemOSCfg
	client := memosClientFor(ag)
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

func (m *MemoryManager) GetOrCreateConversation(ctx context.Context, conversationUUID string, userID string) (*model.Conversation, error) {
	if conversationUUID != "" {
		conv, err := m.store.GetConversationByUUID(ctx, conversationUUID)
		if err == nil {
			if conv.UserID != "" && conv.UserID != userID {
				return nil, fmt.Errorf("conversation %s does not belong to user %s", conversationUUID, userID)
			}
			return conv, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("get conversation: %w", err)
		}
	}
	conv := &model.Conversation{
		UserID: userID,
		Title:  "New Conversation",
	}
	if err := m.store.CreateConversation(ctx, conv); err != nil {
		return nil, err
	}
	return conv, nil
}

const recentTurnsFullDetail = 3

func (m *MemoryManager) LoadHistory(ctx context.Context, conversationID int64, maxTurns int) ([]openai.ChatCompletionMessage, error) {
	msgs, err := m.store.ListMessages(ctx, conversationID, maxTurns)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, nil
	}

	turns := splitTurns(msgs)
	total := len(turns)

	var result []openai.ChatCompletionMessage
	for i, turn := range turns {
		if i >= total-recentTurnsFullDetail {
			for _, msg := range turn {
				result = append(result, toOpenAIMessage(msg))
			}
		} else {
			for _, msg := range compactTurn(turn) {
				result = append(result, toOpenAIMessage(msg))
			}
		}
	}

	result = sanitizeToolCallSequence(result)
	return result, nil
}

// sanitizeToolCallSequence ensures every assistant message with tool_calls has
// matching tool responses, and every tool response has a matching tool_call.
// Incomplete pairs are removed entirely to avoid LLM API rejection.
func sanitizeToolCallSequence(messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	if len(messages) == 0 {
		return messages
	}

	// Phase 1: collect all existing tool response IDs.
	responseIDs := make(map[string]bool)
	for _, msg := range messages {
		if msg.Role == openai.ChatMessageRoleTool && msg.ToolCallID != "" {
			responseIDs[msg.ToolCallID] = true
		}
	}

	// Phase 2: for each assistant with tool_calls, check completeness.
	// Keep track of valid (fully paired) tool_call IDs.
	validCallIDs := make(map[string]bool)
	for i := range messages {
		msg := &messages[i]
		if msg.Role != openai.ChatMessageRoleAssistant || len(msg.ToolCalls) == 0 {
			continue
		}

		complete := true
		for _, tc := range msg.ToolCalls {
			if !responseIDs[tc.ID] {
				complete = false
				break
			}
		}

		if complete {
			for _, tc := range msg.ToolCalls {
				validCallIDs[tc.ID] = true
			}
		} else {
			log.WithFields(log.Fields{
				"tool_calls_count": len(msg.ToolCalls),
				"message_index":    i,
			}).Warn("[Memory] stripping incomplete tool_calls from assistant message")
			msg.ToolCalls = nil
		}
	}

	// Phase 3: remove orphaned tool responses and empty assistant messages
	// left behind after stripping their tool_calls.
	result := messages[:0]
	for _, msg := range messages {
		if msg.Role == openai.ChatMessageRoleTool && msg.ToolCallID != "" && !validCallIDs[msg.ToolCallID] {
			log.WithField("tool_call_id", msg.ToolCallID).Debug("[Memory] dropping orphaned tool response")
			continue
		}
		if msg.Role == openai.ChatMessageRoleAssistant && len(msg.ToolCalls) == 0 && strings.TrimSpace(msg.Content) == "" {
			log.WithField("role", msg.Role).Debug("[Memory] dropping empty assistant message")
			continue
		}
		result = append(result, msg)
	}

	return result
}

func splitTurns(msgs []model.Message) [][]model.Message {
	var turns [][]model.Message
	var cur []model.Message
	for _, msg := range msgs {
		if msg.Role == "user" && len(cur) > 0 {
			turns = append(turns, cur)
			cur = nil
		}
		cur = append(cur, msg)
	}
	if len(cur) > 0 {
		turns = append(turns, cur)
	}
	return turns
}

func compactTurn(turn []model.Message) []model.Message {
	var result []model.Message
	var lastAssistant *model.Message

	for i := range turn {
		msg := &turn[i]
		if msg.Role == "user" {
			result = append(result, *msg)
		}
		if msg.Role == "assistant" && !hasToolCalls(msg.ToolCalls) && msg.Content != "" {
			lastAssistant = msg
		}
	}
	if lastAssistant != nil {
		result = append(result, *lastAssistant)
	}
	return result
}

// hasToolCalls 检查 JSON 格式的 ToolCalls 是否包含实际的工具调用。
// 处理 nil、空字节、"null"、"[]" 等情况。
func hasToolCalls(tc model.JSON) bool {
	if len(tc) == 0 {
		return false
	}
	s := string(tc)
	if s == "null" || s == "[]" {
		return false
	}
	var calls []any
	if json.Unmarshal(tc, &calls) != nil {
		return false
	}
	return len(calls) > 0
}

func toOpenAIMessage(msg model.Message) openai.ChatCompletionMessage {
	role := openai.ChatMessageRoleUser
	switch msg.Role {
	case "assistant":
		role = openai.ChatMessageRoleAssistant
	case "system":
		role = openai.ChatMessageRoleSystem
	case "tool":
		role = openai.ChatMessageRoleTool
	}
	cm := openai.ChatCompletionMessage{
		Role:       role,
		Content:    msg.Content,
		Name:       msg.Name,
		ToolCallID: msg.ToolCallID,
	}
	if hasToolCalls(msg.ToolCalls) {
		_ = json.Unmarshal(msg.ToolCalls, &cm.ToolCalls)
	}
	return cm
}

func (m *MemoryManager) SaveUserMessage(ctx context.Context, conversationID int64, content string, files []*model.File) (int64, error) {
	msgID, err := m.saveMessage(ctx, conversationID, "user", content, 0)
	if err != nil {
		return 0, err
	}
	m.linkFiles(ctx, files, conversationID, msgID)
	return msgID, nil
}

func (m *MemoryManager) SaveAssistantMessage(ctx context.Context, conversationID int64, content string, tokensUsed int) (int64, error) {
	return m.saveMessage(ctx, conversationID, "assistant", content, tokensUsed)
}

// SaveToolCallRound atomically saves one tool-call round: assistant message +
// all tool responses in a single DB transaction. On failure the entire round
// is rolled back, preventing orphaned tool_calls in the database.
func (m *MemoryManager) SaveToolCallRound(ctx context.Context, conversationID int64, assistantContent string, toolCalls []openai.ToolCall, results []ToolResult) error {
	if len(toolCalls) != len(results) {
		return fmt.Errorf("tool calls count (%d) != results count (%d)", len(toolCalls), len(results))
	}

	tcJSON, err := json.Marshal(toolCalls)
	if err != nil {
		return fmt.Errorf("marshal tool calls: %w", err)
	}

	msgs := make([]*model.Message, 0, 1+len(results))
	msgs = append(msgs, &model.Message{
		ConversationID: conversationID,
		Role:           "assistant",
		Content:        assistantContent,
		ToolCalls:      tcJSON,
	})
	for _, r := range results {
		msgs = append(msgs, &model.Message{
			ConversationID: conversationID,
			Role:           "tool",
			Content:        r.Content,
			ToolCallID:     r.ToolCallID,
			Name:           r.ToolName,
		})
	}

	return m.store.CreateMessages(ctx, msgs)
}

func (m *MemoryManager) saveMessage(ctx context.Context, conversationID int64, role, content string, tokensUsed int) (int64, error) {
	msg := &model.Message{
		ConversationID: conversationID,
		Role:           role,
		Content:        content,
		TokensUsed:     tokensUsed,
	}
	if err := m.store.CreateMessage(ctx, msg); err != nil {
		return 0, err
	}
	return msg.ID, nil
}

func (m *MemoryManager) linkFiles(ctx context.Context, files []*model.File, conversationID, messageID int64) {
	for _, f := range files {
		if f.ID == 0 {
			continue
		}
		if err := m.files.LinkFileToMessage(ctx, f.ID, conversationID, messageID); err != nil {
			log.WithFields(log.Fields{"file": f.Filename, "msg_id": messageID}).WithError(err).Warn("[Memory] link file to message failed")
		}
	}
}

func (m *MemoryManager) LinkFilesToMessage(ctx context.Context, files []*model.File, conversationID, messageID int64) {
	m.linkFiles(ctx, files, conversationID, messageID)
}

func (m *MemoryManager) AutoSetTitle(ctx context.Context, conversationID int64, userMessage string) {
	title := userMessage
	rs := []rune(title)
	if len(rs) > 50 {
		title = string(rs[:50]) + "..."
	}
	if err := m.store.UpdateConversationTitle(ctx, conversationID, title); err != nil {
		log.WithFields(log.Fields{"conv_id": conversationID, "title": title}).WithError(err).Warn("[Memory] auto set title failed")
	}
}
