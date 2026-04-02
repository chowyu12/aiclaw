package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/chowyu12/aiclaw/internal/agent"
	"github.com/chowyu12/aiclaw/internal/auth"
	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/store"
	"github.com/chowyu12/aiclaw/pkg/httputil"
	"github.com/chowyu12/aiclaw/pkg/sse"
)

type ChatHandler struct {
	store    store.Store
	executor *agent.Executor
}

func NewChatHandler(s store.Store, executor *agent.Executor) *ChatHandler {
	return &ChatHandler{store: s, executor: executor}
}

func (h *ChatHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/chat/completions", h.Complete)
	mux.HandleFunc("POST /api/v1/chat/stream", h.Stream)
	mux.HandleFunc("POST /api/v1/chat/retry", h.RetryStream)
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

// RetryStream 重试指定的 assistant 消息：删除该轮对话数据后重新执行。
// 请求体：{ "conversation_id": "uuid", "message_id": 123 }
// message_id 为要重试的 assistant 消息 ID。
func (h *ChatHandler) RetryStream(w http.ResponseWriter, r *http.Request) {
	var req model.RetryRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if req.ConversationID == "" || req.MessageID == 0 {
		httputil.BadRequest(w, "conversation_id and message_id are required")
		return
	}

	conv, err := h.store.GetConversationByUUID(r.Context(), req.ConversationID)
	if err != nil {
		httputil.NotFound(w, "conversation not found")
		return
	}

	assistantMsg, err := h.store.GetMessage(r.Context(), req.MessageID)
	if err != nil || assistantMsg.ConversationID != conv.ID || assistantMsg.Role != "assistant" {
		httputil.BadRequest(w, "invalid assistant message")
		return
	}

	msgs, err := h.store.ListMessages(r.Context(), conv.ID, 500)
	if err != nil {
		httputil.InternalError(w, "load messages failed")
		return
	}
	var userMsg *model.Message
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].ID < assistantMsg.ID && msgs[i].Role == "user" {
			userMsg = &msgs[i]
			break
		}
	}
	if userMsg == nil {
		httputil.BadRequest(w, "user message not found for retry")
		return
	}

	userText := userMsg.Content
	if err := h.store.DeleteMessagesFrom(r.Context(), conv.ID, userMsg.ID); err != nil {
		httputil.InternalError(w, "cleanup old messages failed")
		return
	}

	chatReq := model.ChatRequest{
		ConversationID: req.ConversationID,
		Message:        userText,
	}
	fillIdentity(r, &chatReq)
	if chatReq.UserID == "" {
		chatReq.UserID = "anonymous"
	}

	sseWriter, ok := sse.NewWriter(w)
	if !ok {
		httputil.InternalError(w, "streaming not supported")
		return
	}

	if err := h.executor.ExecuteStream(r.Context(), chatReq, func(chunk model.StreamChunk) error {
		return sseWriter.WriteJSON("message", chunk)
	}); err != nil {
		sseWriter.WriteJSON("error", map[string]string{"error": err.Error()})
		return
	}
	sseWriter.WriteDone()
}

func (h *ChatHandler) ListConversations(w http.ResponseWriter, r *http.Request) {
	q := ParseListQuery(r)
	q.AgentUUID = strings.TrimSpace(r.URL.Query().Get("agent_uuid"))
	userID := r.URL.Query().Get("user_id")
	userPrefix := strings.TrimSpace(r.URL.Query().Get("user_prefix"))
	includeChannels := r.URL.Query().Get("include_channels") == "true"
	if userID == "" && !includeChannels {
		if id := auth.IdentityFromContext(r.Context()); id != nil && id.IsWebSession() {
			userID = auth.DefaultChatUserID
		}
	}
	if userPrefix != "" {
		list, total, err := h.store.ListConversationsByUserPrefix(r.Context(), userPrefix, q)
		if err != nil {
			httputil.InternalError(w, err.Error())
			return
		}
		httputil.OKList(w, list, total)
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
