package handler

import (
	"net/http"
	"strconv"
	"strings"

	agentpkg "github.com/chowyu12/aiclaw/internal/agent"
	"github.com/chowyu12/aiclaw/internal/auth"
	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/store"
	"github.com/chowyu12/aiclaw/pkg/httputil"
	"github.com/chowyu12/aiclaw/pkg/sse"
)

type ChatHandler struct {
	store    store.Store
	executor *agentpkg.Executor
}

func NewChatHandler(s store.Store, executor *agentpkg.Executor) *ChatHandler {
	return &ChatHandler{store: s, executor: executor}
}

func (h *ChatHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/chat/completions", h.Complete)
	mux.HandleFunc("POST /api/v1/chat/stream", h.Stream)
	mux.HandleFunc("GET /api/v1/conversations", h.ListConversations)
	mux.HandleFunc("GET /api/v1/conversations/{id}/messages", h.ListMessages)
	mux.HandleFunc("DELETE /api/v1/conversations/{id}", h.DeleteConversation)
	mux.HandleFunc("GET /api/v1/messages/{id}/steps", h.ListSteps)
	mux.HandleFunc("GET /api/v1/conversations/{id}/steps", h.ListConversationSteps)
}

func fillIdentity(r *http.Request, req *model.ChatRequest) {
	id := auth.IdentityFromContext(r.Context())
	if id == nil {
		return
	}
	if req.UserID == "" && id.IsWebSession() {
		req.UserID = auth.DefaultChatUserID
	}
}

func (h *ChatHandler) Complete(w http.ResponseWriter, r *http.Request) {
	var req model.ChatRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	fillIdentity(r, &req)
	if _, err := agentpkg.TryLoadAgent(r.Context(), h.store); err != nil {
		httputil.BadRequest(w, "no agent configured: add a model provider in settings first")
		return
	}
	if req.Message == "" {
		httputil.BadRequest(w, "message is required")
		return
	}
	if req.UserID == "" {
		req.UserID = "anonymous"
	}

	result, err := h.executor.Execute(r.Context(), req)
	if err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, model.ChatResponse{
		ConversationID: result.ConversationID,
		Message:        result.Content,
		TokensUsed:     result.TokensUsed,
		Steps:          result.Steps,
	})
}

func (h *ChatHandler) Stream(w http.ResponseWriter, r *http.Request) {
	var req model.ChatRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	fillIdentity(r, &req)
	if _, err := agentpkg.TryLoadAgent(r.Context(), h.store); err != nil {
		httputil.BadRequest(w, "no agent configured: add a model provider in settings first")
		return
	}
	if req.Message == "" {
		httputil.BadRequest(w, "message is required")
		return
	}
	if req.UserID == "" {
		req.UserID = "anonymous"
	}

	sseWriter, ok := sse.NewWriter(w)
	if !ok {
		httputil.InternalError(w, "streaming not supported")
		return
	}

	err := h.executor.ExecuteStream(r.Context(), req, func(chunk model.StreamChunk) error {
		return sseWriter.WriteJSON("message", chunk)
	})
	if err != nil {
		sseWriter.WriteJSON("error", map[string]string{"error": err.Error()})
		return
	}
	sseWriter.WriteDone()
}

func (h *ChatHandler) ListConversations(w http.ResponseWriter, r *http.Request) {
	q := ParseListQuery(r)
	userID := r.URL.Query().Get("user_id")
	userPrefix := strings.TrimSpace(r.URL.Query().Get("user_prefix"))
	if userID == "" {
		if id := auth.IdentityFromContext(r.Context()); id != nil && id.IsWebSession() {
			userID = auth.DefaultChatUserID
		}
	}
	if userPrefix != "" {
		allQ := q
		allQ.Page = 1
		allQ.PageSize = 10000
		all, _, err := h.store.ListConversations(r.Context(), "", allQ)
		if err != nil {
			httputil.InternalError(w, err.Error())
			return
		}
		filtered := make([]*model.Conversation, 0, len(all))
		for _, c := range all {
			if c != nil && strings.HasPrefix(c.UserID, userPrefix) {
				filtered = append(filtered, c)
			}
		}
		total := int64(len(filtered))
		page := max(q.Page, 1)
		pageSize := max(q.PageSize, 1)
		offset := (page - 1) * pageSize
		if offset >= len(filtered) {
			httputil.OKList(w, []*model.Conversation{}, total)
			return
		}
		end := min(offset+pageSize, len(filtered))
		httputil.OKList(w, filtered[offset:end], total)
		return
	}
	list, total, err := h.store.ListConversations(r.Context(), userID, q)
	if err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OKList(w, list, total)
}

func (h *ChatHandler) ListMessages(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httputil.BadRequest(w, "invalid id")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	withSteps := r.URL.Query().Get("with_steps") == "true"

	msgs, err := h.store.ListMessages(r.Context(), id, limit)
	if err != nil {
		httputil.InternalError(w, err.Error())
		return
	}

	for i, msg := range msgs {
		if withSteps && msg.Role == "assistant" {
			steps, err := h.store.ListExecutionSteps(r.Context(), msg.ID)
			if err == nil {
				msgs[i].Steps = steps
			}
		}
		files, err := h.store.ListFilesByMessage(r.Context(), msg.ID)
		if err == nil && len(files) > 0 {
			msgs[i].Files = files
		}
	}

	httputil.OK(w, msgs)
}

func (h *ChatHandler) ListSteps(w http.ResponseWriter, r *http.Request) {
	messageID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httputil.BadRequest(w, "invalid message id")
		return
	}
	steps, err := h.store.ListExecutionSteps(r.Context(), messageID)
	if err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, steps)
}

func (h *ChatHandler) ListConversationSteps(w http.ResponseWriter, r *http.Request) {
	convID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httputil.BadRequest(w, "invalid conversation id")
		return
	}
	steps, err := h.store.ListExecutionStepsByConversation(r.Context(), convID)
	if err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, steps)
}

func (h *ChatHandler) DeleteConversation(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httputil.BadRequest(w, "invalid id")
		return
	}
	if err := h.store.DeleteConversation(r.Context(), id); err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, nil)
}
