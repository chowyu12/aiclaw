package server

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/config"
	"github.com/chowyu12/aiclaw/internal/handler"
)

// RunDatabaseSetupWizard 在数据库未配置时启动仅含 setup API + 前端的临时 HTTP 服务，完成后关闭。
func RunDatabaseSetupWizard(addr, cfgPath string, cfg *config.Config) {
	done := make(chan struct{})

	mux := http.NewServeMux()
	handler.NewSetupHandler(cfg, cfgPath, done).Register(mux)
	MountEmbeddedFrontend(mux)

	srv := &http.Server{
		Addr:        addr,
		Handler:     handler.Logger(handler.CORS(mux)),
		ReadTimeout: 30 * time.Second,
		IdleTimeout: 120 * time.Second,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Fatal("setup server error")
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-done:
		log.Info("database setup completed via web wizard")
	case <-quit:
		log.Info("setup interrupted, shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
		os.Exit(0)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(ctx)

	signal.Stop(quit)
}
