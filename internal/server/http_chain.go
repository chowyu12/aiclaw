package server

import (
	"net/http"

	"github.com/chowyu12/aiclaw/internal/auth"
	"github.com/chowyu12/aiclaw/internal/handler"
)

// WrapWithAuthAndLog 套上 CORS、WebToken/Agent 鉴权与请求日志。
func WrapWithAuthAndLog(mux http.Handler, authCfg auth.Config) http.Handler {
	return handler.Logger(handler.CORS(auth.Middleware(authCfg)(mux)))
}
