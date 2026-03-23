package handler

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/chowyu12/aiclaw/internal/auth"
	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/pkg/httputil"
)

type AuthHandler struct {
	databaseConfigured bool
}

func NewAuthHandler(databaseConfigured bool) *AuthHandler {
	return &AuthHandler{databaseConfigured: databaseConfigured}
}

func (h *AuthHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/setup/check", h.SetupCheck)
	mux.HandleFunc("GET /api/v1/auth/setup-check", h.SetupCheck)
	mux.HandleFunc("POST /api/v1/auth/login", h.Login)
	mux.HandleFunc("GET /api/v1/auth/me", h.Me)
}

func (h *AuthHandler) SetupCheck(w http.ResponseWriter, _ *http.Request) {
	httputil.OK(w, map[string]any{
		"database_configured": h.databaseConfigured,
	})
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req model.WebTokenLoginReq
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request")
		return
	}
	in := strings.TrimSpace(req.Token)
	secret := strings.TrimSpace(auth.CurrentWebToken())
	if in == "" {
		httputil.BadRequest(w, "token required")
		return
	}
	if secret == "" || len(in) != len(secret) || subtle.ConstantTimeCompare([]byte(in), []byte(secret)) != 1 {
		httputil.Unauthorized(w, "invalid token")
		return
	}

	// 与中间件校验一致：前端保存 web_token，后续请求 Bearer 携带同一值。
	httputil.OK(w, map[string]string{"token": in})
}

func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	id := auth.IdentityFromContext(r.Context())
	if id == nil || !id.IsWebSession() {
		httputil.Unauthorized(w, "unauthorized")
		return
	}
	httputil.OK(w, map[string]bool{"ok": true})
}
