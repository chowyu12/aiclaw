package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"

	agentpkg "github.com/chowyu12/aiclaw/internal/agent"
	"github.com/chowyu12/aiclaw/internal/auth"
	"github.com/chowyu12/aiclaw/internal/config"
	"github.com/chowyu12/aiclaw/internal/seed"
	"github.com/chowyu12/aiclaw/internal/server"
	"github.com/chowyu12/aiclaw/internal/store/gormstore"
	"github.com/chowyu12/aiclaw/internal/tool/browser"
	"github.com/chowyu12/aiclaw/internal/workspace"
)

var configFile = flag.String("config", "", "config file path (default: ~/.aiclaw/config.yaml)")

func main() {
	flag.Parse()

	log.SetFormatter(&log.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05",
	})
	log.SetLevel(log.DebugLevel)

	cfgPath := config.ConfigPath(*configFile)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.WithError(err).Fatal("load config failed")
	}
	log.WithField("path", cfgPath).Info("config loaded")

	if cfg.Log.Level != "" {
		if lvl, err := log.ParseLevel(cfg.Log.Level); err == nil {
			log.SetLevel(lvl)
			log.WithField("level", lvl).Info("log level configured")
		} else {
			log.WithFields(log.Fields{"level": cfg.Log.Level, "error": err}).Warn("invalid log level, using debug")
		}
	}

	if err := workspace.Init(cfg.Workspace); err != nil {
		log.WithError(err).Fatal("init workspace failed")
	}
	log.WithField("path", workspace.Root()).Info("workspace initialized")

	if cfg.Upload.Dir == "" || cfg.Upload.Dir == "./uploads" {
		cfg.Upload.Dir = workspace.Uploads()
	}

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)

	if cfg.NeedsDatabaseSetup() {
		log.WithField("addr", addr).Warn("database not configured, starting setup wizard")
		log.Infof("→ please open http://localhost:%d in your browser to configure database", cfg.Server.Port)
		server.RunDatabaseSetupWizard(addr, cfgPath, cfg)

		cfg, err = config.Load(cfgPath)
		if err != nil {
			log.WithError(err).Fatal("reload config after setup failed")
		}
		log.Info("database configured, continuing startup...")
	}

	config.SetRuntime(cfgPath, cfg)

	generated, err := config.EnsureAuthWebToken(cfg, cfgPath)
	if err != nil {
		log.WithError(err).Fatal("无法自动生成并保存 auth.web_token：请检查配置文件路径可写，或手动在 config 中设置 auth.web_token")
	}
	if generated {
		log.WithField("web_token", cfg.Auth.WebToken).Warn("首次启动：已自动生成 auth.web_token 并写入配置文件，请用此令牌登录 Web 控制台（勿泄露）")
	}

	auth.SetWebToken(cfg.Auth.WebToken)

	store, err := gormstore.New(cfg.Database)
	if err != nil {
		log.WithError(err).Fatal("connect database failed")
	}
	defer store.Close()

	seed.Init(context.Background(), store)

	if err := agentpkg.InitSingletonAgent(context.Background(), store); err != nil {
		log.WithError(err).Fatal("init singleton agent failed")
	}

	server.ApplyBrowserToolConfig(cfg.Browser)

	registry := agentpkg.NewToolRegistry()
	executor := agentpkg.NewExecutor(store, registry)

	mux := http.NewServeMux()
	server.RegisterAPIRoutes(mux, server.APIParams{
		Store:              store,
		Executor:           executor,
		DatabaseConfigured: !cfg.NeedsDatabaseSetup(),
		Upload:             cfg.Upload,
	})
	server.MountEmbeddedFrontend(mux)

	authCfg := auth.Config{
		TokenResolver: auth.AgentTokenResolver{
			Providers: store,
		},
	}
	wrapped := server.WrapWithAuthAndLog(mux, authCfg)

	srv := &http.Server{
		Addr:        addr,
		Handler:     wrapped,
		ReadTimeout: 30 * time.Second,
		IdleTimeout: 120 * time.Second,
	}

	go func() {
		log.WithField("addr", addr).Info("server starting")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Fatal("server error")
		}
	}()

	startConfigHotReload(cfgPath)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info("shutting down server...")

	browser.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.WithError(err).Error("server shutdown error")
	}
	log.Info("server stopped")
}

func startConfigHotReload(cfgPath string) {
	abs, err := filepath.Abs(cfgPath)
	if err != nil {
		log.WithError(err).Warn("config hot reload disabled: abs path failed")
		return
	}
	_, err = config.StartConfigWatcher(abs, func() error {
		if err := config.ReplaceRuntimeFromDisk(); err != nil {
			return err
		}
		config.RT.Mu.RLock()
		c := config.RT.Cfg
		config.RT.Mu.RUnlock()
		if c == nil {
			return nil
		}
		auth.SetWebToken(c.Auth.WebToken)
		if err := agentpkg.ReloadSingletonFromConfig(&c.Agent); err != nil {
			log.WithError(err).Warn("hot reload: 保留内存中的 Agent（yaml 中 agent 段不完整时可手工修正）")
		}
		if c.Log.Level != "" {
			if lvl, err := log.ParseLevel(c.Log.Level); err == nil {
				log.SetLevel(lvl)
			}
		}
		server.ApplyBrowserToolConfig(c.Browser)
		return nil
	})
	if err != nil {
		log.WithError(err).Warn("config watcher not started")
		return
	}
	log.WithField("path", abs).Info("config hot reload enabled")
}
