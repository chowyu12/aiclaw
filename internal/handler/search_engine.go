package handler

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/store"
	"github.com/chowyu12/aiclaw/internal/tools/websearch"
	"github.com/chowyu12/aiclaw/pkg/httputil"
)

const (
	defaultSearchEngineProvider = model.SearchEngineTavily
	defaultTavilyBaseURL        = "https://api.tavily.com/search"
	defaultSerpAPIBaseURL       = "https://serpapi.com/search.json"
	defaultAliyunIQSBaseURL     = "https://cloud-iqs.aliyuncs.com/search/unified"
)

type SearchEngineHandler struct {
	store store.Store
}

func NewSearchEngineHandler(s store.Store) *SearchEngineHandler {
	return &SearchEngineHandler{store: s}
}

func (h *SearchEngineHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/search-engines", h.List)
	mux.HandleFunc("POST /api/v1/search-engines", h.Create)
	mux.HandleFunc("POST /api/v1/search-engines/test", h.TestConfig)
	mux.HandleFunc("GET /api/v1/search-engines/{id}", h.Get)
	mux.HandleFunc("PUT /api/v1/search-engines/{id}", h.Update)
	mux.HandleFunc("DELETE /api/v1/search-engines/{id}", h.Delete)
	mux.HandleFunc("POST /api/v1/search-engines/{id}/test", h.Test)
}

func (h *SearchEngineHandler) List(w http.ResponseWriter, r *http.Request) {
	q := ParseListQuery(r)
	items, total, err := h.store.ListSearchEngineConfigs(r.Context(), q)
	if err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	resp := make([]model.SearchEngineConfigResp, 0, len(items))
	for _, item := range items {
		resp = append(resp, responseForSearchEngine(item))
	}
	httputil.OKList(w, resp, total)
}

func (h *SearchEngineHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := searchEngineIDFromRequest(r)
	if err != nil {
		httputil.BadRequest(w, "invalid id")
		return
	}
	cfg, err := h.store.GetSearchEngineConfig(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		httputil.NotFound(w, "search engine not found")
		return
	}
	if err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, responseForSearchEngine(cfg))
}

func (h *SearchEngineHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req model.CreateSearchEngineConfigReq
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	cfg := buildNewSearchEngineConfig(req)
	if err := validateSearchEngineConfig(cfg); err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}
	if err := h.store.CreateSearchEngineConfig(r.Context(), cfg); err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, responseForSearchEngine(cfg))
}

func (h *SearchEngineHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := searchEngineIDFromRequest(r)
	if err != nil {
		httputil.BadRequest(w, "invalid id")
		return
	}
	var req model.UpdateSearchEngineConfigReq
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	existing, err := h.store.GetSearchEngineConfig(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		httputil.NotFound(w, "search engine not found")
		return
	}
	if err != nil {
		httputil.InternalError(w, err.Error())
		return
	}

	cfg := buildUpdatedSearchEngineConfig(existing, req)
	if err := validateSearchEngineConfig(cfg); err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}
	if err := h.store.UpdateSearchEngineConfig(r.Context(), id, cfg); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httputil.NotFound(w, "search engine not found")
			return
		}
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, responseForSearchEngine(cfg))
}

func (h *SearchEngineHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := searchEngineIDFromRequest(r)
	if err != nil {
		httputil.BadRequest(w, "invalid id")
		return
	}
	if err := h.store.DeleteSearchEngineConfig(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httputil.NotFound(w, "search engine not found")
			return
		}
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, nil)
}

func (h *SearchEngineHandler) Test(w http.ResponseWriter, r *http.Request) {
	id, err := searchEngineIDFromRequest(r)
	if err != nil {
		httputil.BadRequest(w, "invalid id")
		return
	}
	var req model.TestSearchEngineReq
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Query) == "" {
		httputil.BadRequest(w, "query is required")
		return
	}
	cfg, err := h.store.GetSearchEngineConfig(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		httputil.NotFound(w, "search engine not found")
		return
	}
	if err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	cfg = cloneSearchEngineForTest(cfg)
	resp, err := websearch.Search(r.Context(), cfg, req.Query, req.Limit)
	if err != nil {
		httputil.Error(w, http.StatusBadGateway, "搜索失败: "+err.Error())
		return
	}
	httputil.OK(w, resp)
}

func (h *SearchEngineHandler) TestConfig(w http.ResponseWriter, r *http.Request) {
	var req model.TestSearchEngineConfigReq
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Query) == "" {
		httputil.BadRequest(w, "query is required")
		return
	}
	cfg, err := h.searchEngineConfigForTest(r.Context(), req)
	if errors.Is(err, sql.ErrNoRows) {
		httputil.NotFound(w, "search engine not found")
		return
	}
	if err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	if err := validateSearchEngineConfig(cfg); err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}
	resp, err := websearch.Search(r.Context(), cfg, req.Query, req.Limit)
	if err != nil {
		httputil.Error(w, http.StatusBadGateway, "搜索失败: "+err.Error())
		return
	}
	httputil.OK(w, resp)
}

