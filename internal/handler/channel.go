package handler

import (
	"context"
	"crypto/subtle"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/chowyu12/aiclaw/internal/channels"
	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/store"
	"github.com/chowyu12/aiclaw/pkg/httputil"
	log "github.com/sirupsen/logrus"
)

type ChannelHandler struct {
	store store.Store
	mgr   *channels.Manager
}

func NewChannelHandler(s store.Store, mgr *channels.Manager) *ChannelHandler {
	return &ChannelHandler{store: s, mgr: mgr}
}

func (h *ChannelHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/channels", h.Create)
	mux.HandleFunc("GET /api/v1/channels", h.List)
	mux.HandleFunc("GET /api/v1/channels/{id}", h.Get)
	mux.HandleFunc("PATCH /api/v1/channels/{id}/enabled", h.SetEnabled)
	mux.HandleFunc("GET /api/v1/channels/{id}/conversations", h.ListConversations)
	mux.HandleFunc("GET /api/v1/channels/{id}/conversations/{conversation_id}/messages", h.ListConversationMessages)
	mux.HandleFunc("DELETE /api/v1/channels/{id}/conversations/{conversation_id}", h.DeleteConversation)
	mux.HandleFunc("PUT /api/v1/channels/{id}", h.Update)
	mux.HandleFunc("DELETE /api/v1/channels/{id}", h.Delete)
	mux.HandleFunc("POST /api/v1/webhooks/channels/{uuid}", h.WebhookPOST)
	mux.HandleFunc("GET /api/v1/webhooks/channels/{uuid}", h.WebhookGET)
}

func (h *ChannelHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req model.CreateChannelReq
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if req.Name == "" {
		httputil.BadRequest(w, "name is required")
		return
	}
	if !model.IsValidChannelType(req.ChannelType) {
		httputil.BadRequest(w, "invalid channel_type")
		return
	}
	c := &model.Channel{
		Name:         req.Name,
		ChannelType:  req.ChannelType,
		Enabled:      true,
		WebhookToken: req.WebhookToken,
		Config:       req.Config,
		Description:  req.Description,
	}
	if req.Enabled != nil {
		c.Enabled = *req.Enabled
	}
	if err := h.store.CreateChannel(r.Context(), c); err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	h.mgr.Refresh(r.Context())
	httputil.OK(w, c)
}

func (h *ChannelHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httputil.BadRequest(w, "invalid id")
		return
	}
	c, err := h.store.GetChannel(r.Context(), id)
	if err != nil {
		httputil.NotFound(w, "channel not found")
		return
	}
	httputil.OK(w, c)
}

func (h *ChannelHandler) List(w http.ResponseWriter, r *http.Request) {
	q := ParseListQuery(r)
	list, total, err := h.store.ListChannels(r.Context(), q)
	if err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OKList(w, list, total)
}

func (h *ChannelHandler) SetEnabled(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httputil.BadRequest(w, "invalid id")
		return
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if req.Enabled == nil {
		httputil.BadRequest(w, "enabled is required")
		return
	}
	if err := h.store.UpdateChannel(r.Context(), id, model.UpdateChannelReq{
		Enabled: req.Enabled,
	}); err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	h.mgr.Refresh(r.Context())
	httputil.OK(w, nil)
}

