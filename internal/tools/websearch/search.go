package websearch

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/store"
)

const (
	defaultTavilyURL  = "https://api.tavily.com/search"
	defaultSerpAPIURL = "https://serpapi.com/search.json"
	defaultAliyunURL  = "https://cloud-iqs.aliyuncs.com/search/unified"
	searchTimeout     = 20 * time.Second
	maxSearchResults  = 10
	maxResponseBytes  = 4 * 1024 * 1024
	maxSnippetRunes   = 4000
)

var httpClient = &http.Client{
	Timeout: searchTimeout,
	Transport: &http.Transport{
		MaxIdleConns:        32,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     90 * time.Second,
	},
}

type searchEngineIDKey struct{}

type Args struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

type SearchResponse struct {
	Query    string         `json:"query"`
	Provider string         `json:"provider"`
	Results  []SearchResult `json:"results"`
}

func WithSearchEngineID(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, searchEngineIDKey{}, id)
}

func SearchEngineIDFromContext(ctx context.Context) int64 {
	if id, ok := ctx.Value(searchEngineIDKey{}).(int64); ok {
		return id
	}
	return 0
}

func NewHandler(s store.SearchEngineStore) func(context.Context, string) (string, error) {
	return func(ctx context.Context, args string) (string, error) {
		var req Args
		if err := json.Unmarshal([]byte(args), &req); err != nil {
			return "", fmt.Errorf("invalid web_search args: %w", err)
		}
		searchEngineID := SearchEngineIDFromContext(ctx)
		if searchEngineID <= 0 {
			return "", errors.New("web_search search_engine_id is not selected")
		}
		cfg, err := s.GetSearchEngineConfig(ctx, searchEngineID)
		if errors.Is(err, sql.ErrNoRows) {
			return "", errors.New("selected web_search engine is not configured")
		}
		if err != nil {
			return "", err
		}
		resp, err := Search(ctx, cfg, req.Query, req.Limit)
		if err != nil {
			return "", err
		}
		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
}

func Search(ctx context.Context, cfg *model.SearchEngineConfig, query string, limit int) (*SearchResponse, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("query is required")
	}
	if cfg == nil || !cfg.Enabled {
		return nil, errors.New("web_search is not enabled")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("web_search api_key is not configured")
	}
	limit = normalizeLimit(limit)

	switch cfg.Provider {
	case model.SearchEngineTavily:
		return searchTavily(ctx, cfg, query, limit)
	case model.SearchEngineSerpAPI:
		return searchSerpAPI(ctx, cfg, query, limit)
	case model.SearchEngineAliyunIQS:
		return searchAliyunIQS(ctx, cfg, query, limit)
	default:
		return nil, fmt.Errorf("unsupported search engine provider: %s", cfg.Provider)
	}
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return 5
	}
	if limit > maxSearchResults {
		return maxSearchResults
	}
	return limit
}

func searchTavily(ctx context.Context, cfg *model.SearchEngineConfig, query string, limit int) (*SearchResponse, error) {
	endpoint, err := tavilyEndpoint(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{
		"api_key":        cfg.APIKey,
		"query":          query,
		"max_results":    limit,
		"search_depth":   "basic",
		"include_answer": false,
	}
	body, err := postJSON(ctx, endpoint, payload)
	if err != nil {
		return nil, err
	}

	var parsed struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
			Snippet string `json:"snippet"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse tavily response: %w", err)
	}

	resp := &SearchResponse{Query: query, Provider: string(cfg.Provider)}
	for _, item := range parsed.Results {
		snippet := item.Snippet
		if snippet == "" {
			snippet = item.Content
		}
		resp.Results = append(resp.Results, SearchResult{
			Title:   strings.TrimSpace(item.Title),
			URL:     strings.TrimSpace(item.URL),
			Snippet: cleanSnippet(snippet),
		})
	}
	return resp, nil
}

func searchSerpAPI(ctx context.Context, cfg *model.SearchEngineConfig, query string, limit int) (*SearchResponse, error) {
	endpoint, err := serpAPIEndpoint(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	if q.Get("engine") == "" {
		q.Set("engine", "google")
	}
	q.Set("q", query)
	q.Set("api_key", cfg.APIKey)
	q.Set("num", fmt.Sprintf("%d", limit))
	u.RawQuery = q.Encode()

	body, err := get(ctx, u.String())
	if err != nil {
		return nil, err
	}

	var parsed struct {
		OrganicResults []struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Snippet string `json:"snippet"`
		} `json:"organic_results"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse serpapi response: %w", err)
	}

	resp := &SearchResponse{Query: query, Provider: string(cfg.Provider)}
	for _, item := range parsed.OrganicResults {
		resp.Results = append(resp.Results, SearchResult{
			Title:   strings.TrimSpace(item.Title),
			URL:     strings.TrimSpace(item.Link),
			Snippet: cleanSnippet(item.Snippet),
		})
	}
	return resp, nil
}

