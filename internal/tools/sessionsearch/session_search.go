package sessionsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/store"
)

type searchArgs struct {
	Query string `json:"query,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

type sessionHit struct {
	ConversationUUID string `json:"conversation_uuid"`
	Title            string `json:"title"`
	CreatedAt        string `json:"created_at"`
	Preview          string `json:"preview,omitempty"`
}

type messageHit struct {
	ConversationUUID string `json:"conversation_uuid"`
	Title            string `json:"title"`
	Role             string `json:"role"`
	Snippet          string `json:"snippet"`
	CreatedAt        string `json:"created_at"`
}

type searchResult struct {
	Success bool   `json:"success"`
	Mode    string `json:"mode"`
	Query   string `json:"query,omitempty"`
	Results any    `json:"results"`
	Count   int    `json:"count"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

// MessageSearchResult FTS 搜索返回的结构。
type MessageSearchResult struct {
	ConversationUUID string
	Title            string
	Role             string
	Snippet          string
	CreatedAt        string
}

// MessageSearcher 实现了 FTS5 消息搜索的 store 扩展接口。
type MessageSearcher interface {
	SearchMessages(ctx context.Context, query string, limit int) ([]MessageSearchResult, error)
}

// NewHandler 返回绑定了 store 的 session_search handler。
func NewHandler(s store.Store) func(ctx context.Context, args string) (string, error) {
	return func(ctx context.Context, args string) (string, error) {
		var p searchArgs
		if err := json.Unmarshal([]byte(args), &p); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
		if p.Limit <= 0 {
			p.Limit = 5
		}
		if p.Limit > 20 {
			p.Limit = 20
		}

		query := strings.TrimSpace(p.Query)
		if query == "" {
			return listRecent(ctx, s, p.Limit)
		}
		return searchMessages(ctx, s, query, p.Limit)
	}
}

func listRecent(ctx context.Context, s store.Store, limit int) (string, error) {
	q := model.ListQuery{Page: 1, PageSize: limit}
	convs, _, err := s.ListConversations(ctx, "", q)
	if err != nil {
		return errResult("查询会话列表失败: " + err.Error()), nil
	}

	var hits []sessionHit
	for _, c := range convs {
		title := c.Title
		if title == "" {
			title = "(无标题)"
		}
		preview := ""
		msgs, _ := s.ListMessages(ctx, c.ID, 1)
		for _, m := range msgs {
			if m.Role == "user" && m.Content != "" {
				preview = truncate(m.Content, 80)
				break
			}
		}
		hits = append(hits, sessionHit{
			ConversationUUID: c.UUID,
			Title:            title,
			CreatedAt:        c.CreatedAt.Format(time.DateTime),
			Preview:          preview,
		})
	}

	r := searchResult{
		Success: true,
		Mode:    "recent",
		Results: hits,
		Count:   len(hits),
		Message: fmt.Sprintf("最近 %d 个会话。使用关键词搜索特定主题。", len(hits)),
	}
	out, _ := json.Marshal(r)
	return string(out), nil
}

func searchMessages(ctx context.Context, s store.Store, query string, limit int) (string, error) {
	searcher, ok := s.(MessageSearcher)
	if !ok {
		return searchFallback(ctx, s, query, limit)
	}

	results, err := searcher.SearchMessages(ctx, query, limit)
	if err != nil {
		return searchFallback(ctx, s, query, limit)
	}

	var hits []messageHit
	for _, r := range results {
		hits = append(hits, messageHit{
			ConversationUUID: r.ConversationUUID,
			Title:            r.Title,
			Role:             r.Role,
			Snippet:          r.Snippet,
			CreatedAt:        r.CreatedAt,
		})
	}

	res := searchResult{
		Success: true,
		Mode:    "search",
		Query:   query,
		Results: hits,
		Count:   len(hits),
	}
	if len(hits) == 0 {
		res.Message = "没有找到匹配的会话。"
	}
	out, _ := json.Marshal(res)
	return string(out), nil
}

func searchFallback(ctx context.Context, s store.Store, query string, limit int) (string, error) {
	q := model.ListQuery{Page: 1, PageSize: 200}
	convs, _, err := s.ListConversations(ctx, "", q)
	if err != nil {
		return errResult("查询会话列表失败: " + err.Error()), nil
	}

	keywords := strings.Fields(strings.ToLower(query))
	var hits []messageHit

	for _, c := range convs {
		if len(hits) >= limit {
			break
		}
		msgs, _ := s.ListMessages(ctx, c.ID, 50)
		for _, m := range msgs {
			if len(hits) >= limit {
				break
			}
			if m.Content == "" {
				continue
			}
			lower := strings.ToLower(m.Content)
			matched := false
			for _, kw := range keywords {
				if strings.Contains(lower, kw) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
			title := c.Title
			if title == "" {
				title = "(无标题)"
			}
			hits = append(hits, messageHit{
				ConversationUUID: c.UUID,
				Title:            title,
				Role:             m.Role,
				Snippet:          snippetAround(m.Content, keywords, 120),
				CreatedAt:        m.CreatedAt.Format(time.DateTime),
			})
		}
	}

	res := searchResult{
		Success: true,
		Mode:    "search",
		Query:   query,
		Results: hits,
		Count:   len(hits),
	}
	if len(hits) == 0 {
		res.Message = "没有找到匹配的会话。"
	}
	out, _ := json.Marshal(res)
	return string(out), nil
}

func snippetAround(content string, keywords []string, maxLen int) string {
	lower := strings.ToLower(content)
	bestPos := 0
	for _, kw := range keywords {
		if pos := strings.Index(lower, kw); pos >= 0 {
			bestPos = pos
			break
		}
	}
	start := max(0, bestPos-maxLen/2)
	end := min(len(content), start+maxLen)
	if end-start < maxLen {
		start = max(0, end-maxLen)
	}
	snippet := content[start:end]
	prefix := ""
	suffix := ""
	if start > 0 {
		prefix = "..."
	}
	if end < len(content) {
		suffix = "..."
	}
	return prefix + snippet + suffix
}

func truncate(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n]) + "..."
}

func errResult(msg string) string {
	r := searchResult{Success: false, Error: msg}
	out, _ := json.Marshal(r)
	return string(out)
}
