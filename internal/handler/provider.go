package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/provider"
	"github.com/chowyu12/aiclaw/internal/store"
	"github.com/chowyu12/aiclaw/pkg/httputil"
)

type ProviderHandler struct {
	store store.Store
}

func resolveDefaultBaseURL(providerType model.ProviderType, baseURL string) string {
	if baseURL != "" {
		return baseURL
	}
	return provider.DefaultBaseURLs[providerType]
}

func NewProviderHandler(s store.Store) *ProviderHandler {
	return &ProviderHandler{store: s}
}

func (h *ProviderHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/providers", h.Create)
	mux.HandleFunc("GET /api/v1/providers", h.List)
	mux.HandleFunc("GET /api/v1/providers/{id}", h.Get)
	mux.HandleFunc("PUT /api/v1/providers/{id}", h.Update)
	mux.HandleFunc("DELETE /api/v1/providers/{id}", h.Delete)
	mux.HandleFunc("GET /api/v1/providers/{id}/models", h.ListModels)
	mux.HandleFunc("GET /api/v1/providers/{id}/models/remote", h.FetchRemoteModels)
	mux.HandleFunc("POST /api/v1/providers/models/remote", h.FetchRemoteModelsByConfig)
}

func (h *ProviderHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req model.CreateProviderReq
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	p := &model.Provider{
		Name:    req.Name,
		Type:    req.Type,
		BaseURL: resolveDefaultBaseURL(req.Type, req.BaseURL),
		APIKey:  req.APIKey,
		Models:  req.Models,
		Enabled: true,
	}
	if req.Enabled != nil {
		p.Enabled = *req.Enabled
	}
	if err := h.store.CreateProvider(r.Context(), p); err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, p)
}

func (h *ProviderHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httputil.BadRequest(w, "invalid id")
		return
	}
	p, err := h.store.GetProvider(r.Context(), id)
	if err != nil {
		httputil.NotFound(w, "provider not found")
		return
	}
	httputil.OK(w, p)
}

func (h *ProviderHandler) List(w http.ResponseWriter, r *http.Request) {
	q := ParseListQuery(r)
	list, total, err := h.store.ListProviders(r.Context(), q)
	if err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OKList(w, list, total)
}

func (h *ProviderHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httputil.BadRequest(w, "invalid id")
		return
	}
	var req model.UpdateProviderReq
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if err := h.store.UpdateProvider(r.Context(), id, req); err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, nil)
}

func (h *ProviderHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httputil.BadRequest(w, "invalid id")
		return
	}
	if err := h.store.DeleteProvider(r.Context(), id); err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, nil)
}

func (h *ProviderHandler) ListModels(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httputil.BadRequest(w, "invalid id")
		return
	}
	p, err := h.store.GetProvider(r.Context(), id)
	if err != nil {
		httputil.NotFound(w, "provider not found")
		return
	}
	var models []string
	if len(p.Models) > 0 {
		json.Unmarshal(p.Models, &models)
	}
	if models == nil {
		models = defaultModelsByType(p.Type)
	}
	httputil.OK(w, models)
}

func (h *ProviderHandler) FetchRemoteModels(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httputil.BadRequest(w, "invalid id")
		return
	}
	p, err := h.store.GetProvider(r.Context(), id)
	if err != nil {
		httputil.NotFound(w, "provider not found")
		return
	}
	models, err := provider.FetchRemoteModels(r.Context(), p)
	if err != nil {
		httputil.Error(w, http.StatusBadGateway, "拉取远程模型列表失败: "+err.Error())
		return
	}
	httputil.OK(w, models)
}

func (h *ProviderHandler) FetchRemoteModelsByConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type    model.ProviderType `json:"type"`
		BaseURL string             `json:"base_url"`
		APIKey  string             `json:"api_key"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if req.APIKey == "" {
		httputil.BadRequest(w, "api_key is required")
		return
	}
	p := &model.Provider{Type: req.Type, BaseURL: req.BaseURL, APIKey: req.APIKey}
	models, err := provider.FetchRemoteModels(r.Context(), p)
	if err != nil {
		httputil.Error(w, http.StatusBadGateway, "拉取远程模型列表失败: "+err.Error())
		return
	}
	httputil.OK(w, models)
}

func defaultModelsByType(providerType model.ProviderType) []string {
	switch providerType {
	case model.ProviderOpenAI:
		return []string{
			"gpt-4o", "gpt-4o-mini", "gpt-4-turbo", "gpt-4",
			"gpt-3.5-turbo", "o1", "o1-mini", "o3-mini",
		}
	case model.ProviderQwen:
		return []string{
			"qwen-max", "qwen-plus", "qwen-turbo", "qwen-long",
			"qwen-vl-max", "qwen-vl-plus",
			"qwen2.5-72b-instruct", "qwen2.5-32b-instruct", "qwen2.5-14b-instruct", "qwen2.5-7b-instruct",
		}
	case model.ProviderKimi:
		return []string{
			"moonshot-v1-128k", "moonshot-v1-32k", "moonshot-v1-8k",
		}
	case model.ProviderOpenRouter:
		return []string{
			"anthropic/claude-sonnet-4-20250514",
			"openai/gpt-4o", "openai/gpt-4o-mini",
			"google/gemini-2.0-flash-001", "google/gemini-2.5-pro-preview",
			"deepseek/deepseek-chat-v3-0324", "deepseek/deepseek-r1",
			"meta-llama/llama-3.3-70b-instruct",
		}
	case model.ProviderClaude:
		return []string{
			"claude-sonnet-4-20250514",
			"claude-3-7-sonnet-20250219",
			"claude-3-5-haiku-20241022",
			"claude-3-5-sonnet-20241022",
			"claude-3-opus-20240229",
		}
	case model.ProviderGemini:
		return []string{
			"gemini-2.5-pro-preview-06-05",
			"gemini-2.5-flash-preview-05-20",
			"gemini-2.0-flash",
			"gemini-2.0-flash-lite",
			"gemini-1.5-pro",
			"gemini-1.5-flash",
		}
	case model.ProviderOpenAICompat:
		return []string{}
	default:
		return []string{}
	}
}
