package handler

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/store"
	"github.com/chowyu12/aiclaw/internal/workspace"
	"github.com/chowyu12/aiclaw/pkg/httputil"
)

type AgentHandler struct {
	store store.Store
}

func NewAgentHandler(s store.Store) *AgentHandler {
	return &AgentHandler{store: s}
}

func (h *AgentHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/agents", h.List)
	mux.HandleFunc("POST /api/v1/agents", h.Create)
	mux.HandleFunc("GET /api/v1/agents/{id}", h.Get)
	mux.HandleFunc("PUT /api/v1/agents/{id}", h.Update)
	mux.HandleFunc("DELETE /api/v1/agents/{id}", h.Delete)
	mux.HandleFunc("POST /api/v1/agents/{id}/reset-token", h.ResetToken)
}

func (h *AgentHandler) List(w http.ResponseWriter, r *http.Request) {
	q := ParseListQuery(r)
	list, total, err := h.store.ListAgents(r.Context(), q)
	if err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OKList(w, list, total)
}

func (h *AgentHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req model.CreateAgentReq
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if req.Name == "" {
		httputil.BadRequest(w, "name is required")
		return
	}
	ctx := r.Context()
	a := &model.Agent{
		Name:              req.Name,
		Description:       req.Description,
		SystemPrompt:      req.SystemPrompt,
		ProviderID:        req.ProviderID,
		ModelName:         req.ModelName,
		Temperature:       req.Temperature,
		MaxTokens:         req.MaxTokens,
		Timeout:           req.Timeout,
		MaxHistory:        req.MaxHistory,
		MaxIterations:     req.MaxIterations,
		ToolSearchEnabled: req.ToolSearchEnabled,
		MemOSEnabled:      req.MemOSEnabled,
		MemOSCfg:          req.MemOSCfg,
		ToolIDs:           model.Int64Slice(req.ToolIDs),
		IsDefault:         req.IsDefault,
	}
	if a.Temperature == 0 {
		a.Temperature = 0.7
	}
	if a.MaxTokens == 0 {
		a.MaxTokens = 4096
	}
	if a.MaxHistory == 0 {
		a.MaxHistory = model.DefaultAgentMaxHistory
	}
	if a.MaxIterations == 0 {
		a.MaxIterations = model.DefaultAgentMaxIterations
	}
	if err := h.store.CreateAgent(ctx, a); err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	workspace.AgentDir(a.UUID)
	httputil.OK(w, a)
}

func (h *AgentHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httputil.BadRequest(w, "invalid id")
		return
	}
	ctx := r.Context()
	a, err := h.store.GetAgent(ctx, id)
	if err != nil {
		httputil.NotFound(w, "agent not found")
		return
	}
	a.Tools = h.resolveTools(ctx, []int64(a.ToolIDs))
	httputil.OK(w, a)
}

func (h *AgentHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httputil.BadRequest(w, "invalid id")
		return
	}
	var req model.UpdateAgentReq
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	ctx := r.Context()
	if err := h.store.UpdateAgent(ctx, id, &req); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httputil.NotFound(w, "agent not found")
			return
		}
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, nil)
}

func (h *AgentHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httputil.BadRequest(w, "invalid id")
		return
	}
	if err := h.store.DeleteAgent(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httputil.NotFound(w, "agent not found")
			return
		}
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, nil)
}

func (h *AgentHandler) ResetToken(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httputil.BadRequest(w, "invalid id")
		return
	}
	newToken, err := h.store.ResetAgentToken(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httputil.NotFound(w, "agent not found")
			return
		}
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, map[string]string{"token": newToken})
}

// ---- helpers ----

func (h *AgentHandler) resolveTools(ctx context.Context, ids []int64) []model.Tool {
	var out []model.Tool
	for _, id := range ids {
		t, err := h.store.GetTool(ctx, id)
		if err != nil || t == nil {
			continue
		}
		out = append(out, *t)
	}
	return out
}

