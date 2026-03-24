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

// TokenResolver resolves an agent token to an Agent.
type TokenResolver interface {
	GetAgentByToken(ctx context.Context, token string) (*model.Agent, error)
}

// Config Web 端令牌由 CurrentWebToken() 提供（支持热加载）；此处仅保留 Agent 解析。
type Config struct {
	TokenResolver TokenResolver
}

// Middleware returns an HTTP middleware that performs authentication and authorization.
//
// Flow: extract token → authenticate（Web web_token / Agent Token）→ authorize → next handler。
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

			if err := authorize(id, r.Method, r.URL.Path); err != nil {
				httputil.Forbidden(w, err.Error())
				return
			}

			ctx := WithIdentity(r.Context(), id)
			next.ServeHTTP(w, r.WithContext(ctx))
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

func authenticate(cfg Config, ctx context.Context, tokenStr string) (*Identity, error) {
	if strings.HasPrefix(tokenStr, "ag-") {
		return authenticateAgentToken(cfg.TokenResolver, ctx, tokenStr)
	}
	wt := strings.TrimSpace(CurrentWebToken())
	if wt != "" && secureStringEqual(strings.TrimSpace(tokenStr), wt) {
		return &Identity{Kind: KindWebSession}, nil
	}
	return nil, errors.New("invalid token")
}

func authenticateAgentToken(resolver TokenResolver, ctx context.Context, token string) (*Identity, error) {
	if _, err := resolver.GetAgentByToken(ctx, token); err != nil {
		return nil, errors.New("invalid agent token")
	}
	return &Identity{Kind: KindAgentToken}, nil
}

func authorize(id *Identity, method, path string) error {
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

func secureStringEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
