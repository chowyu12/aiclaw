package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/chowyu12/aiclaw/internal/agent"
	"github.com/chowyu12/aiclaw/internal/auth"
	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/store"
	harnesspkg "github.com/chowyu12/aiclaw/pkg/harness"
	"github.com/chowyu12/aiclaw/pkg/httputil"
	"github.com/chowyu12/aiclaw/pkg/sse"
)

type ChatHandler struct {
	store    store.Store
	executor *agent.Executor
}

type agentRunDetail struct {
	Run    *model.AgentRun       `json:"run"`
	Steps  []model.ExecutionStep `json:"steps"`
	Files  []*model.File         `json:"files,omitzero"`
	Plan   *model.PlanState      `json:"plan,omitzero"`
	Memory *model.MemoryContext  `json:"memory,omitzero"`
}

func NewChatHandler(s store.Store, executor *agent.Executor) *ChatHandler {
	return &ChatHandler{store: s, executor: executor}
}

func (h *ChatHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/chat/completions", h.Complete)
	mux.HandleFunc("POST /api/v1/chat/stream", h.Stream)
	mux.HandleFunc("POST /api/v1/chat/retry", h.RetryStream)
	mux.HandleFunc("POST /api/v1/chat/retry/runs", h.StartRetryRun)
	mux.HandleFunc("POST /api/v1/chat/runs", h.StartRun)
	mux.HandleFunc("GET /api/v1/agent-runs", h.ListRuns)
	mux.HandleFunc("GET /api/v1/agent-runs/{id}", h.GetRun)
	mux.HandleFunc("GET /api/v1/agent-runs/{id}/stream", h.StreamRun)
	mux.HandleFunc("DELETE /api/v1/agent-runs/{id}", h.CancelRun)
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

	Metrics.IncChat()
	result, err := h.executor.Execute(r.Context(), req)
	if err != nil {
		Metrics.IncErr()
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, model.ChatResponse{
		RunID:          result.RunID,
		ConversationID: result.ConversationID,
		Message:        result.Content,
		TokensUsed:     result.TokensUsed,
		Steps:          result.Steps,
		Files:          result.ToolFiles,
		Plan:           result.Plan,
		Memory:         result.Memory,
	})
}

// StartRun starts an Agent independently from the request lifetime. Clients
// receive the run ID immediately and consume the same StreamChunk protocol via
// GET /agent-runs/{id}/stream.
func (h *ChatHandler) StartRun(w http.ResponseWriter, r *http.Request) {
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

	Metrics.IncChat()
	run, err := h.executor.StartBackgroundRun(r.Context(), req)
	if err != nil {
		Metrics.IncErr()
		if strings.Contains(err.Error(), "already has a running") {
			httputil.Error(w, http.StatusConflict, err.Error())
			return
		}
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, run)
}

func (h *ChatHandler) ListRuns(w http.ResponseWriter, r *http.Request) {
	q := model.AgentRunListQuery{
		AgentUUID:        strings.TrimSpace(r.URL.Query().Get("agent_uuid")),
		ConversationUUID: strings.TrimSpace(r.URL.Query().Get("conversation_id")),
		UserID:           strings.TrimSpace(r.URL.Query().Get("user_id")),
		Status:           model.AgentRunStatus(strings.TrimSpace(r.URL.Query().Get("status"))),
	}
	q.Page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	q.PageSize, _ = strconv.Atoi(r.URL.Query().Get("page_size"))
	if q.UserID == "" {
		if identity := auth.IdentityFromContext(r.Context()); identity != nil && identity.IsWebSession() {
			q.UserID = auth.DefaultChatUserID
		}
	}
	runs, total, err := h.store.ListAgentRuns(r.Context(), q)
	if err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OKList(w, runs, total)
}

func (h *ChatHandler) GetRun(w http.ResponseWriter, r *http.Request) {
	detail, err := h.loadRunDetail(r, r.PathValue("id"))
	if err != nil {
		h.writeRunLoadError(w, err)
		return
	}
	httputil.OK(w, detail)
}

