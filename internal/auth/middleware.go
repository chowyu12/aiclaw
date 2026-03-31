package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/pkg/httputil"
)

type AgentStore interface {
	GetAgentByToken(ctx context.Context, token string) (*model.Agent, error)
}

type Config struct {
	AgentStore AgentStore
}

// Middleware 认证流程：提取 token → Web token 或 Agent token 校验 → 权限检查 → 放行。
func Middleware(cfg Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isPublic(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			tokenStr := extractToken(r)
			if tokenStr == "" {
				httputil.Unauthorized(w, "missing token")
				return
			}

			id, err := authenticate(cfg, r.Context(), tokenStr)
			if err != nil {
				httputil.Unauthorized(w, "invalid token")
				return
			}

			if err := authorize(id, r.URL.Path); err != nil {
				httputil.Forbidden(w, err.Error())
				return
			}

			next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), id)))
		})
	}
}

func extractToken(r *http.Request) string {
	v := r.Header.Get("Authorization")
	if strings.HasPrefix(v, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(v, "Bearer "))
	}
	if v != "" {
		return strings.TrimSpace(v)
	}
	return strings.TrimSpace(r.URL.Query().Get("token"))
}

func authenticate(cfg Config, ctx context.Context, token string) (*Identity, error) {
	if strings.HasPrefix(token, "ag-") {
		ag, err := cfg.AgentStore.GetAgentByToken(ctx, token)
		if err != nil || ag == nil || ag.Token == "" {
			return nil, errors.New("invalid agent token")
		}
		return &Identity{Kind: KindAgentToken}, nil
	}
	wt := strings.TrimSpace(CurrentWebToken())
	if wt != "" && subtle.ConstantTimeCompare([]byte(strings.TrimSpace(token)), []byte(wt)) == 1 {
		return &Identity{Kind: KindWebSession}, nil
	}
	return nil, errors.New("invalid token")
}

func authorize(id *Identity, path string) error {
	if id.IsAgentToken() {
		if !strings.HasPrefix(path, "/api/v1/chat/") {
			return errors.New("agent token can only access chat endpoints")
		}
		return nil
	}
	if id.IsWebSession() {
		return nil
	}
	return errors.New("unauthorized")
}

func isPublic(path string) bool {
	if !strings.HasPrefix(path, "/api/") {
		return true
	}
	if strings.HasPrefix(path, "/api/v1/webhooks/") {
		return true
	}
	return path == "/api/v1/auth/login" ||
		strings.HasPrefix(path, "/api/v1/setup/")
}
