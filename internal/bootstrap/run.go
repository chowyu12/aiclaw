package bootstrap

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"
	"gopkg.in/natefinch/lumberjack.v2"

	agentpkg "github.com/chowyu12/aiclaw/internal/agent"
	"github.com/chowyu12/aiclaw/internal/auth"
	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/channels"
	"github.com/chowyu12/aiclaw/internal/config"
	"github.com/chowyu12/aiclaw/internal/daemon"
	"github.com/chowyu12/aiclaw/internal/scheduler"
	"github.com/chowyu12/aiclaw/internal/server"
	skillspkg "github.com/chowyu12/aiclaw/internal/skills"
	"github.com/chowyu12/aiclaw/internal/store/gormstore"
	"github.com/chowyu12/aiclaw/internal/tools/browser"
	"github.com/chowyu12/aiclaw/internal/workspace"
)

// Options 命令行与启动选项（由 cmd/server 传入）。
type Options struct {
	ConfigFlag string // -config，空则走默认路径
	Version    string // 构建时注入的版本号
}

// Run 阻塞运行直至收到 SIGINT/SIGTERM；正常退出前关闭 HTTP 与共享浏览器资源。
func Run(opts Options) {
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05",
	})
	log.SetLevel(log.DebugLevel)

	cfgPath := config.ConfigPath(opts.ConfigFlag)
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

	ws, err := workspace.New(cfg.Workspace)
	if err != nil {
		log.WithError(err).Fatal("init workspace failed")
	}
	log.WithField("path", ws.Root()).Info("workspace initialized")

	skillspkg.SyncBuiltinsToDisk(ws.Skills())

	logFile := cfg.Log.File
	if logFile == "" && daemon.IsChild() {
		logFile = daemon.LogFile()
	}
	if logFile == "" && ws.Logs() != "" {
		logFile = filepath.Join(ws.Logs(), "aiclaw.log")
	}
	if logFile != "" {
		fileWriter := &lumberjack.Logger{
			Filename:   logFile,
			MaxSize:    cfg.Log.MaxSize,
			MaxBackups: 3,
			Compress:   true,
		}
		if daemon.IsChild() || cfg.Log.File != "" {
			log.SetOutput(fileWriter)
		} else {
			log.SetOutput(io.MultiWriter(os.Stdout, fileWriter))
		}
		log.WithField("file", logFile).Info("log output to file")
	}

	if cfg.Upload.Dir == "" || cfg.Upload.Dir == "./uploads" {
		cfg.Upload.Dir = ws.Uploads()
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

	rt := config.NewRuntimeConfig(cfgPath, cfg)

	generated, err := config.EnsureAuthWebToken(cfg, cfgPath)
	if err != nil {
		log.WithError(err).Fatal("无法自动生成并保存 auth.web_token：请检查配置文件路径可写，或手动在 config 中设置 auth.web_token")
	}
	if generated {
		log.WithField("web_token", cfg.Auth.WebToken).Warn("首次启动：已自动生成 auth.web_token 并写入配置文件，请用此令牌登录 Web 控制台（勿泄露）")
	}

	auth.SetWebToken(cfg.Auth.WebToken)
	log.WithField("url", webConsoleURL(cfg.Server.Host, cfg.Server.Port, cfg.Auth.WebToken)).Info("open web console with token")

	store, err := gormstore.New(cfg.Database)
	if err != nil {
		log.WithError(err).Fatal("connect database failed")
	}
	defer store.Close()

	store.InitFTS5()

	server.ApplyBrowserToolConfig(cfg.Browser)

	registry := agentpkg.NewToolRegistry()
	executor := agentpkg.NewExecutor(store, registry, ws)

	appCtx, appCancel := context.WithCancel(context.Background())
	defer appCancel()
	sched := startScheduler(appCtx, ws, executor, store)
	defer sched.Stop()

	channelMgr := channels.NewManager(store, executor, rt)
	defer channelMgr.Stop()

	mux := http.NewServeMux()
	server.RegisterAPIRoutes(mux, server.APIParams{
		Store:              store,
		Executor:           executor,
		ChannelMgr:         channelMgr,
		DatabaseConfigured: !cfg.NeedsDatabaseSetup(),
		Upload:             cfg.Upload,
		Version:            cmp.Or(opts.Version, "dev"),
		WS:                 ws,
	})
	server.MountEmbeddedFrontend(mux)

	authCfg := auth.Config{AgentStore: store}
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

	startConfigHotReload(rt)
	startTmpCleanup(ws)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info("shutting down server...")

	executor.Shutdown(30 * time.Second)
	browser.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.WithError(err).Error("server shutdown error")
	}
	log.Info("server stopped")
}

func startConfigHotReload(rt *config.RuntimeConfig) {
	abs, err := filepath.Abs(rt.Path())
	if err != nil {
		log.WithError(err).Warn("config hot reload disabled: abs path failed")
		return
	}
	_, err = config.StartConfigWatcher(abs, func() error {
		if err := rt.ReplaceFromDisk(); err != nil {
			return err
		}
		rt.WithReadLock(func(c *config.Config) {
			if c == nil {
				return
			}
			auth.SetWebToken(c.Auth.WebToken)
			if c.Log.Level != "" {
				if lvl, err := log.ParseLevel(c.Log.Level); err == nil {
					log.SetLevel(lvl)
				}
			}
			server.ApplyBrowserToolConfig(c.Browser)
		})
		return nil
	})
	if err != nil {
		log.WithError(err).Warn("config watcher not started")
		return
	}
	log.WithField("path", abs).Info("config hot reload enabled")
}

func startScheduler(ctx context.Context, ws *workspace.Workspace, executor *agentpkg.Executor, store *gormstore.GormStore) *scheduler.Scheduler {
	jobExecutor := func(ctx context.Context, job *scheduler.Job) (string, error) {
		switch job.Type {
		case scheduler.JobTypePrompt:
			agentUUID := job.AgentUUID
			if agentUUID == "" {
				agents, _, err := store.ListAgents(ctx, model.ListQuery{Page: 1, PageSize: 1})
				if err != nil || len(agents) == 0 {
					return "", fmt.Errorf("no agent found for scheduled job")
				}
				agentUUID = agents[0].UUID
			}
			req := model.ChatRequest{
				AgentUUID: agentUUID,
				UserID:    "scheduler:" + job.ID,
				Message:   job.Prompt,
			}
			result, err := executor.Execute(ctx, req)
			if err != nil {
				return "", err
			}
			return result.Content, nil

		case scheduler.JobTypeCommand:
			cmd := exec.CommandContext(ctx, "sh", "-c", job.Command)
			output, err := cmd.CombinedOutput()
			return string(output), err

		default:
			return "", fmt.Errorf("unknown job type: %s", job.Type)
		}
	}

	sched := scheduler.New(ws, jobExecutor)
	sched.Start(ctx)

	executor.SetSchedulerContextFunc(func(c context.Context) context.Context {
		return scheduler.WithScheduler(c, sched)
	})

	return sched
}

func startTmpCleanup(ws *workspace.Workspace) {
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			ws.CleanupAgentTmpFiles(24 * time.Hour)
		}
	}()
}

func webConsoleURL(host string, port int, token string) string {
	h := strings.TrimSpace(host)
	switch h {
	case "", "0.0.0.0", "::", "[::]":
		h = "localhost"
	}
	return fmt.Sprintf("http://%s:%d/?token=%s", h, port, url.QueryEscape(strings.TrimSpace(token)))
}
