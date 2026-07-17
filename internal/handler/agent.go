package handler

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/store"
	"github.com/chowyu12/aiclaw/internal/workspace"
	"github.com/chowyu12/aiclaw/pkg/httputil"
	"github.com/chowyu12/aiclaw/pkg/modelcaps"
)

type AgentHandler struct {
	store store.Store
	ws    *workspace.Workspace
}

func NewAgentHandler(s store.Store, ws *workspace.Workspace) *AgentHandler {
	return &AgentHandler{store: s, ws: ws}
}

func (h *AgentHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/agents", h.List)
	mux.HandleFunc("POST /api/v1/agents", h.Create)
	mux.HandleFunc("GET /api/v1/agents/{id}", h.Get)
	mux.HandleFunc("PUT /api/v1/agents/{id}", h.Update)
	mux.HandleFunc("DELETE /api/v1/agents/{id}", h.Delete)
	mux.HandleFunc("POST /api/v1/agents/{id}/reset-token", h.ResetToken)
	mux.HandleFunc("GET /api/v1/model-caps", h.GetModelCaps)
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
	localAgentType, ok := model.NormalizeRuntimeAgentType(req.LocalAgentType)
	if !ok {
		httputil.BadRequest(w, "unsupported local_agent_type")
		return
	}
	a := &model.Agent{
		Name:              req.Name,
		Description:       req.Description,
		ExecutionMode:     normalizeExecutionMode(req.ExecutionMode),
		RuntimeID:         req.RuntimeID,
		LocalAgentType:    localAgentType,
		WorkingDir:        strings.TrimSpace(req.WorkingDir),
		SystemPrompt:      req.SystemPrompt,
		ProviderID:        req.ProviderID,
		ModelName:         req.ModelName,
		FastModelName:     req.FastModelName,
		FallbackModelName: req.FallbackModelName,
		Temperature:       req.Temperature,
		MaxTokens:         req.MaxTokens,
		Timeout:           req.Timeout,
		MaxHistory:        req.MaxHistory,
		MaxIterations:     req.MaxIterations,
		TokenBudget:       req.TokenBudget,
		EnableThinking:    req.EnableThinking,
		ReasoningEffort:   req.ReasoningEffort,
		EnableWebSearch:   req.EnableWebSearch,
		WebSearchMode:     normalizeWebSearchMode(req.WebSearchMode),
		SearchEngineID:    req.SearchEngineID,
		ToolSearchEnabled: req.ToolSearchEnabled,
		ToolIDs:           model.Int64Slice(req.ToolIDs),
		IsDefault:         req.IsDefault,
	}
	if a.Temperature == 0 {
		a.Temperature = 0.7
	}
	if a.MaxHistory == 0 {
		a.MaxHistory = model.DefaultAgentMaxHistory
	}
	if a.MaxIterations == 0 {
		a.MaxIterations = model.DefaultAgentMaxIterations
	}
	if msg, err := h.validateExecution(ctx, a); err != nil {
		httputil.InternalError(w, err.Error())
		return
	} else if msg != "" {
		httputil.BadRequest(w, msg)
		return
	}
	if msg, err := h.validateExternalWebSearch(ctx, a); err != nil {
		httputil.InternalError(w, err.Error())
		return
	} else if msg != "" {
		httputil.BadRequest(w, msg)
		return
	}
	if err := h.store.CreateAgent(ctx, a); err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	h.ws.AgentDir(a.UUID)
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
	if req.WebSearchMode != nil {
		mode := normalizeWebSearchMode(*req.WebSearchMode)
		req.WebSearchMode = &mode
	}
	ctx := r.Context()
	existing, err := h.store.GetAgent(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httputil.NotFound(w, "agent not found")
			return
		}
		httputil.InternalError(w, err.Error())
		return
	}
	candidate := *existing
	if req.ExecutionMode != nil {
		mode := normalizeExecutionMode(*req.ExecutionMode)
		req.ExecutionMode = &mode
		candidate.ExecutionMode = mode
	}
	if req.RuntimeID != nil {
		candidate.RuntimeID = *req.RuntimeID
	}
	if req.LocalAgentType != nil {
		localAgentType, ok := model.NormalizeRuntimeAgentType(*req.LocalAgentType)
		if !ok {
			httputil.BadRequest(w, "unsupported local_agent_type")
			return
		}
		req.LocalAgentType = &localAgentType
		candidate.LocalAgentType = localAgentType
	}
	if req.WorkingDir != nil {
		workingDir := strings.TrimSpace(*req.WorkingDir)
		req.WorkingDir = &workingDir
		candidate.WorkingDir = workingDir
	}
	if req.EnableWebSearch != nil {
		candidate.EnableWebSearch = *req.EnableWebSearch
	}
	if req.WebSearchMode != nil {
		candidate.WebSearchMode = *req.WebSearchMode
	}
	if req.SearchEngineID != nil {
		candidate.SearchEngineID = *req.SearchEngineID
	}
	if msg, err := h.validateExecution(ctx, &candidate); err != nil {
		httputil.InternalError(w, err.Error())
		return
	} else if msg != "" {
		httputil.BadRequest(w, msg)
		return
	}
	if msg, err := h.validateExternalWebSearch(ctx, &candidate); err != nil {
		httputil.InternalError(w, err.Error())
		return
	} else if msg != "" {
		httputil.BadRequest(w, msg)
		return
	}
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

