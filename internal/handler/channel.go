package handler

import (
	"crypto/subtle"
	"io"
	"net/http"
	"strconv"

	agentpkg "github.com/chowyu12/aiclaw/internal/agent"
	"github.com/chowyu12/aiclaw/internal/channels"
	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/store"
	"github.com/chowyu12/aiclaw/pkg/httputil"
	log "github.com/sirupsen/logrus"
)

type ChannelHandler struct {
	store  store.Store
	bridge *channels.Bridge
}

func NewChannelHandler(s store.Store, exec *agentpkg.Executor) *ChannelHandler {
	return &ChannelHandler{
		store:  s,
		bridge: channels.NewBridge(s, exec),
	}
}

func (h *ChannelHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/channels", h.Create)
	mux.HandleFunc("GET /api/v1/channels", h.List)
	mux.HandleFunc("GET /api/v1/channels/{id}", h.Get)
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
	channels.RefreshChannelRuntimes(r.Context())
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
	channels.RefreshChannelRuntimes(r.Context())
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
	channels.RefreshChannelRuntimes(r.Context())
	httputil.OK(w, nil)
}

// WebhookPOST 各平台事件回调入口（需将 URL 配置到对应开放平台）。若设置了 WebhookToken，请求须带 Header X-Webhook-Token 或 Query token。
// 企业微信 wecom：智能机器人走 WebSocket 长连接（bot_id+secret），HTTP 入口仅探测；业务入站由对应 ChannelDriver 处理。
func (h *ChannelHandler) WebhookPOST(w http.ResponseWriter, r *http.Request) {
	h.serveWebhook(w, r, false)
}

// WebhookGET 供企业微信/微信等 URL 校验（echostr）或健康探测；具体验签逻辑按平台扩展。
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
	// 企业微信 / 微信客服 URL 验证：GET 带 echostr，无自定义 Header；验签应使用 config 内 token，此处先放行回显。
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

	if inbound != nil && h.bridge != nil {
		log.WithFields(log.Fields{
			"channel_id": ch.ID, "channel_type": ch.ChannelType, "body_bytes": len(body),
		}).Info("[Channel] webhook POST dispatched")
		h.bridge.HandleInboundAsync(r.Context(), ch, inbound, drv)
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
