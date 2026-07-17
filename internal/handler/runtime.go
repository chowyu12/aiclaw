package handler

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chowyu12/aiclaw/internal/agent"
	"github.com/chowyu12/aiclaw/internal/auth"
	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/store"
	"github.com/chowyu12/aiclaw/pkg/httputil"
)

type RuntimeHandler struct {
	store    store.Store
	executor *agent.Executor
}

func NewRuntimeHandler(s store.Store, executor *agent.Executor) *RuntimeHandler {
	return &RuntimeHandler{store: s, executor: executor}
}

func (h *RuntimeHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/runtimes", h.List)
	mux.HandleFunc("POST /api/v1/runtimes", h.Create)
	mux.HandleFunc("GET /api/v1/runtimes/{id}", h.Get)
	mux.HandleFunc("PUT /api/v1/runtimes/{id}", h.Update)
	mux.HandleFunc("DELETE /api/v1/runtimes/{id}", h.Delete)
	mux.HandleFunc("POST /api/v1/runtimes/{id}/reset-token", h.ResetToken)
	mux.HandleFunc("GET /api/v1/runtimes/{id}/agents", h.ListRuntimeAgents)
	mux.HandleFunc("PUT /api/v1/runtimes/{id}/agents/{agentType}", h.UpdateRuntimeAgent)

	mux.HandleFunc("POST /api/v1/runtime-daemon/heartbeat", h.Heartbeat)
	mux.HandleFunc("POST /api/v1/runtime-daemon/tasks/claim", h.Claim)
	mux.HandleFunc("POST /api/v1/runtime-daemon/tasks/{id}/events", h.PublishEvent)
	mux.HandleFunc("POST /api/v1/runtime-daemon/tasks/{id}/complete", h.Complete)
}

func (h *RuntimeHandler) List(w http.ResponseWriter, r *http.Request) {
	items, total, err := h.store.ListRuntimes(r.Context(), ParseListQuery(r))
	if err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	now := time.Now()
	for _, item := range items {
		item.RefreshStatus(now)
		if err := h.attachRuntimeAgentConfigs(r.Context(), item); err != nil {
			httputil.InternalError(w, err.Error())
			return
		}
	}
	httputil.OKList(w, items, total)
}

func (h *RuntimeHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req model.CreateRuntimeReq
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Command = strings.TrimSpace(req.Command)
	if req.Name == "" {
		httputil.BadRequest(w, "name is required")
		return
	}
	agentType, ok := model.NormalizeRuntimeAgentType(req.AgentType)
	if !ok {
		httputil.BadRequest(w, "unsupported runtime agent type")
		return
	}
	promptMode, ok := model.NormalizeRuntimePromptMode(req.PromptMode)
	if !ok {
		httputil.BadRequest(w, "unsupported runtime prompt mode")
		return
	}
	if agentType != model.RuntimeAgentTypeCustom {
		promptMode = model.RuntimeAgentPromptMode(agentType)
	}
	if !validRuntimeArgs(req.Args) {
		httputil.BadRequest(w, "runtime arguments contain invalid characters")
		return
	}
	runtime := &model.Runtime{
		Name: req.Name, Description: strings.TrimSpace(req.Description), AgentType: agentType,
		Command: req.Command, Args: model.StringSlice(req.Args), PromptMode: promptMode, Status: model.RuntimeStatusOffline,
	}
	if err := h.store.CreateRuntime(r.Context(), runtime); err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, runtime)
}

func (h *RuntimeHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := runtimePathID(w, r)
	if !ok {
		return
	}
	runtime, err := h.store.GetRuntime(r.Context(), id)
	if err != nil {
		httputil.NotFound(w, "runtime not found")
		return
	}
	runtime.RefreshStatus(time.Now())
	if err := h.attachRuntimeAgentConfigs(r.Context(), runtime); err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, runtime)
}