func normalizeWebSearchMode(mode string) string {
	switch mode {
	case model.WebSearchModeExternal:
		return model.WebSearchModeExternal
	default:
		return model.WebSearchModeBuiltin
	}
}

func normalizeExecutionMode(mode string) string {
	if strings.TrimSpace(mode) == model.AgentExecutionLocal {
		return model.AgentExecutionLocal
	}
	return model.AgentExecutionManaged
}

func (h *AgentHandler) validateExecution(ctx context.Context, a *model.Agent) (string, error) {
	if a == nil || a.ExecutionMode != model.AgentExecutionLocal {
		return "", nil
	}
	if a.RuntimeID <= 0 {
		return "runtime_id is required for a local agent", nil
	}
	runtime, err := h.store.GetRuntime(ctx, a.RuntimeID)
	if errors.Is(err, sql.ErrNoRows) {
		return "selected runtime not found", nil
	} else if err != nil {
		return "", err
	}
	if a.EffectiveLocalAgentType() == model.RuntimeAgentTypeCustom {
		if strings.TrimSpace(runtime.Command) == "" {
			if len(runtime.DetectedAgents) == 0 {
				return "runtime has not detected a local agent CLI yet", nil
			}
			return "select a detected local agent CLI", nil
		}
		return "", nil
	}
	if !runtime.HasDetectedAgent(a.EffectiveLocalAgentType()) {
		return "selected local agent CLI is not detected on this runtime", nil
	}
	config, err := h.store.GetRuntimeAgentConfig(ctx, runtime.ID, a.EffectiveLocalAgentType())
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if config != nil && !config.Enabled {
		return "selected local agent CLI is disabled on this runtime", nil
	}
	return "", nil
}

func (h *AgentHandler) validateExternalWebSearch(ctx context.Context, a *model.Agent) (string, error) {
	if a == nil || !a.EnableWebSearch || a.EffectiveWebSearchMode() != model.WebSearchModeExternal {
		return "", nil
	}
	if a.SearchEngineID <= 0 {
		return "search_engine_id is required when external web search is enabled", nil
	}
	cfg, err := h.store.GetSearchEngineConfig(ctx, a.SearchEngineID)
	if errors.Is(err, sql.ErrNoRows) {
		return "selected search engine not found", nil
	}
	if err != nil {
		return "", err
	}
	if !cfg.Enabled {
		return "selected search engine is disabled", nil
	}
	return "", nil
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

func (h *AgentHandler) GetModelCaps(w http.ResponseWriter, r *http.Request) {
	modelName := r.URL.Query().Get("model")
	if modelName == "" {
		httputil.BadRequest(w, "model parameter is required")
		return
	}
	caps := modelcaps.GetModelCaps(modelName)
	httputil.OK(w, caps)
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
