package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/chowyu12/aiclaw/internal/auth"
	memorypkg "github.com/chowyu12/aiclaw/internal/memory"
	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/store"
	"github.com/chowyu12/aiclaw/pkg/httputil"
)

type MemoryHandler struct {
	store    store.Store
	memories *memorypkg.Service
}

func NewMemoryHandler(s store.Store) *MemoryHandler {
	return &MemoryHandler{store: s, memories: memorypkg.NewService(s)}
}

func (h *MemoryHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/memories", h.List)
	mux.HandleFunc("POST /api/v1/memories", h.Create)
	mux.HandleFunc("GET /api/v1/memories/{id}", h.Get)
	mux.HandleFunc("PATCH /api/v1/memories/{id}", h.Update)
	mux.HandleFunc("DELETE /api/v1/memories/{id}", h.Delete)
	mux.HandleFunc("GET /api/v1/memories/{id}/revisions", h.ListRevisions)
	mux.HandleFunc("GET /api/v1/memories/{id}/evidence", h.ListEvidence)
}

func (h *MemoryHandler) List(w http.ResponseWriter, r *http.Request) {
	q := model.MemoryListQuery{
		UserID:      memoryUserID(r),
		AgentUUID:   strings.TrimSpace(r.URL.Query().Get("agent_uuid")),
		Scope:       model.MemoryScope(strings.TrimSpace(r.URL.Query().Get("scope"))),
		Status:      model.MemoryStatus(strings.TrimSpace(r.URL.Query().Get("status"))),
		Kind:        model.MemoryKind(strings.TrimSpace(r.URL.Query().Get("kind"))),
		Keyword:     strings.TrimSpace(r.URL.Query().Get("keyword")),
		IncludeAll:  r.URL.Query().Get("include_all") == "true",
		OnlyPending: r.URL.Query().Get("pending") == "true",
	}
	q.Page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	q.PageSize, _ = strconv.Atoi(r.URL.Query().Get("page_size"))
	items, total, err := h.store.ListMemories(r.Context(), q)
	if err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OKList(w, items, total)
}

func (h *MemoryHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req model.CreateMemoryRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if req.Status == "" {
		req.Status = model.MemoryStatusActive
	}
	item, err := h.memories.Upsert(r.Context(), memorypkg.ExecutionContext{
		UserID:    memoryUserID(r),
		AgentUUID: req.AgentUUID,
	}, req, "user")
	if err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}
	httputil.OK(w, item)
}

func (h *MemoryHandler) Get(w http.ResponseWriter, r *http.Request) {
	item, err := h.memoryForRequest(r)
	if err != nil {
		h.writeMemoryError(w, err)
		return
	}
	revisions, _ := h.store.ListMemoryRevisions(r.Context(), item.ID)
	evidence, _ := h.store.ListMemoryEvidence(r.Context(), item.ID)
	httputil.OK(w, map[string]any{"memory": item, "revisions": revisions, "evidence": evidence})
}

func (h *MemoryHandler) Update(w http.ResponseWriter, r *http.Request) {
	var req model.UpdateMemoryRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	item, err := h.memories.Update(r.Context(), memoryUserID(r), r.PathValue("id"), req, "user")
	if err != nil {
		h.writeMemoryError(w, err)
		return
	}
	httputil.OK(w, item)
}

func (h *MemoryHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.memories.Forget(r.Context(), memoryUserID(r), r.PathValue("id"), "user"); err != nil {
		h.writeMemoryError(w, err)
		return
	}
	httputil.OK(w, nil)
}

func (h *MemoryHandler) ListRevisions(w http.ResponseWriter, r *http.Request) {
	item, err := h.memoryForRequest(r)
	if err != nil {
		h.writeMemoryError(w, err)
		return
	}
	items, err := h.store.ListMemoryRevisions(r.Context(), item.ID)
	if err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, items)
}

func (h *MemoryHandler) ListEvidence(w http.ResponseWriter, r *http.Request) {
	item, err := h.memoryForRequest(r)
	if err != nil {
		h.writeMemoryError(w, err)
		return
	}
	items, err := h.store.ListMemoryEvidence(r.Context(), item.ID)
	if err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, items)
}

func (h *MemoryHandler) memoryForRequest(r *http.Request) (*model.MemoryItem, error) {
	return h.store.GetMemoryByUUID(r.Context(), memoryUserID(r), r.PathValue("id"))
}

func (h *MemoryHandler) writeMemoryError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		httputil.NotFound(w, "memory not found")
		return
	}
	httputil.BadRequest(w, err.Error())
}

func memoryUserID(r *http.Request) string {
	if identity := auth.IdentityFromContext(r.Context()); identity != nil && identity.IsWebSession() {
		return auth.DefaultChatUserID
	}
	return auth.DefaultChatUserID
}