func (h *ChatHandler) StreamRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")
	run, events, unsubscribe, active, err := h.executor.SubscribeAgentRun(r.Context(), runID)
	if err != nil {
		h.writeRunLoadError(w, err)
		return
	}
	sseWriter, ok := sse.NewWriter(w)
	if !ok {
		httputil.InternalError(w, "streaming not supported")
		return
	}
	_ = sseWriter.WritePing()
	stopPing := sseWriter.StartPing(r.Context(), 0)
	defer stopPing()
	if unsubscribe != nil {
		defer unsubscribe()
	}

	if !active {
		h.writeStoredRunTerminal(sseWriter, r, run)
		return
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-events:
			if !ok {
				latest, getErr := h.executor.GetAgentRun(r.Context(), runID)
				if getErr == nil {
					h.writeStoredRunTerminal(sseWriter, r, latest)
				}
				return
			}
			if event.Chunk != nil {
				if err := writeStreamChunk(sseWriter, *event.Chunk); err != nil {
					return
				}
				continue
			}
			if err := sseWriter.WriteJSON("run", event); err != nil {
				return
			}
			if event.Type == model.AgentRunEventCompleted || event.Type == model.AgentRunEventFailed || event.Type == model.AgentRunEventCancelled {
				_ = sseWriter.WriteDone()
				return
			}
		}
	}
}

func (h *ChatHandler) CancelRun(w http.ResponseWriter, r *http.Request) {
	run, err := h.executor.CancelAgentRun(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeRunLoadError(w, err)
		return
	}
	httputil.OK(w, run)
}

func (h *ChatHandler) loadRunDetail(r *http.Request, runID string) (*agentRunDetail, error) {
	run, err := h.executor.GetAgentRun(r.Context(), runID)
	if err != nil {
		return nil, err
	}
	steps, err := h.store.ListExecutionStepsByRun(r.Context(), runID)
	if err != nil {
		return nil, err
	}
	detail := &agentRunDetail{Run: run, Steps: steps}
	if run.MessageID == 0 {
		return detail, nil
	}
	files, err := h.store.ListFilesByMessage(r.Context(), run.MessageID)
	if err == nil {
		detail.Files = files
	}
	detail.Plan = h.planForMessage(r, run.MessageID)
	if memories, err := h.store.ListMemoryUsageByMessage(r.Context(), run.MessageID); err == nil && len(memories) > 0 {
		detail.Memory = &model.MemoryContext{Items: memories}
	}
	return detail, nil
}

func (h *ChatHandler) writeStoredRunTerminal(sseWriter *sse.Writer, r *http.Request, run *model.AgentRun) {
	detail, err := h.loadRunDetail(r, run.UUID)
	if err == nil {
		run = detail.Run
	}
	if err == nil && run.Status == model.AgentRunSucceeded {
		_ = writeStreamChunk(sseWriter, model.StreamChunk{
			RunID:          detail.Run.UUID,
			ConversationID: detail.Run.ConversationUUID,
			MessageID:      detail.Run.MessageID,
			Done:           true,
			Content:        detail.Run.Content,
			TokensUsed:     detail.Run.TokensUsed,
			DurationMs:     detail.Run.DurationMs,
			Steps:          detail.Steps,
			Files:          detail.Files,
			Plan:           detail.Plan,
			Memory:         detail.Memory,
		})
	}
	eventType := model.AgentRunEventCompleted
	if run.Status == model.AgentRunFailed {
		eventType = model.AgentRunEventFailed
	}
	if run.Status == model.AgentRunCancelled {
		eventType = model.AgentRunEventCancelled
	}
	_ = sseWriter.WriteJSON("run", model.AgentRunEvent{Type: eventType, RunID: run.UUID, Status: run.Status, Run: run, Error: run.Error})
	_ = sseWriter.WriteDone()
}

