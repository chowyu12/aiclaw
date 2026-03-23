package memos

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

const DefaultBaseURL = "https://memos.memtensor.cn/api/openmem/v1"

type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func NewClient(baseURL, apiKey string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ---------- Search ----------

type SearchRequest struct {
	Query            string `json:"query"`
	UserID           string `json:"user_id"`
	TopK             int    `json:"top_k,omitempty"`
	IncludePreference bool  `json:"include_preference"`
	SearchToolMemory bool   `json:"search_tool_memory"`
}

type SearchResponse struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type MemoryItem struct {
	ID      string `json:"id"`
	Memory  string `json:"memory"`
	Score   float64 `json:"score"`
	Type    string `json:"type"`
}

type SearchResult struct {
	Memories    []MemoryItem `json:"memories"`
	Preferences []MemoryItem `json:"preferences"`
}

func (c *Client) Search(ctx context.Context, query, userID string, topK int) (*SearchResult, error) {
	if topK <= 0 {
		topK = 10
	}
	body := SearchRequest{
		Query:            query,
		UserID:           userID,
		TopK:             topK,
		IncludePreference: true,
		SearchToolMemory: false,
	}

	resp, err := c.doPost(ctx, "/product/search", body)
	if err != nil {
		return nil, fmt.Errorf("memos search: %w", err)
	}

	var sr SearchResponse
	if err := json.Unmarshal(resp, &sr); err != nil {
		return nil, fmt.Errorf("memos search decode: %w", err)
	}
	if sr.Code != 0 && sr.Code != 200 {
		return nil, fmt.Errorf("memos search error: %s", sr.Message)
	}

	var result SearchResult
	if len(sr.Data) > 0 {
		_ = json.Unmarshal(sr.Data, &result)
	}
	return &result, nil
}

// ---------- Add ----------

type AddMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type AddRequest struct {
	UserID    string       `json:"user_id"`
	Messages  []AddMessage `json:"messages"`
	AsyncMode string       `json:"async_mode"`
}

func (c *Client) Add(ctx context.Context, userID, userMsg, assistantMsg string, async bool) error {
	mode := "async"
	if !async {
		mode = "sync"
	}
	body := AddRequest{
		UserID: userID,
		Messages: []AddMessage{
			{Role: "user", Content: userMsg},
			{Role: "assistant", Content: assistantMsg},
		},
		AsyncMode: mode,
	}

	resp, err := c.doPost(ctx, "/product/add", body)
	if err != nil {
		return fmt.Errorf("memos add: %w", err)
	}

	var sr SearchResponse
	if err := json.Unmarshal(resp, &sr); err != nil {
		return fmt.Errorf("memos add decode: %w", err)
	}
	if sr.Code != 0 && sr.Code != 200 {
		return fmt.Errorf("memos add error: %s", sr.Message)
	}
	return nil
}

// ---------- Delete ----------

type DeleteRequest struct {
	MemoryIDs []string `json:"memory_ids"`
}

func (c *Client) Delete(ctx context.Context, memoryIDs []string) error {
	body := DeleteRequest{MemoryIDs: memoryIDs}

	resp, err := c.doPost(ctx, "/delete/memory", body)
	if err != nil {
		return fmt.Errorf("memos delete: %w", err)
	}

	var sr SearchResponse
	if err := json.Unmarshal(resp, &sr); err != nil {
		return fmt.Errorf("memos delete decode: %w", err)
	}
	if sr.Code != 0 && sr.Code != 200 {
		return fmt.Errorf("memos delete error: %s", sr.Message)
	}
	return nil
}

// ---------- Format ----------

func FormatMemories(sr *SearchResult) string {
	if sr == nil {
		return ""
	}
	var parts []string
	for _, m := range sr.Memories {
		if m.Memory != "" {
			parts = append(parts, "- "+m.Memory)
		}
	}
	for _, p := range sr.Preferences {
		if p.Memory != "" {
			parts = append(parts, "- [偏好] "+p.Memory)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n")
}

// ---------- HTTP ----------

func (c *Client) doPost(ctx context.Context, path string, body any) ([]byte, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Token "+c.apiKey)

	l := log.WithFields(log.Fields{"url": c.baseURL + path})
	l.Debug("[MemOS] >> request")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	l.WithField("status", resp.StatusCode).Debug("[MemOS] << response")
	return respBody, nil
}
