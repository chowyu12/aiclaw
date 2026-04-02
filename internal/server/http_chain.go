package server

import (
	"net/http"
	"time"

	"github.com/chowyu12/aiclaw/internal/auth"
	"github.com/chowyu12/aiclaw/internal/handler"
)

var defaultRateLimiter = handler.NewRateLimiter(120, time.Minute)

// WrapWithAuthAndLog 套上 RequestID、限流、CORS、鉴权与请求日志。
func WrapWithAuthAndLog(mux http.Handler, authCfg auth.Config) http.Handler {
	return handler.RequestID(
		handler.Logger(
			defaultRateLimiter.Middleware(
				handler.CORS(
					auth.Middleware(authCfg)(mux),
				),
			),
		),
	)
}