func (h *ChatHandler) writeRunLoadError(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		httputil.NotFound(w, "agent run not found")
		return
	}
	httputil.InternalError(w, err.Error())
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
	_ = sseWriter.WritePing()
	stopPing := sseWriter.StartPing(r.Context(), 0)
	defer stopPing()

	Metrics.IncChat()
	err := h.executor.ExecuteStream(r.Context(), req, func(chunk model.StreamChunk) error {
		return writeStreamChunk(sseWriter, chunk)
	})
	if err != nil {
		Metrics.IncErr()
		sseWriter.WriteJSON("error", map[string]string{"error": err.Error()})
		return
	}
	sseWriter.WriteDone()
}

// RetryStream is kept for clients that still expect a request-bound retry SSE.
// New clients should use StartRetryRun so retries share the durable run flow.
// 请求体：{ "conversation_id": "uuid", "message_id": 123 }
// message_id 为要重试的 assistant 消息 ID。
func (h *ChatHandler) RetryStream(w http.ResponseWriter, r *http.Request) {
	chatReq, status, message := h.prepareRetryRequest(r)
	if status != 0 {
		httputil.Error(w, status, message)
		return
	}

	sseWriter, ok := sse.NewWriter(w)
	if !ok {
		httputil.InternalError(w, "streaming not supported")
		return
	}
	_ = sseWriter.WritePing()
	stopPing := sseWriter.StartPing(r.Context(), 0)
	defer stopPing()

	Metrics.IncChat()
	if err := h.executor.ExecuteStream(r.Context(), chatReq, func(chunk model.StreamChunk) error {
		return writeStreamChunk(sseWriter, chunk)
	}); err != nil {
		sseWriter.WriteJSON("error", map[string]string{"error": err.Error()})
		return
	}
	sseWriter.WriteDone()
}

// StartRetryRun clears the retried turn and starts a fresh durable run. This
// makes retries work consistently for both managed and Local Runtime agents.
func (h *ChatHandler) StartRetryRun(w http.ResponseWriter, r *http.Request) {
	chatReq, status, message := h.prepareRetryRequest(r)
	if status != 0 {
		httputil.Error(w, status, message)
		return
	}

	Metrics.IncChat()
	run, err := h.executor.StartBackgroundRun(r.Context(), chatReq)
	if err != nil {
		Metrics.IncErr()
		if strings.Contains(err.Error(), "already has a running") {
			httputil.Error(w, http.StatusConflict, err.Error())
			return
		}
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, run)
}

// prepareRetryRequest validates ownership, removes the user turn being retried
// and all of its derived records, then returns the fresh run input.
func (h *ChatHandler) prepareRetryRequest(r *http.Request) (model.ChatRequest, int, string) {
	var req model.RetryRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		return model.ChatRequest{}, http.StatusBadRequest, "invalid request body"
	}
	if req.ConversationID == "" || req.MessageID == 0 {
		return model.ChatRequest{}, http.StatusBadRequest, "conversation_id and message_id are required"
	}

	conv, err := h.store.GetConversationByUUID(r.Context(), req.ConversationID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.ChatRequest{}, http.StatusNotFound, "conversation not found"
		}
		return model.ChatRequest{}, http.StatusInternalServerError, "load conversation failed"
	}
	chatReq := model.ChatRequest{ConversationID: req.ConversationID, AgentUUID: conv.AgentUUID}
	fillIdentity(r, &chatReq)
	if chatReq.UserID == "" {
		chatReq.UserID = "anonymous"
	}
	if conv.UserID != "" && conv.UserID != chatReq.UserID {
		return model.ChatRequest{}, http.StatusForbidden, "conversation does not belong to this user"
	}
	if active, err := h.hasActiveConversationRun(r, conv.UUID); err != nil {
		return model.ChatRequest{}, http.StatusInternalServerError, "check active agent run failed"
	} else if active {
		return model.ChatRequest{}, http.StatusConflict, "stop the active agent run before retrying"
	}

	assistantMsg, err := h.store.GetMessage(r.Context(), req.MessageID)
	if err != nil || assistantMsg.ConversationID != conv.ID || assistantMsg.Role != "assistant" {
		return model.ChatRequest{}, http.StatusBadRequest, "invalid assistant message"
	}

	msgs, err := h.store.ListMessages(r.Context(), conv.ID, 500)
	if err != nil {
		return model.ChatRequest{}, http.StatusInternalServerError, "load messages failed"
	}
	var userMsg *model.Message
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].ID < assistantMsg.ID && msgs[i].Role == "user" {
			userMsg = &msgs[i]
			break
		}
	}
	if userMsg == nil {
		return model.ChatRequest{}, http.StatusBadRequest, "user message not found for retry"
	}

	if err := h.store.DeleteMessagesFrom(r.Context(), conv.ID, userMsg.ID); err != nil {
		return model.ChatRequest{}, http.StatusInternalServerError, "cleanup old messages failed"
	}
	chatReq.Message = userMsg.Content
	return chatReq, 0, ""
}

