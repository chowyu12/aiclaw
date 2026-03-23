package handler

import (
	"context"
	"net/http"

	agentpkg "github.com/chowyu12/aiclaw/internal/agent"
	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/store"
	"github.com/chowyu12/aiclaw/pkg/httputil"
)

type AgentHandler struct {
	store store.Store
}

func NewAgentHandler(s store.Store) *AgentHandler {
	return &AgentHandler{store: s}
}

func (h *AgentHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/agent", h.GetSingleton)
	mux.HandleFunc("PUT /api/v1/agent", h.UpdateSingleton)
	mux.HandleFunc("POST /api/v1/agent/reset-token", h.ResetTokenSingleton)
}

func (h *AgentHandler) GetSingleton(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a, err := agentpkg.TryLoadAgent(ctx, h.store)
	if err != nil {
		httputil.NotFound(w, "no agent configured: add at least one model provider first, or check config.yaml agent section")
		return
	}
	a.Tools = h.resolveTools(ctx, a.ToolIDs)
	httputil.OK(w, a)
}

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

func (h *AgentHandler) UpdateSingleton(w http.ResponseWriter, r *http.Request) {
	var req model.UpdateAgentReq
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	ctx := r.Context()
	a, err := agentpkg.TryLoadAgent(ctx, h.store)
	if err != nil {
		httputil.NotFound(w, "no agent configured: add at least one model provider first, or check config.yaml agent section")
		return
	}
	mergeAgentUpdate(a, &req)
	if req.ToolSearchEnabled != nil && *req.ToolSearchEnabled {
		a.ToolIDs = nil
	} else if req.ToolIDs != nil {
		a.ToolIDs = req.ToolIDs
	}
	if err := agentpkg.SaveAgent(a); err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, nil)
}

func mergeAgentUpdate(a *model.Agent, req *model.UpdateAgentReq) {
	if req.Name != nil {
		a.Name = *req.Name
	}
	if req.Description != nil {
		a.Description = *req.Description
	}
	if req.SystemPrompt != nil {
		a.SystemPrompt = *req.SystemPrompt
	}
	if req.ProviderID != nil {
		a.ProviderID = *req.ProviderID
	}
	if req.ModelName != nil {
		a.ModelName = *req.ModelName
	}
	if req.Temperature != nil {
		a.Temperature = *req.Temperature
	}
	if req.MaxTokens != nil {
		a.MaxTokens = *req.MaxTokens
	}
	if req.Timeout != nil {
		a.Timeout = *req.Timeout
	}
	if req.MaxHistory != nil {
		a.MaxHistory = *req.MaxHistory
	}
	if req.MaxIterations != nil {
		a.MaxIterations = *req.MaxIterations
	}
	if req.ToolSearchEnabled != nil {
		a.ToolSearchEnabled = *req.ToolSearchEnabled
	}
	if req.MemOSEnabled != nil {
		a.MemOSEnabled = *req.MemOSEnabled
	}
	if req.MemOSCfg != nil {
		a.MemOSCfg = *req.MemOSCfg
	}
}

func (h *AgentHandler) ResetTokenSingleton(w http.ResponseWriter, r *http.Request) {
	tok, err := agentpkg.UpdateAgentToken()
	if err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, map[string]string{"token": tok})
}