func (h *ChannelHandler) ListConversations(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httputil.BadRequest(w, "invalid id")
		return
	}
	ch, err := h.store.GetChannel(r.Context(), id)
	if err != nil {
		httputil.NotFound(w, "channel not found")
		return
	}
	q := ParseListQuery(r)
	threadFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("thread_key")))
	senderFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("sender_id")))

	userPrefix := "channel:" + ch.UUID + ":"
	conversations, total, err := h.store.ListConversationsByUserPrefix(r.Context(), userPrefix, q)
	if err != nil {
		httputil.InternalError(w, err.Error())
		return
	}

	threadRows, err := h.store.ListChannelThreads(r.Context(), ch.ID)
	if err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	threadMap := make(map[string][]string)
	for _, row := range threadRows {
		keys := threadMap[row.ConversationUUID]
		if !slices.Contains(keys, row.ThreadKey) {
			threadMap[row.ConversationUUID] = append(keys, row.ThreadKey)
		}
	}
	for uuid, keys := range threadMap {
		slices.Sort(keys)
		threadMap[uuid] = keys
	}

	items := make([]*model.ChannelConversationItem, 0, len(conversations))
	for _, conv := range conversations {
		if conv == nil {
			continue
		}
		senderID := strings.TrimPrefix(conv.UserID, userPrefix)
		if senderFilter != "" && !strings.Contains(strings.ToLower(senderID), senderFilter) {
			continue
		}
		threadKeys := threadMap[conv.UUID]
		if threadFilter != "" {
			matched := false
			for _, key := range threadKeys {
				if strings.Contains(strings.ToLower(key), threadFilter) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		msgCount, _ := h.store.CountMessages(r.Context(), conv.ID)
		lastUser, lastReply := h.resolveConversationLastMessages(r.Context(), conv.ID)
		items = append(items, &model.ChannelConversationItem{
			ConversationID:   conv.ID,
			ConversationUUID: conv.UUID,
			Title:            conv.Title,
			UserID:           conv.UserID,
			SenderID:         senderID,
			ThreadKeys:       threadKeys,
			MessageCount:     msgCount,
			LastUserMessage:  lastUser,
			LastReplyMessage: lastReply,
			UpdatedAt:        conv.UpdatedAt,
			CreatedAt:        conv.CreatedAt,
		})
	}
	httputil.OKList(w, items, total)
}

func (h *ChannelHandler) ListConversationMessages(w http.ResponseWriter, r *http.Request) {
	channelID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httputil.BadRequest(w, "invalid channel id")
		return
	}
	conversationID, err := strconv.ParseInt(r.PathValue("conversation_id"), 10, 64)
	if err != nil {
		httputil.BadRequest(w, "invalid conversation id")
		return
	}
	ch, err := h.store.GetChannel(r.Context(), channelID)
	if err != nil {
		httputil.NotFound(w, "channel not found")
		return
	}
	conv, err := h.store.GetConversation(r.Context(), conversationID)
	if err != nil {
		httputil.NotFound(w, "conversation not found")
		return
	}
	ownerPrefix := "channel:" + ch.UUID + ":"
	if !strings.HasPrefix(conv.UserID, ownerPrefix) {
		httputil.Forbidden(w, "conversation does not belong to this channel")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	msgs, err := h.store.ListMessages(r.Context(), conversationID, limit)
	if err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	withSteps := r.URL.Query().Get("with_steps") == "true"
	for i, msg := range msgs {
		if withSteps && msg.Role == "assistant" {
			steps, err := h.store.ListExecutionSteps(r.Context(), msg.ID)
			if err == nil {
				msgs[i].Steps = steps
			}
		}
	}
	httputil.OK(w, msgs)
}

func (h *ChannelHandler) DeleteConversation(w http.ResponseWriter, r *http.Request) {
	channelID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httputil.BadRequest(w, "invalid channel id")
		return
	}
	conversationID, err := strconv.ParseInt(r.PathValue("conversation_id"), 10, 64)
	if err != nil {
		httputil.BadRequest(w, "invalid conversation id")
		return
	}

	ch, err := h.store.GetChannel(r.Context(), channelID)
	if err != nil {
		httputil.NotFound(w, "channel not found")
		return
	}
	conv, err := h.store.GetConversation(r.Context(), conversationID)
	if err != nil {
		httputil.NotFound(w, "conversation not found")
		return
	}
	ownerPrefix := "channel:" + ch.UUID + ":"
	if !strings.HasPrefix(conv.UserID, ownerPrefix) {
		httputil.Forbidden(w, "conversation does not belong to this channel")
		return
	}
	if err := h.store.DeleteConversation(r.Context(), conversationID); err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	if err := h.store.DeleteChannelThreadsByConversation(r.Context(), channelID, conv.UUID); err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	httputil.OK(w, nil)
}

