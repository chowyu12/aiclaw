package handler

import (
	"net/http"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/store"
	"github.com/chowyu12/aiclaw/pkg/httputil"
)

type MCPHandler struct {
	store store.Store
}

func NewMCPHandler(s store.Store) *MCPHandler {
	return &MCPHandler{store: s}
}

func (h *MCPHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/runtime/mcp", h.Get)
	mux.HandleFunc("PUT /api/v1/runtime/mcp", h.Put)
}

func (h *MCPHandler) Get(w http.ResponseWriter, r *http.Request) {
	list, err := h.store.ListMCPServers(r.Context())
	if err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	if list == nil {
		list = []model.MCPServer{}
	}
	httputil.OK(w, map[string]any{"list": list})
}

type putMCPReq struct {
	Servers []model.MCPServer `json:"servers"`
}

func (h *MCPHandler) Put(w http.ResponseWriter, r *http.Request) {
	var req putMCPReq
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if err := h.store.ReplaceMCPServers(r.Context(), req.Servers); err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, nil)
}