func (h *RuntimeHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := runtimePathID(w, r)
	if !ok {
		return
	}
	existing, err := h.store.GetRuntime(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httputil.NotFound(w, "runtime not found")
			return
		}
		httputil.InternalError(w, err.Error())
		return
	}
	if existing.Builtin {
		httputil.BadRequest(w, "built-in local runtime is managed automatically")
		return
	}
	var req model.UpdateRuntimeReq
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			httputil.BadRequest(w, "name is required")
			return
		}
		req.Name = &name
	}
	if req.Command != nil {
		command := strings.TrimSpace(*req.Command)
		req.Command = &command
	}
	if req.Args != nil && !validRuntimeArgs(*req.Args) {
		httputil.BadRequest(w, "runtime arguments contain invalid characters")
		return
	}
	agentType := existing.EffectiveAgentType()
	if req.AgentType != nil {
		normalized, ok := model.NormalizeRuntimeAgentType(*req.AgentType)
		if !ok {
			httputil.BadRequest(w, "unsupported runtime agent type")
			return
		}
		req.AgentType = &normalized
		agentType = normalized
	}
	if req.PromptMode != nil {
		normalized, ok := model.NormalizeRuntimePromptMode(*req.PromptMode)
		if !ok {
			httputil.BadRequest(w, "unsupported runtime prompt mode")
			return
		}
		req.PromptMode = &normalized
	}
	if agentType != model.RuntimeAgentTypeCustom {
		promptMode := model.RuntimeAgentPromptMode(agentType)
		req.PromptMode = &promptMode
	} else if req.AgentType != nil && req.PromptMode == nil {
		promptMode := model.RuntimePromptStdin
		req.PromptMode = &promptMode
	}
	if err := h.store.UpdateRuntime(r.Context(), id, req); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httputil.NotFound(w, "runtime not found")
			return
		}
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, nil)
}

func (h *RuntimeHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := runtimePathID(w, r)
	if !ok {
		return
	}
	runtime, err := h.store.GetRuntime(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httputil.NotFound(w, "runtime not found")
			return
		}
		httputil.InternalError(w, err.Error())
		return
	}
	if runtime.Builtin {
		httputil.Error(w, http.StatusConflict, "built-in local runtime cannot be deleted")
		return
	}
	agents, _, err := h.store.ListAgents(r.Context(), model.ListQuery{Page: 1, PageSize: 10000})
	if err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	for _, ag := range agents {
		if ag.ExecutionMode == model.AgentExecutionLocal && ag.RuntimeID == id {
			httputil.Error(w, http.StatusConflict, "runtime is still used by a local agent")
			return
		}
	}
	if err := h.store.DeleteRuntime(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httputil.NotFound(w, "runtime not found")
			return
		}
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, nil)
}

func (h *RuntimeHandler) ResetToken(w http.ResponseWriter, r *http.Request) {
	id, ok := runtimePathID(w, r)
	if !ok {
		return
	}
	runtime, err := h.store.GetRuntime(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httputil.NotFound(w, "runtime not found")
			return
		}
		httputil.InternalError(w, err.Error())
		return
	}
	if runtime.Builtin {
		httputil.Error(w, http.StatusConflict, "built-in local runtime does not use a connection token")
		return
	}
	token, err := h.store.ResetRuntimeToken(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httputil.NotFound(w, "runtime not found")
			return
		}
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, map[string]string{"token": token})
}