func (h *ChannelHandler) resolveConversationLastMessages(ctx context.Context, conversationID int64) (string, string) {
	msgs, err := h.store.ListMessages(ctx, conversationID, 20)
	if err != nil || len(msgs) == 0 {
		return "", ""
	}
	lastUser := ""
	lastReply := ""
	for i := len(msgs) - 1; i >= 0; i-- {
		msg := msgs[i]
		text := channels.TruncateRunes(strings.TrimSpace(msg.Content), 120)
		if text == "" {
			continue
		}
		if lastUser == "" && msg.Role == "user" {
			lastUser = text
		}
		if lastReply == "" && msg.Role == "assistant" {
			lastReply = text
		}
		if lastUser != "" && lastReply != "" {
			break
		}
	}
	return lastUser, lastReply
}

func (h *ChannelHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httputil.BadRequest(w, "invalid id")
		return
	}
	var req model.UpdateChannelReq
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if err := h.store.UpdateChannel(r.Context(), id, req); err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	h.mgr.Refresh(r.Context())
	httputil.OK(w, nil)
}

func (h *ChannelHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httputil.BadRequest(w, "invalid id")
		return
	}
	if err := h.store.DeleteChannel(r.Context(), id); err != nil {
		httputil.NotFound(w, "channel not found")
		return
	}
	h.mgr.Refresh(r.Context())
	httputil.OK(w, nil)
}

func (h *ChannelHandler) WebhookPOST(w http.ResponseWriter, r *http.Request) {
	h.serveWebhook(w, r, false)
}

func (h *ChannelHandler) WebhookGET(w http.ResponseWriter, r *http.Request) {
	h.serveWebhook(w, r, true)
}

func (h *ChannelHandler) serveWebhook(w http.ResponseWriter, r *http.Request, isGET bool) {
	u := r.PathValue("uuid")
	if u == "" {
		httputil.BadRequest(w, "missing uuid")
		return
	}
	ch, err := h.store.GetChannelByUUID(r.Context(), u)
	if err != nil || !ch.Enabled {
		httputil.NotFound(w, "channel not found")
		return
	}
	skipToken := isGET && r.URL.Query().Get("echostr") != ""
	if !skipToken && !h.verifyWebhookToken(r, ch) {
		httputil.Unauthorized(w, "invalid webhook token")
		return
	}
	if isGET {
		drv := channels.WebhookFor(ch.ChannelType)
		out := drv.HandleGET(channels.ConfigFromModel(ch), r.URL.Query())
		out.WriteTo(w)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		httputil.BadRequest(w, "read body")
		return
	}
	drv := channels.WebhookFor(ch.ChannelType)
	out, inbound := drv.HandlePOST(channels.ConfigFromModel(ch), body, r.Header.Get("Content-Type"), r.Header)
	out.WriteTo(w)

	bridge := h.mgr.Bridge()
	if inbound != nil && bridge != nil {
		log.WithFields(log.Fields{
			"channel_id": ch.ID, "channel_type": ch.ChannelType, "body_bytes": len(body),
		}).Info("[Channel] webhook POST dispatched")
		bridge.HandleInboundAsync(r.Context(), ch, inbound, drv)
	} else {
		log.WithFields(log.Fields{
			"channel_id": ch.ID, "channel_type": ch.ChannelType, "body_bytes": len(body),
		}).Debug("[Channel] webhook POST (no inbound message)")
	}
}

func (h *ChannelHandler) verifyWebhookToken(r *http.Request, ch *model.Channel) bool {
	if ch.WebhookToken == "" {
		return true
	}
	tok := r.Header.Get("X-Webhook-Token")
	if tok == "" {
		tok = r.URL.Query().Get("token")
	}
	return subtle.ConstantTimeCompare([]byte(tok), []byte(ch.WebhookToken)) == 1
}