func searchAliyunIQS(ctx context.Context, cfg *model.SearchEngineConfig, query string, limit int) (*SearchResponse, error) {
	endpoint, err := aliyunIQSEndpoint(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{
		"query":      query,
		"engineType": "LiteAdvanced",
		"contents": map[string]any{
			"mainText":     true,
			"markdownText": false,
			"summary":      false,
			"rerankScore":  true,
		},
		"advancedParams": map[string]any{
			"numResults": limit,
		},
	}
	body, err := postAliyunJSON(ctx, endpoint, cfg.APIKey, payload)
	if err != nil {
		return nil, err
	}

	var parsed struct {
		PageItems []struct {
			Title        string `json:"title"`
			Link         string `json:"link"`
			Snippet      string `json:"snippet"`
			MainText     string `json:"mainText"`
			MarkdownText string `json:"markdownText"`
			Summary      string `json:"summary"`
		} `json:"pageItems"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse aliyun iqs response: %w", err)
	}

	resp := &SearchResponse{Query: query, Provider: string(cfg.Provider)}
	for _, item := range parsed.PageItems {
		snippet := item.Snippet
		if snippet == "" {
			snippet = item.Summary
		}
		if snippet == "" {
			snippet = item.MainText
		}
		if snippet == "" {
			snippet = item.MarkdownText
		}
		resp.Results = append(resp.Results, SearchResult{
			Title:   strings.TrimSpace(item.Title),
			URL:     strings.TrimSpace(item.Link),
			Snippet: cleanSnippet(snippet),
		})
	}
	return resp, nil
}

func tavilyEndpoint(baseURL string) (string, error) {
	return endpointWithPath(baseURL, defaultTavilyURL, "search")
}

func serpAPIEndpoint(baseURL string) (string, error) {
	return endpointWithPath(baseURL, defaultSerpAPIURL, "search.json")
}

func aliyunIQSEndpoint(baseURL string) (string, error) {
	raw := strings.TrimSpace(baseURL)
	if raw == "" {
		raw = defaultAliyunURL
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid aliyun iqs base_url: %s", baseURL)
	}
	return u.String(), nil
}

func endpointWithPath(baseURL, defaultURL, defaultPath string) (string, error) {
	raw := strings.TrimSpace(baseURL)
	if raw == "" {
		raw = defaultURL
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid search engine base_url: %s", baseURL)
	}
	path := strings.TrimRight(u.Path, "/")
	if path == "" {
		u.Path = "/" + defaultPath
	} else if !strings.HasSuffix(path, "/"+defaultPath) && !strings.HasSuffix(path, defaultPath) {
		u.Path = path + "/" + defaultPath
	} else {
		u.Path = path
	}
	return u.String(), nil
}

func postJSON(ctx context.Context, endpoint string, payload any) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return do(req)
}

func postAliyunJSON(ctx context.Context, endpoint, apiKey string, payload any) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", aliyunAuthorization(apiKey))
	return do(req)
}

func aliyunAuthorization(apiKey string) string {
	apiKey = strings.TrimSpace(apiKey)
	if strings.HasPrefix(strings.ToLower(apiKey), "bearer ") {
		return apiKey
	}
	return "Bearer " + apiKey
}

func get(ctx context.Context, endpoint string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	return do(req)
}

func do(req *http.Request) ([]byte, error) {
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxResponseBytes {
		return nil, fmt.Errorf("search engine response exceeds %d bytes", maxResponseBytes)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("search engine returned HTTP %d: %s", resp.StatusCode, truncateBody(body))
	}
	return body, nil
}

func cleanSnippet(s string) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= maxSnippetRunes {
		return s
	}
	return string(runes[:maxSnippetRunes]) + "...[truncated]"
}

func truncateBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) > 500 {
		return text[:500] + "..."
	}
	return text
}