// writeStreamChunk preserves the legacy message event while exposing stable
// harness protocol records as their own SSE event type.
func writeStreamChunk(sseWriter *sse.Writer, chunk model.StreamChunk) error {
	if event, ok := chunk.HarnessEvent.(harnesspkg.Event); ok {
		return sseWriter.WriteJSON("harness", event)
	}
	if event, ok := chunk.HarnessEvent.(*harnesspkg.Event); ok && event != nil {
		return sseWriter.WriteJSON("harness", event)
	}
	return sseWriter.WriteJSON("message", chunk)
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
		if msg.Role == "assistant" {
			msgs[i].Plan = h.planForMessage(r, msg.ID)
			if memories, err := h.store.ListMemoryUsageByMessage(r.Context(), msg.ID); err == nil && len(memories) > 0 {
				msgs[i].Memory = &model.MemoryContext{Items: memories}
			}
		}
	}

	httputil.OK(w, msgs)
}

func (h *ChatHandler) planForMessage(r *http.Request, messageID int64) *model.PlanState {
	run, err := h.store.GetPlanRunByMessage(r.Context(), messageID)
	if err != nil || run == nil {
		return nil
	}
	items, err := h.store.ListPlanItems(r.Context(), run.ID)
	if err != nil {
		return nil
	}
	return &model.PlanState{
		ID:             run.ID,
		UUID:           run.UUID,
		ConversationID: run.ConversationID,
		MessageID:      run.MessageID,
		Goal:           run.Goal,
		Source:         run.Source,
		Status:         run.Status,
		RevisionReason: run.RevisionReason,
		Items:          items,
		UpdatedAt:      run.UpdatedAt,
	}
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
	conv, err := h.store.GetConversation(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httputil.NotFound(w, "conversation not found")
			return
		}
		httputil.InternalError(w, err.Error())
		return
	}
	if active, err := h.hasActiveConversationRun(r, conv.UUID); err != nil {
		httputil.InternalError(w, err.Error())
		return
	} else if active {
		httputil.Error(w, http.StatusConflict, "stop the active agent run before deleting this conversation")
		return
	}
	if err := h.store.DeleteConversation(r.Context(), id); err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, nil)
}

func (h *ChatHandler) hasActiveConversationRun(r *http.Request, conversationUUID string) (bool, error) {
	for _, status := range []model.AgentRunStatus{model.AgentRunQueued, model.AgentRunRunning} {
		runs, _, err := h.store.ListAgentRuns(r.Context(), model.AgentRunListQuery{
			ConversationUUID: conversationUUID,
			Status:           status,
			Page:             1,
			PageSize:         1,
		})
		if err != nil {
			return false, err
		}
		if len(runs) > 0 {
			return true, nil
		}
	}
	return false, nil
}