func (h *RuntimeHandler) Heartbeat(w http.ResponseWriter, r *http.Request) {
	runtimeID, ok := runtimeIdentity(w, r)
	if !ok {
		return
	}
	var req struct {
		Version string   `json:"version"`
		Agents  []string `json:"agents"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	agents, ok := normalizeDetectedAgents(req.Agents)
	if !ok {
		httputil.BadRequest(w, "unsupported detected runtime agent")
		return
	}
	if err := h.store.TouchRuntime(r.Context(), runtimeID, req.Version, agents, time.Now()); err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	if err := h.store.EnsureRuntimeAgentConfigs(r.Context(), runtimeID, agents); err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, map[string]any{"status": model.RuntimeStatusOnline, "agents": agents})
}

func (h *RuntimeHandler) ListRuntimeAgents(w http.ResponseWriter, r *http.Request) {
	id, ok := runtimePathID(w, r)
	if !ok {
		return
	}
	runtime, err := h.store.GetRuntime(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httputil.NotFound(w, "runtime not found")
			return
		}
		httputil.InternalError(w, err.Error())
		return
	}
	if err := h.attachRuntimeAgentConfigs(r.Context(), runtime); err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, runtime.AgentConfigs)
}

func (h *RuntimeHandler) UpdateRuntimeAgent(w http.ResponseWriter, r *http.Request) {
	id, ok := runtimePathID(w, r)
	if !ok {
		return
	}
	agentType, valid := model.NormalizeRuntimeAgentType(r.PathValue("agentType"))
	if !valid || agentType == model.RuntimeAgentTypeCustom {
		httputil.BadRequest(w, "unsupported runtime agent type")
		return
	}
	runtime, err := h.store.GetRuntime(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httputil.NotFound(w, "runtime not found")
			return
		}
		httputil.InternalError(w, err.Error())
		return
	}
	if !runtime.HasDetectedAgent(agentType) {
		httputil.BadRequest(w, "runtime has not detected this local agent CLI")
		return
	}
	var req model.UpdateRuntimeAgentConfigReq
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if req.ModelName != nil {
		modelName := strings.TrimSpace(*req.ModelName)
		req.ModelName = &modelName
		spec, _ := model.LocalCLISpecFor(agentType)
		if modelName != "" && !spec.SupportsModel() {
			httputil.BadRequest(w, "this local agent CLI does not support per-task model selection")
			return
		}
	}
	if err := h.store.EnsureRuntimeAgentConfigs(r.Context(), id, []string{agentType}); err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	if err := h.store.UpdateRuntimeAgentConfig(r.Context(), id, agentType, req); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httputil.NotFound(w, "runtime agent not found")
			return
		}
		httputil.InternalError(w, err.Error())
		return
	}
	config, err := h.store.GetRuntimeAgentConfig(r.Context(), id, agentType)
	if err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, config)
}

func (h *RuntimeHandler) Claim(w http.ResponseWriter, r *http.Request) {
	runtimeID, ok := runtimeIdentity(w, r)
	if !ok {
		return
	}
	task, err := h.executor.ClaimLocalAgentRun(r.Context(), runtimeID)
	if agent.IsNoRuntimeTask(err) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, task)
}

func (h *RuntimeHandler) PublishEvent(w http.ResponseWriter, r *http.Request) {
	runtimeID, ok := runtimeIdentity(w, r)
	if !ok {
		return
	}
	var req model.RuntimeRunEvent
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if err := h.executor.PublishLocalAgentRun(r.Context(), runtimeID, r.PathValue("id"), req.Delta); err != nil {
		httputil.Error(w, http.StatusConflict, err.Error())
		return
	}
	httputil.OK(w, nil)
}

func (h *RuntimeHandler) Complete(w http.ResponseWriter, r *http.Request) {
	runtimeID, ok := runtimeIdentity(w, r)
	if !ok {
		return
	}
	var req model.RuntimeRunComplete
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	run, err := h.executor.CompleteLocalAgentRun(r.Context(), runtimeID, r.PathValue("id"), req.Content, req.Error)
	if err != nil {
		httputil.Error(w, http.StatusConflict, err.Error())
		return
	}
	httputil.OK(w, run)
}

func runtimePathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httputil.BadRequest(w, "invalid runtime id")
		return 0, false
	}
	return id, true
}

func runtimeIdentity(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id := auth.IdentityFromContext(r.Context())
	if id == nil || !id.IsRuntime() {
		httputil.Forbidden(w, "runtime token required")
		return 0, false
	}
	return id.RuntimeID, true
}

func validRuntimeArgs(args []string) bool {
	for _, arg := range args {
		if strings.ContainsRune(arg, '\x00') {
			return false
		}
	}
	return true
}

func normalizeDetectedAgents(items []string) ([]string, bool) {
	seen := make(map[string]struct{}, len(items))
	result := make([]string, 0, len(items))
	for _, item := range items {
		agentType, ok := model.NormalizeRuntimeAgentType(item)
		if !ok || agentType == model.RuntimeAgentTypeCustom {
			return nil, false
		}
		if _, exists := seen[agentType]; exists {
			continue
		}
		seen[agentType] = struct{}{}
		result = append(result, agentType)
	}
	return result, true
}

func (h *RuntimeHandler) attachRuntimeAgentConfigs(ctx context.Context, runtime *model.Runtime) error {
	if runtime == nil {
		return nil
	}
	configs, err := h.store.ListRuntimeAgentConfigs(ctx, runtime.ID)
	if err != nil {
		return err
	}
	byType := make(map[string]model.RuntimeAgentConfig, len(configs))
	for _, config := range configs {
		byType[config.AgentType] = config
	}
	runtime.AgentConfigs = runtime.AgentConfigs[:0]
	for _, agentType := range runtime.DetectedAgents {
		if config, ok := byType[agentType]; ok {
			runtime.AgentConfigs = append(runtime.AgentConfigs, config)
		}
	}
	return nil
}
