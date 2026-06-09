package websearch

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chowyu12/aiclaw/internal/model"
)

type testSearchEngineStore struct {
	configs     map[int64]*model.SearchEngineConfig
	requestedID int64
}

func (s *testSearchEngineStore) ListSearchEngineConfigs(context.Context, model.ListQuery) ([]*model.SearchEngineConfig, int64, error) {
	return nil, 0, nil
}

func (s *testSearchEngineStore) GetSearchEngineConfig(_ context.Context, id int64) (*model.SearchEngineConfig, error) {
	s.requestedID = id
	cfg := s.configs[id]
	if cfg == nil {
		return nil, sql.ErrNoRows
	}
	cp := *cfg
	return &cp, nil
}

func (s *testSearchEngineStore) CreateSearchEngineConfig(context.Context, *model.SearchEngineConfig) error {
	return nil
}

func (s *testSearchEngineStore) UpdateSearchEngineConfig(context.Context, int64, *model.SearchEngineConfig) error {
	return nil
}

func (s *testSearchEngineStore) DeleteSearchEngineConfig(context.Context, int64) error {
	return nil
}

func TestSearchTavily(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"results":[{"title":"A","url":"https://example.com/a","content":"Alpha"}]}`))
	}))
	defer srv.Close()

	cfg := &model.SearchEngineConfig{
		Provider: model.SearchEngineTavily,
		BaseURL:  srv.URL,
		APIKey:   "secret",
		Enabled:  true,
	}
	resp, err := Search(context.Background(), cfg, "latest go release", 3)
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if gotPath != "/search" {
		t.Fatalf("path = %q, want /search", gotPath)
	}
	if gotBody["api_key"] != "secret" || gotBody["query"] != "latest go release" {
		t.Fatalf("unexpected request body: %#v", gotBody)
	}
	if len(resp.Results) != 1 || resp.Results[0].Snippet != "Alpha" {
		t.Fatalf("unexpected response: %#v", resp)
	}
}

func TestCleanSnippetTruncatesLargeText(t *testing.T) {
	input := strings.Repeat("界", maxSnippetRunes+5)
	got := cleanSnippet(input)
	if !strings.HasSuffix(got, "...[truncated]") {
		t.Fatalf("snippet should be marked truncated, got suffix: %q", got[len(got)-20:])
	}
	if len([]rune(got)) != maxSnippetRunes+len([]rune("...[truncated]")) {
		t.Fatalf("unexpected truncated length: %d", len([]rune(got)))
	}
}

func TestSearchSerpAPI(t *testing.T) {
	var gotPath string
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"organic_results":[{"title":"B","link":"https://example.com/b","snippet":"Beta"}]}`))
	}))
	defer srv.Close()

	cfg := &model.SearchEngineConfig{
		Provider: model.SearchEngineSerpAPI,
		BaseURL:  srv.URL,
		APIKey:   "secret",
		Enabled:  true,
	}
	resp, err := Search(context.Background(), cfg, "weather", 20)
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if gotPath != "/search.json" {
		t.Fatalf("path = %q, want /search.json", gotPath)
	}
	if !strings.Contains(gotQuery, "q=weather") || !strings.Contains(gotQuery, "num=10") {
		t.Fatalf("unexpected query: %s", gotQuery)
	}
	if len(resp.Results) != 1 || resp.Results[0].URL != "https://example.com/b" {
		t.Fatalf("unexpected response: %#v", resp)
	}
}

func TestSearchAliyunIQS(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"pageItems":[{"title":"杭州美食","link":"https://example.com/food","snippet":"西湖醋鱼"}]}`))
	}))
	defer srv.Close()

	cfg := &model.SearchEngineConfig{
		Provider: model.SearchEngineAliyunIQS,
		BaseURL:  srv.URL + "/search/unified",
		APIKey:   "API-secret",
		Enabled:  true,
	}
	resp, err := Search(context.Background(), cfg, "杭州美食", 5)
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if gotPath != "/search/unified" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer API-secret" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotBody["query"] != "杭州美食" || gotBody["engineType"] != "LiteAdvanced" {
		t.Fatalf("unexpected request body: %#v", gotBody)
	}
	contents, ok := gotBody["contents"].(map[string]any)
	if !ok || contents["mainText"] != true || contents["markdownText"] != false || contents["summary"] != false || contents["rerankScore"] != true {
		t.Fatalf("unexpected contents: %#v", gotBody["contents"])
	}
	advancedParams, ok := gotBody["advancedParams"].(map[string]any)
	if !ok || advancedParams["numResults"] != float64(5) {
		t.Fatalf("unexpected advancedParams: %#v", gotBody["advancedParams"])
	}
	if len(resp.Results) != 1 || resp.Results[0].Title != "杭州美食" {
		t.Fatalf("unexpected response: %#v", resp)
	}
}

func TestSearchRequiresEnabledConfig(t *testing.T) {
	cfg := &model.SearchEngineConfig{Provider: model.SearchEngineTavily, APIKey: "secret"}
	if _, err := Search(context.Background(), cfg, "x", 1); err == nil {
		t.Fatal("expected disabled config error")
	}
}

func TestNewHandlerUsesSelectedSearchEngineID(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"results":[{"title":"A","url":"https://example.com/a","content":"Alpha"}]}`))
	}))
	defer srv.Close()

	store := &testSearchEngineStore{configs: map[int64]*model.SearchEngineConfig{
		7: {
			Provider: model.SearchEngineTavily,
			BaseURL:  srv.URL,
			APIKey:   "secret",
			Enabled:  true,
		},
	}}
	handler := NewHandler(store)

	if _, err := handler(context.Background(), `{"query":"go"}`); err == nil || !strings.Contains(err.Error(), "search_engine_id is not selected") {
		t.Fatalf("expected missing search_engine_id error, got %v", err)
	}

	out, err := handler(WithSearchEngineID(context.Background(), 7), `{"query":"go","limit":3}`)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if store.requestedID != 7 {
		t.Fatalf("requested search engine id = %d, want 7", store.requestedID)
	}
	if gotBody["query"] != "go" || gotBody["max_results"] != float64(3) {
		t.Fatalf("unexpected request body: %#v", gotBody)
	}
	if !strings.Contains(out, `"provider": "tavily"`) {
		t.Fatalf("unexpected output: %s", out)
	}
}