func cloneSearchEngineForTest(cfg *model.SearchEngineConfig) *model.SearchEngineConfig {
	cp := *cfg
	cp.Enabled = true
	return &cp
}

func (h *SearchEngineHandler) searchEngineConfigForTest(ctx context.Context, req model.TestSearchEngineConfigReq) (*model.SearchEngineConfig, error) {
	if req.ID <= 0 {
		cfg := buildNewSearchEngineConfig(model.CreateSearchEngineConfigReq{
			Provider: req.Provider,
			Name:     req.Name,
			BaseURL:  req.BaseURL,
			APIKey:   req.APIKey,
			Enabled:  true,
		})
		return cfg, nil
	}

	existing, err := h.store.GetSearchEngineConfig(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	reqProvider := req.Provider
	reqName := req.Name
	reqBaseURL := req.BaseURL
	reqAPIKey := req.APIKey
	cfg := buildUpdatedSearchEngineConfig(existing, model.UpdateSearchEngineConfigReq{
		Provider: &reqProvider,
		Name:     &reqName,
		BaseURL:  &reqBaseURL,
		APIKey:   &reqAPIKey,
	})
	cfg.Enabled = true
	return cfg, nil
}

func searchEngineIDFromRequest(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

func buildNewSearchEngineConfig(req model.CreateSearchEngineConfigReq) *model.SearchEngineConfig {
	provider := req.Provider
	if provider == "" {
		provider = defaultSearchEngineProvider
	}
	cfg := &model.SearchEngineConfig{
		Provider: provider,
		Name:     strings.TrimSpace(req.Name),
		BaseURL:  strings.TrimSpace(req.BaseURL),
		APIKey:   strings.TrimSpace(req.APIKey),
		Enabled:  req.Enabled,
	}
	applySearchEngineDefaults(cfg)
	return cfg
}

func buildUpdatedSearchEngineConfig(existing *model.SearchEngineConfig, req model.UpdateSearchEngineConfigReq) *model.SearchEngineConfig {
	cfg := *existing
	oldProvider := cfg.Provider
	if req.Provider != nil {
		cfg.Provider = *req.Provider
	}
	providerChanged := req.Provider != nil && *req.Provider != oldProvider

	if req.Name != nil {
		cfg.Name = strings.TrimSpace(*req.Name)
	} else if providerChanged && cfg.Name == searchEngineName(oldProvider) {
		cfg.Name = ""
	}
	if req.BaseURL != nil {
		cfg.BaseURL = strings.TrimSpace(*req.BaseURL)
	} else if providerChanged {
		cfg.BaseURL = ""
	}
	if req.APIKey != nil {
		key := strings.TrimSpace(*req.APIKey)
		if key != "" {
			cfg.APIKey = key
		} else if providerChanged {
			cfg.APIKey = ""
		}
	} else if providerChanged {
		cfg.APIKey = ""
	}
	if req.Enabled != nil {
		cfg.Enabled = *req.Enabled
	}
	applySearchEngineDefaults(&cfg)
	return &cfg
}

func applySearchEngineDefaults(cfg *model.SearchEngineConfig) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultSearchEngineBaseURL(cfg.Provider)
	}
	if cfg.Name == "" {
		cfg.Name = searchEngineName(cfg.Provider)
	}
}

func validateSearchEngineConfig(cfg *model.SearchEngineConfig) error {
	if !isSupportedSearchEngine(cfg.Provider) {
		return errors.New("unsupported search engine provider")
	}
	if cfg.Name == "" {
		return errors.New("name is required")
	}
	if !validHTTPURL(cfg.BaseURL) {
		return errors.New("invalid base_url")
	}
	if cfg.Enabled && cfg.APIKey == "" {
		return errors.New("api_key is required when search engine is enabled")
	}
	return nil
}

func responseForSearchEngine(cfg *model.SearchEngineConfig) model.SearchEngineConfigResp {
	return model.SearchEngineConfigResp{
		ID:        cfg.ID,
		Provider:  cfg.Provider,
		Name:      cfg.Name,
		BaseURL:   cfg.BaseURL,
		APIKeySet: cfg.APIKey != "",
		Enabled:   cfg.Enabled,
		CreatedAt: cfg.CreatedAt,
		UpdatedAt: cfg.UpdatedAt,
	}
}

func isSupportedSearchEngine(provider model.SearchEngineProvider) bool {
	return provider == model.SearchEngineTavily || provider == model.SearchEngineSerpAPI || provider == model.SearchEngineAliyunIQS
}

func defaultSearchEngineBaseURL(provider model.SearchEngineProvider) string {
	switch provider {
	case model.SearchEngineAliyunIQS:
		return defaultAliyunIQSBaseURL
	case model.SearchEngineSerpAPI:
		return defaultSerpAPIBaseURL
	default:
		return defaultTavilyBaseURL
	}
}

func searchEngineName(provider model.SearchEngineProvider) string {
	switch provider {
	case model.SearchEngineAliyunIQS:
		return "Aliyun IQS"
	case model.SearchEngineSerpAPI:
		return "SerpAPI"
	default:
		return "Tavily"
	}
}

func validHTTPURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
