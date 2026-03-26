package handler

import (
	"net/http"
	"strconv"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/store"
	"github.com/chowyu12/aiclaw/internal/tools"
	"github.com/chowyu12/aiclaw/pkg/httputil"
)

type ToolHandler struct {
	store store.Store
}

func NewToolHandler(s store.Store) *ToolHandler {
	return &ToolHandler{store: s}
}

func (h *ToolHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/tools", h.Create)
	mux.HandleFunc("GET /api/v1/tools", h.List)
	mux.HandleFunc("GET /api/v1/tools/{id}", h.Get)
	mux.HandleFunc("PUT /api/v1/tools/{id}", h.Update)
	mux.HandleFunc("DELETE /api/v1/tools/{id}", h.Delete)
}

func (h *ToolHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req model.CreateToolReq
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	t := &model.Tool{
		Name:          req.Name,
		Description:   req.Description,
		FunctionDef:   req.FunctionDef,
		HandlerType:   req.HandlerType,
		HandlerConfig: req.HandlerConfig,
		Enabled:       true,
	}
	if req.Enabled != nil {
		t.Enabled = *req.Enabled
	}
	if err := h.store.CreateTool(r.Context(), t); err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, t)
}

func (h *ToolHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httputil.BadRequest(w, "invalid id")
		return
	}
	t, err := h.store.GetTool(r.Context(), id)
	if err != nil {
		httputil.NotFound(w, "tool not found")
		return
	}
	httputil.OK(w, t)
}

func (h *ToolHandler) List(w http.ResponseWriter, r *http.Request) {
	q := ParseListQuery(r)

	builtins := tools.DefaultBuiltinDefs()
	dbList, dbTotal, err := h.store.ListTools(r.Context(), q)
	if err != nil {
		httputil.InternalError(w, err.Error())
		return
	}

	var merged []*model.Tool
	for i := range builtins {
		bt := builtins[i]
		bt.ID = -int64(i + 1)
		merged = append(merged, &bt)
	}
	merged = append(merged, dbList...)

	httputil.OKList(w, merged, dbTotal+int64(len(builtins)))
}

func (h *ToolHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httputil.BadRequest(w, "invalid id")
		return
	}
	if id <= 0 {
		httputil.BadRequest(w, "builtin tools cannot be modified")
		return
	}
	var req model.UpdateToolReq
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if err := h.store.UpdateTool(r.Context(), id, req); err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, nil)
}

func (h *ToolHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httputil.BadRequest(w, "invalid id")
		return
	}
	if id <= 0 {
		httputil.BadRequest(w, "builtin tools cannot be deleted")
		return
	}
	if err := h.store.DeleteTool(r.Context(), id); err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, nil)
}
