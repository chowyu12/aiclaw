package handler

import (
	"cmp"
	"fmt"
	"net/http"
	"time"

	"github.com/chowyu12/aiclaw/internal/config"
	"github.com/chowyu12/aiclaw/internal/store/gormstore"
	"github.com/chowyu12/aiclaw/pkg/httputil"
)

type SetupHandler struct {
	cfg        *config.Config
	configPath string
	done       chan struct{}
}

func NewSetupHandler(cfg *config.Config, configPath string, done chan struct{}) *SetupHandler {
	return &SetupHandler{cfg: cfg, configPath: configPath, done: done}
}

func (h *SetupHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/setup/check", h.Check)
	mux.HandleFunc("POST /api/v1/setup/database/test", h.TestDatabase)
	mux.HandleFunc("POST /api/v1/setup/database", h.SaveDatabase)
}

func (h *SetupHandler) Check(w http.ResponseWriter, _ *http.Request) {
	httputil.OK(w, map[string]any{
		"database_configured": !h.cfg.NeedsDatabaseSetup(),
	})
}

type databaseSetupReq struct {
	Driver   string `json:"driver"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	Database string `json:"database"`
	Charset  string `json:"charset"`
	SSLMode  string `json:"ssl_mode"`
	DSN      string `json:"dsn"`
}

func (r *databaseSetupReq) toDatabaseConfig() (config.DatabaseConfig, error) {
	cfg := config.DatabaseConfig{Driver: r.Driver}
	switch r.Driver {
	case "sqlite":
		cfg.DSN = cmp.Or(r.DSN, "go_ai_agent.db")
	case "mysql":
		if r.Host == "" || r.Database == "" {
			return cfg, fmt.Errorf("host and database are required")
		}
		port := cmp.Or(r.Port, 3306)
		charset := cmp.Or(r.Charset, "utf8mb4")
		cfg.DSN = fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=%s&parseTime=True&loc=Local",
			r.User, r.Password, r.Host, port, r.Database, charset)
	case "postgres":
		if r.Host == "" || r.Database == "" {
			return cfg, fmt.Errorf("host and database are required")
		}
		port := cmp.Or(r.Port, 5432)
		sslMode := cmp.Or(r.SSLMode, "disable")
		cfg.DSN = fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
			r.Host, port, r.User, r.Password, r.Database, sslMode)
	default:
		return cfg, fmt.Errorf("unsupported driver: %s", r.Driver)
	}
	return cfg, nil
}

func (h *SetupHandler) TestDatabase(w http.ResponseWriter, r *http.Request) {
	var req databaseSetupReq
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request")
		return
	}
	dbCfg, err := req.toDatabaseConfig()
	if err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}
	if err := gormstore.TestConnection(dbCfg); err != nil {
		httputil.OK(w, map[string]any{"success": false, "error": err.Error()})
		return
	}
	httputil.OK(w, map[string]any{"success": true})
}

func (h *SetupHandler) SaveDatabase(w http.ResponseWriter, r *http.Request) {
	var req databaseSetupReq
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request")
		return
	}
	dbCfg, err := req.toDatabaseConfig()
	if err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}
	if err := gormstore.TestConnection(dbCfg); err != nil {
		httputil.BadRequest(w, "数据库连接失败: "+err.Error())
		return
	}

	h.cfg.Database = dbCfg
	if err := h.cfg.Save(h.configPath); err != nil {
		httputil.InternalError(w, "保存配置失败: "+err.Error())
		return
	}

	httputil.OK(w, map[string]string{"message": "数据库配置已保存"})

	go func() {
		time.Sleep(500 * time.Millisecond)
		close(h.done)
	}()
}
