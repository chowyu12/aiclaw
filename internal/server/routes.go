package server

import (
	"net/http"

	agentpkg "github.com/chowyu12/aiclaw/internal/agent"
	"github.com/chowyu12/aiclaw/internal/channels"
	"github.com/chowyu12/aiclaw/internal/config"
	"github.com/chowyu12/aiclaw/internal/handler"
	"github.com/chowyu12/aiclaw/internal/store"
	"github.com/chowyu12/aiclaw/internal/workspace"
	"github.com/chowyu12/aiclaw/pkg/httputil"
)

// APIParams 注册 REST API 所需的依赖与认证相关配置片段。
type APIParams struct {
	Store              store.Store
	Executor           *agentpkg.Executor
	ChannelMgr         *channels.Manager
	DatabaseConfigured bool
	Upload             config.UploadConfig
	Version            string
	WS                 *workspace.Workspace
}

// RegisterAPIRoutes 注册全部 /api/v1 业务路由（不含全局中间件）。
func RegisterAPIRoutes(mux *http.ServeMux, p APIParams) {
	handler.NewAuthHandler(p.DatabaseConfigured).Register(mux)
	handler.NewProviderHandler(p.Store).Register(mux)
	handler.NewAgentHandler(p.Store, p.WS).Register(mux)
	handler.NewToolHandler(p.Store).Register(mux)
	handler.NewSkillsHandler(p.WS).Register(mux)
	handler.NewMCPHandler(p.Store).Register(mux)
	handler.NewChannelHandler(p.Store, p.ChannelMgr).Register(mux)
	handler.NewChatHandler(p.Store, p.Executor).Register(mux)
	handler.NewFileHandler(p.Store, p.Upload).Register(mux)
	handler.NewMetricsHandler().Register(mux)

	ver := p.Version
	mux.HandleFunc("GET /api/v1/version", func(w http.ResponseWriter, _ *http.Request) {
		httputil.OK(w, map[string]string{"version": ver})
	})
}
